// approval.go — runtime engine that evaluates approval_rule entries and
// materializes approval_request rows. Designed to be called from the
// submit() of every submittable doctype, before the actual submit work.
//
// Pattern:
//   if err := approvalEng.CheckSubmit(ctx, tx, "purchase_invoice", pi.ID, pi.Name, pi.CompanyID,
//           map[string]any{"grand_total": pi.GrandTotal}); err != nil {
//       return err   // returns ErrApprovalRequired if any rule is unmet
//   }
//
// On the first call for a doc, pending approval_request rows get inserted
// (one per matching rule). Subsequent calls re-check status and either:
//   - return ErrApprovalRequired while any are still pending (idempotent —
//     existing pending rows are kept, no duplicates created)
//   - return nil once every fired rule has an approved request
//
// Rejected requests permanently block the submit; the caller must amend
// the doc and create a new draft (or admin can delete the rejection).
package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

var (
	// ErrApprovalRequired is returned by CheckSubmit when at least one rule
	// fired and isn't yet approved. The caller should surface this to the
	// user; the message lists what's still pending.
	ErrApprovalRequired = errors.New("approval required")

	// ErrApprovalRejected means a rule fired and was rejected. Submit is
	// blocked until the rejection is administratively cleared.
	ErrApprovalRejected = errors.New("approval rejected")
)

type ApprovalEngine struct {
	db       *dbx.DB
	Notifier ApprovalNotifier // optional; fires approval.requested + approval.decided
}

// ApprovalNotifier — narrow contract the engine needs from a dispatcher.
type ApprovalNotifier interface {
	Fire(eventKey string, payload map[string]any)
}

func NewApprovalEngine(db *dbx.DB) *ApprovalEngine { return &ApprovalEngine{db: db} }

// PendingApproval is the view a caller (or the inbox UI) sees for a single
// outstanding approval against a document.
type PendingApproval struct {
	RuleID         string    `json:"rule_id"`
	RuleName       string    `json:"rule_name"`
	RequiredRoleID string    `json:"required_role_id"`
	RequiredRole   string    `json:"required_role,omitempty"`
	RequestID      string    `json:"request_id,omitempty"` // empty if no pending request was created yet
	Status         string    `json:"status"`               // pending | approved | rejected
	DecidedBy      string    `json:"decided_by,omitempty"`
	DecidedByEmail string    `json:"decided_by_email,omitempty"`
	DecidedAt      time.Time `json:"decided_at,omitempty"`
	Note           string    `json:"note,omitempty"`
	Condition      string    `json:"condition,omitempty"`
}

// CheckSubmit evaluates all active rules for the doctype + company. The `tx`
// parameter is the caller's submit transaction; it is used only for READS
// (rule evaluation + status check) so the caller's atomic rollback semantics
// stay intact. INSERTS of new pending approval_request rows happen in a
// SEPARATE transaction on the engine's own connection, so they survive the
// caller's rollback and show up in the requester's inbox — otherwise the
// user would keep retrying with nothing ever queued for an approver.
func (e *ApprovalEngine) CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("approval: unauthenticated")
	}
	rules, err := loadActiveRules(ctx, tx, doctype, companyID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}

	var (
		blocked      []string
		rejected     []string
		toCreatePend []loadedRule
	)
	for _, r := range rules {
		if !ruleMatches(r, fields) {
			continue
		}
		req, err := findRequest(ctx, tx, doctype, docID, r.ID)
		if err != nil {
			return err
		}
		switch {
		case req == nil:
			toCreatePend = append(toCreatePend, r)
			blocked = append(blocked, fmt.Sprintf("%s (role %s)", r.Name, r.RequiredRoleID))
		case req.Status == "pending":
			blocked = append(blocked, fmt.Sprintf("%s (pending)", r.Name))
		case req.Status == "rejected":
			rejected = append(rejected, fmt.Sprintf("%s (rejected)", r.Name))
		case req.Status == "approved":
			// passes
		}
	}

	if len(rejected) > 0 {
		return fmt.Errorf("%w: %s", ErrApprovalRejected, strings.Join(rejected, ", "))
	}

	// Materialize newly-needed pending requests in a separate committing tx so
	// they survive the caller's rollback (the submit failure that this very
	// function is about to trigger).
	if len(toCreatePend) > 0 {
		if err := e.db.Tx(ctx, func(itx pgx.Tx) error {
			for _, r := range toCreatePend {
				if _, err := itx.Exec(ctx, `
					INSERT INTO approval_request (id, rule_id, doctype, document_id, document_name,
					                              required_role_id, status, requested_by)
					VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)
					ON CONFLICT (doctype, document_id, rule_id) DO NOTHING`,
					dbx.NewIDWithPrefix("apreq"), r.ID, doctype, docID, docName,
					r.RequiredRoleID, p.UserID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		// Notify the roles that owe an approval.
		if e.Notifier != nil {
			for _, r := range toCreatePend {
				e.Notifier.Fire("approval.requested", map[string]any{
					"company_id":    companyID,
					"doctype":       doctype,
					"document_id":   docID,
					"document_name": docName,
					"rule_name":     r.Name,
					// Recipients are derived from rule.required_role at dispatch time
					// by the notification_rule's recipients list; the rule author
					// should set recipients=["role:<the_role_id>"] to match.
					"required_role_id": r.RequiredRoleID,
					"summary":          fmt.Sprintf("Approval needed: %s — rule %q", docName, r.Name),
				})
			}
		}
	}

	if len(blocked) > 0 {
		return fmt.Errorf("%w: %s", ErrApprovalRequired, strings.Join(blocked, ", "))
	}
	return nil
}

// Decide approves or rejects an approval_request. Caller must hold the role
// named in the rule (or be a system user).
func (e *ApprovalEngine) Decide(ctx context.Context, requestID string, approve bool, note string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("approval: unauthenticated")
	}

	err := e.db.Tx(ctx, func(tx pgx.Tx) error {
		var (
			roleID  string
			status  string
		)
		if err := tx.QueryRow(ctx, `
			SELECT required_role_id, status FROM approval_request WHERE id = $1 FOR UPDATE`,
			requestID).Scan(&roleID, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("approval_request %s: not found", requestID)
			}
			return err
		}
		if status != "pending" {
			return fmt.Errorf("approval_request: already %s", status)
		}
		if !p.IsSystem && !contains(p.Roles, roleID) {
			return errors.New("approval: caller does not hold the required role")
		}
		newStatus := "rejected"
		if approve {
			newStatus = "approved"
		}
		_, err := tx.Exec(ctx, `
			UPDATE approval_request
			SET status = $2, decided_by = $3, decided_at = now(), decision_note = $4
			WHERE id = $1`, requestID, newStatus, p.UserID, note)
		return err
	})
	if err == nil && e.Notifier != nil {
		// Pull just enough back out for a helpful payload.
		var doctype, docID, docName string
		_ = e.db.QueryRow(ctx,
			`SELECT doctype, document_id, document_name FROM approval_request WHERE id = $1`,
			requestID).Scan(&doctype, &docID, &docName)
		e.Notifier.Fire("approval.decided", map[string]any{
			"doctype":       doctype,
			"document_id":   docID,
			"document_name": docName,
			"decision":      map[bool]string{true: "approved", false: "rejected"}[approve],
			"decided_by":    p.UserID,
			"note":          note,
			"summary":       fmt.Sprintf("%s %s: %s", docName, map[bool]string{true: "approved", false: "rejected"}[approve], note),
		})
	}
	return err
}

// PendingForCaller returns approval_request rows whose required_role is held
// by the caller (or all pending rows if the caller is a system user).
func (e *ApprovalEngine) PendingForCaller(ctx context.Context) ([]InboxRow, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("approval: unauthenticated")
	}
	q := `
		SELECT a.id, a.rule_id, a.doctype, a.document_id, a.document_name,
		       a.required_role_id, coalesce(r.name,''),
		       a.requested_by, coalesce(rb.email,''),
		       a.requested_at,
		       coalesce(ru.name,''), coalesce(ru.description,'')
		FROM approval_request a
		LEFT JOIN role r          ON r.id  = a.required_role_id
		LEFT JOIN users rb        ON rb.id = a.requested_by
		LEFT JOIN approval_rule ru ON ru.id = a.rule_id
		WHERE a.status = 'pending'`
	args := []any{}
	if !p.IsSystem {
		q += " AND a.required_role_id = ANY($1)"
		args = append(args, p.Roles)
	}
	q += " ORDER BY a.requested_at DESC LIMIT 200"
	rows, err := e.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]InboxRow, 0)
	for rows.Next() {
		var r InboxRow
		if err := rows.Scan(&r.ID, &r.RuleID, &r.Doctype, &r.DocumentID, &r.DocumentName,
			&r.RequiredRoleID, &r.RequiredRole,
			&r.RequestedBy, &r.RequestedByEmail,
			&r.RequestedAt,
			&r.RuleName, &r.RuleDescription); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolvedByCaller returns the caller's recent approve / reject decisions.
func (e *ApprovalEngine) ResolvedByCaller(ctx context.Context) ([]InboxRow, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("approval: unauthenticated")
	}
	rows, err := e.db.Query(ctx, `
		SELECT a.id, a.rule_id, a.doctype, a.document_id, a.document_name,
		       a.required_role_id, coalesce(r.name,''),
		       a.requested_by, coalesce(rb.email,''),
		       a.requested_at,
		       coalesce(ru.name,''), coalesce(ru.description,''),
		       a.status, a.decided_at, a.decision_note
		FROM approval_request a
		LEFT JOIN role r          ON r.id  = a.required_role_id
		LEFT JOIN users rb        ON rb.id = a.requested_by
		LEFT JOIN approval_rule ru ON ru.id = a.rule_id
		WHERE a.decided_by = $1
		ORDER BY a.decided_at DESC LIMIT 100`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]InboxRow, 0)
	for rows.Next() {
		var r InboxRow
		if err := rows.Scan(&r.ID, &r.RuleID, &r.Doctype, &r.DocumentID, &r.DocumentName,
			&r.RequiredRoleID, &r.RequiredRole,
			&r.RequestedBy, &r.RequestedByEmail,
			&r.RequestedAt,
			&r.RuleName, &r.RuleDescription,
			&r.Status, &r.DecidedAt, &r.DecisionNote); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListByDocument returns every approval_request (any status) for a document.
// Used by the per-document approvals widget.
func (e *ApprovalEngine) ListByDocument(ctx context.Context, doctype, documentID string) ([]InboxRow, error) {
	rows, err := e.db.Query(ctx, `
		SELECT a.id, a.rule_id, a.doctype, a.document_id, a.document_name,
		       a.required_role_id, coalesce(r.name,''),
		       a.requested_by, coalesce(rb.email,''),
		       a.requested_at,
		       coalesce(ru.name,''), coalesce(ru.description,''),
		       a.status, a.decided_at, a.decision_note,
		       coalesce(d.email,'')
		FROM approval_request a
		LEFT JOIN role r          ON r.id  = a.required_role_id
		LEFT JOIN users rb        ON rb.id = a.requested_by
		LEFT JOIN users d         ON d.id  = a.decided_by
		LEFT JOIN approval_rule ru ON ru.id = a.rule_id
		WHERE a.doctype = $1 AND a.document_id = $2
		ORDER BY a.requested_at`, doctype, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]InboxRow, 0)
	for rows.Next() {
		var r InboxRow
		if err := rows.Scan(&r.ID, &r.RuleID, &r.Doctype, &r.DocumentID, &r.DocumentName,
			&r.RequiredRoleID, &r.RequiredRole,
			&r.RequestedBy, &r.RequestedByEmail,
			&r.RequestedAt,
			&r.RuleName, &r.RuleDescription,
			&r.Status, &r.DecidedAt, &r.DecisionNote,
			&r.DecidedByEmail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InboxRow is the API shape the inbox UI consumes. Aggregates rule + doc info
// so the client doesn't have to do N follow-up fetches.
type InboxRow struct {
	ID               string    `json:"id"`
	RuleID           string    `json:"rule_id"`
	RuleName         string    `json:"rule_name"`
	RuleDescription  string    `json:"rule_description,omitempty"`
	Doctype          string    `json:"doctype"`
	DocumentID       string    `json:"document_id"`
	DocumentName     string    `json:"document_name"`
	RequiredRoleID   string    `json:"required_role_id"`
	RequiredRole     string    `json:"required_role,omitempty"`
	RequestedBy      string    `json:"requested_by"`
	RequestedByEmail string    `json:"requested_by_email,omitempty"`
	RequestedAt      time.Time `json:"requested_at"`
	Status           string    `json:"status,omitempty"`
	DecidedAt        time.Time `json:"decided_at,omitempty"`
	DecisionNote     string    `json:"decision_note,omitempty"`
	DecidedByEmail   string    `json:"decided_by_email,omitempty"`
}

// ---- internals ----

type loadedRule struct {
	ID             string
	Name           string
	ConditionField string
	ConditionOp    string
	ConditionValue string
	RequiredRoleID string
	Sequence       int
}

func loadActiveRules(ctx context.Context, tx pgx.Tx, doctype, companyID string) ([]loadedRule, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,''),
		       required_role_id, sequence
		FROM approval_rule
		WHERE is_active = true AND doctype = $1
		  AND (company_id IS NULL OR company_id = $2)
		ORDER BY sequence`, doctype, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []loadedRule{}
	for rows.Next() {
		var r loadedRule
		if err := rows.Scan(&r.ID, &r.Name, &r.ConditionField, &r.ConditionOp, &r.ConditionValue,
			&r.RequiredRoleID, &r.Sequence); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type existing struct {
	Status string
}

func findRequest(ctx context.Context, tx pgx.Tx, doctype, docID, ruleID string) (*existing, error) {
	var e existing
	err := tx.QueryRow(ctx, `
		SELECT status FROM approval_request WHERE doctype = $1 AND document_id = $2 AND rule_id = $3`,
		doctype, docID, ruleID).Scan(&e.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ruleMatches returns true if the rule's condition is satisfied by the given
// document fields. No condition = always matches.
func ruleMatches(r loadedRule, fields map[string]any) bool {
	if r.ConditionField == "" {
		return true
	}
	raw, ok := fields[r.ConditionField]
	if !ok {
		return false
	}
	// Try numeric compare first; fall back to string equality.
	got, gotOK := toFloat(raw)
	want, wantOK := toFloat(r.ConditionValue)
	if gotOK && wantOK {
		switch r.ConditionOp {
		case ">":  return got >  want
		case ">=": return got >= want
		case "<":  return got <  want
		case "<=": return got <= want
		case "=":  return got == want
		case "<>": return got != want
		}
	}
	gotStr := toString(raw)
	switch r.ConditionOp {
	case "=":  return gotStr == r.ConditionValue
	case "<>": return gotStr != r.ConditionValue
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	// decimal.Decimal exposes Float64; do this via interface so we don't import
	// the type and create a dependency.
	if d, ok := v.(interface{ Float64() (float64, bool) }); ok {
		f, _ := d.Float64()
		return f, true
	}
	if s, ok := v.(interface{ String() string }); ok {
		f, err := strconv.ParseFloat(s.String(), 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return fmt.Sprintf("%v", v)
}
