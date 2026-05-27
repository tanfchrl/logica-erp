// Package permission implements the 4-layer permission engine:
// RBAC (role_permission), row-level (user_permission), field-level (field_permission), multi-company scope.
package permission

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// Action is one of the doctype-level actions checked by the engine.
type Action string

const (
	ActionRead   Action = "read"
	ActionWrite  Action = "write"
	ActionCreate Action = "create"
	ActionDelete Action = "delete"
	ActionSubmit Action = "submit"
	ActionCancel Action = "cancel"
	ActionAmend  Action = "amend"
	ActionPrint  Action = "print"
	ActionExport Action = "export"
)

var (
	ErrUnauthenticated = errors.New("permission: not authenticated")
	ErrForbidden       = errors.New("permission: forbidden")
)

type Engine struct {
	db    *dbx.DB
	cache *cache // small in-process cache; invalidated on permission edits
}

func NewEngine(db *dbx.DB) *Engine {
	return &Engine{db: db, cache: newCache(30 * time.Second)}
}

// Check verifies the principal can perform action on doctype, scoped to the active company.
func (e *Engine) Check(ctx context.Context, doctype string, action Action) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return ErrUnauthenticated
	}
	co := auth.CompanyFromContext(ctx)
	if co != "" && !contains(p.Companies, co) {
		return fmt.Errorf("%w: no access to company %s", ErrForbidden, co)
	}
	if p.IsSystem {
		return nil
	}
	rp, err := e.rolePermFor(ctx, p.Roles, doctype)
	if err != nil {
		return err
	}
	if !rp.allows(action) {
		return fmt.Errorf("%w: role lacks %s on %s", ErrForbidden, action, doctype)
	}
	return nil
}

// RowFilter returns SQL fragments to AND into a list query, scoping rows the principal may see.
// The returned predicate uses positional args starting at startArg; the caller must offset accordingly.
// alias is the table alias (e.g. "j" for journal_entry j).
func (e *Engine) RowFilter(ctx context.Context, doctype, alias string, startArg int) (sql string, args []any, nextArg int, err error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return "", nil, startArg, ErrUnauthenticated
	}
	if p.IsSystem {
		return "TRUE", nil, startArg, nil
	}

	parts := []string{}
	idx := startArg

	// Company scope: limit to companies the user has access to.
	if len(p.Companies) > 0 {
		placeholders := make([]string, len(p.Companies))
		for i, c := range p.Companies {
			placeholders[i] = fmt.Sprintf("$%d", idx)
			idx++
			args = append(args, c)
		}
		parts = append(parts, fmt.Sprintf("%s.company_id IN (%s)", alias, strings.Join(placeholders, ",")))
	} else {
		parts = append(parts, "FALSE")
	}

	// Row-level scopes from user_permission, applicable to this doctype.
	scopes, err := e.userPermissions(ctx, p.UserID, doctype)
	if err != nil {
		return "", nil, idx, err
	}
	for field, vals := range scopes {
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = fmt.Sprintf("$%d", idx)
			idx++
			args = append(args, v)
		}
		parts = append(parts, fmt.Sprintf("%s.%s IN (%s)", alias, sqlIdent(field), strings.Join(ph, ",")))
	}

	if len(parts) == 0 {
		parts = append(parts, "TRUE")
	}
	return strings.Join(parts, " AND "), args, idx, nil
}

// ReadableFields returns the subset of fields the principal can read on doctype.
func (e *Engine) ReadableFields(ctx context.Context, doctype string, fields []string) []string {
	return e.filterFields(ctx, doctype, fields, "read")
}

// WritableFields returns the subset of fields the principal can write on doctype.
func (e *Engine) WritableFields(ctx context.Context, doctype string, fields []string) []string {
	return e.filterFields(ctx, doctype, fields, "write")
}

func (e *Engine) filterFields(ctx context.Context, doctype string, fields []string, kind string) []string {
	p := auth.FromContext(ctx)
	if p == nil || p.IsSystem {
		return fields
	}
	denied, err := e.fieldDenied(ctx, p.Roles, doctype, kind)
	if err != nil || len(denied) == 0 {
		return fields
	}
	out := fields[:0:0]
	for _, f := range fields {
		if _, no := denied[f]; !no {
			out = append(out, f)
		}
	}
	return out
}

// Invalidate drops cached lookups for a role or user.
func (e *Engine) Invalidate() { e.cache.clear() }

// --- internals ---

type rolePerm struct {
	Read, Write, Create, Delete, Submit, Cancel, Amend, Print, Export bool
}

func (r rolePerm) allows(a Action) bool {
	switch a {
	case ActionRead:
		return r.Read
	case ActionWrite:
		return r.Write
	case ActionCreate:
		return r.Create
	case ActionDelete:
		return r.Delete
	case ActionSubmit:
		return r.Submit
	case ActionCancel:
		return r.Cancel
	case ActionAmend:
		return r.Amend
	case ActionPrint:
		return r.Print
	case ActionExport:
		return r.Export
	}
	return false
}

func (e *Engine) rolePermFor(ctx context.Context, roles []string, doctype string) (rolePerm, error) {
	key := "rp:" + doctype + ":" + strings.Join(roles, ",")
	if v, ok := e.cache.get(key); ok {
		return v.(rolePerm), nil
	}
	if len(roles) == 0 {
		return rolePerm{}, nil
	}
	rows, err := e.db.Query(ctx, `
		SELECT can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export
		FROM role_permission
		WHERE doctype = $1 AND role_id = ANY($2)`, doctype, roles)
	if err != nil {
		return rolePerm{}, err
	}
	defer rows.Close()
	var rp rolePerm
	for rows.Next() {
		var r rolePerm
		if err := rows.Scan(&r.Read, &r.Write, &r.Create, &r.Delete, &r.Submit, &r.Cancel, &r.Amend, &r.Print, &r.Export); err != nil {
			return rolePerm{}, err
		}
		rp.Read = rp.Read || r.Read
		rp.Write = rp.Write || r.Write
		rp.Create = rp.Create || r.Create
		rp.Delete = rp.Delete || r.Delete
		rp.Submit = rp.Submit || r.Submit
		rp.Cancel = rp.Cancel || r.Cancel
		rp.Amend = rp.Amend || r.Amend
		rp.Print = rp.Print || r.Print
		rp.Export = rp.Export || r.Export
	}
	if err := rows.Err(); err != nil {
		return rolePerm{}, err
	}
	e.cache.set(key, rp)
	return rp, nil
}

func (e *Engine) userPermissions(ctx context.Context, userID, doctype string) (map[string][]string, error) {
	key := "up:" + userID + ":" + doctype
	if v, ok := e.cache.get(key); ok {
		return v.(map[string][]string), nil
	}
	rows, err := e.db.Query(ctx, `
		SELECT scope, value FROM user_permission
		WHERE user_id = $1 AND (applicable_for IS NULL OR applicable_for = $2)`, userID, doctype)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var scope, value string
		if err := rows.Scan(&scope, &value); err != nil {
			return nil, err
		}
		out[scope] = append(out[scope], value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	e.cache.set(key, out)
	return out, nil
}

func (e *Engine) fieldDenied(ctx context.Context, roles []string, doctype, kind string) (map[string]struct{}, error) {
	key := "fp:" + kind + ":" + doctype + ":" + strings.Join(roles, ",")
	if v, ok := e.cache.get(key); ok {
		return v.(map[string]struct{}), nil
	}
	col := "can_read"
	if kind == "write" {
		col = "can_write"
	}
	q := fmt.Sprintf(`SELECT field FROM field_permission WHERE doctype = $1 AND role_id = ANY($2) AND %s = false`, col)
	rows, err := e.db.Query(ctx, q, doctype, roles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	e.cache.set(key, out)
	return out, nil
}

// LoadPrincipal fetches the principal's company list and roles. Called by auth middleware.
func LoadPrincipal(ctx context.Context, db *dbx.DB, userID string) (*auth.Principal, error) {
	var p auth.Principal
	p.UserID = userID
	err := db.QueryRow(ctx, `SELECT is_system, locale FROM users WHERE id = $1 AND enabled = true`, userID).Scan(&p.IsSystem, &p.Locale)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT company_id FROM user_company WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		p.Companies = append(p.Companies, c)
	}
	rows.Close()
	rows, err = db.Query(ctx, `SELECT role_id FROM user_role WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		p.Roles = append(p.Roles, r)
	}
	return &p, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// sqlIdent is a conservative identifier filter for user_permission scope field names.
// Scope names come from system-controlled metadata, but we still defend against injection.
func sqlIdent(s string) string {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return "_invalid_"
		}
	}
	return s
}
