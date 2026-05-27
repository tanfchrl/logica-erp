// Package identity exposes admin CRUD for users, roles, and the role permission
// matrix. All endpoints sit under /admin/* and require the corresponding
// permission-engine doctype (`user`, `role`, `role_permission`).
package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const (
	DoctypeUser           = "user"
	DoctypeRole           = "role"
	DoctypeRolePermission = "role_permission"
)

// KnownDoctypes is the list rendered in the role-permission matrix UI.
// Mirrors cmd/logica/seed.go's phase0Doctypes — kept here so the API can
// return it without importing the seed binary.
var KnownDoctypes = []string{
	// Platform / admin
	"company", "account", "cost_center", "role", "user", "user_permission", "field_permission", "role_permission",
	"naming_series", "smtp_config", "email_template", "audit_log", "fiscal_year",
	// Accounting
	"journal_entry", "tax_category", "tax_template", "withholding_tax_type",
	"sales_invoice", "purchase_invoice", "payment_entry", "period_closing_voucher", "report",
	// Stock
	"warehouse", "stock_entry", "item",
	// Customers / suppliers
	"customer", "supplier",
	// Orders
	"sales_order", "purchase_order",
	// CRM / projects
	"lead", "project", "task", "timesheet",
	// Manufacturing
	"bom", "work_order",
	// Assets
	"asset",
	// HR / payroll
	"employee", "department", "designation", "attendance", "leave_application", "expense_claim",
	"salary_component", "salary_structure", "payroll_entry", "salary_slip",
	// POS / support
	"pos_profile", "pos_invoice", "issue", "service_level_agreement",
	// Workflow / crosscut
	"workflow", "notification", "comment", "attachment",
}

// ===========================================================================
// Types
// ===========================================================================

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name,omitempty"`
	Enabled     bool      `json:"enabled"`
	Locale      string    `json:"locale,omitempty"`
	TimeZone    string    `json:"time_zone,omitempty"`
	IsSystem    bool      `json:"is_system"`
	Roles       []string  `json:"roles"`
	Companies   []string  `json:"companies"`
	// IPAllowlist — CIDR blocks the user must log in from. Empty = no restriction.
	IPAllowlist []string  `json:"ip_allowlist"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UserCreateInput struct {
	Email     string   `json:"email"`
	FullName  string   `json:"full_name,omitempty"`
	Password  string   `json:"password" doc:"Initial password — share with the user out-of-band."`
	Roles     []string `json:"roles,omitempty"     doc:"role_id list to assign on create"`
	Companies []string `json:"companies,omitempty" doc:"company_id list to grant access to"`
}

type UserUpdateInput struct {
	FullName    *string  `json:"full_name,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Locale      *string  `json:"locale,omitempty"`
	TimeZone    *string  `json:"time_zone,omitempty"`
	// IPAllowlist replaces the existing value when supplied; pass [] to clear.
	IPAllowlist *[]string `json:"ip_allowlist,omitempty"`
}

type PasswordInput struct {
	NewPassword string `json:"new_password"`
}

type AssignRolesInput     struct{ Roles     []string `json:"roles"` }
type AssignCompaniesInput struct{ Companies []string `json:"companies"` }

type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	IsSystem    bool      `json:"is_system"`
	UserCount   int       `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type RoleSaveInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PermissionMatrix expresses the role_permission grid as a doctype->actions map.
type PermissionRow struct {
	Doctype   string `json:"doctype"`
	CanRead   bool   `json:"can_read"`
	CanWrite  bool   `json:"can_write"`
	CanCreate bool   `json:"can_create"`
	CanDelete bool   `json:"can_delete"`
	CanSubmit bool   `json:"can_submit"`
	CanCancel bool   `json:"can_cancel"`
	CanAmend  bool   `json:"can_amend"`
	CanPrint  bool   `json:"can_print"`
	CanExport bool   `json:"can_export"`
}

type SessionRow struct {
	ID        string    `json:"id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UserAgent string    `json:"user_agent,omitempty"`
	IP        string    `json:"ip,omitempty"`
	Revoked   bool      `json:"revoked"`
}

// ===========================================================================
// Service
// ===========================================================================

type Service struct {
	db   *dbx.DB
	perm *permission.Engine // nil-safe; nil means cache invalidation is a no-op
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// SetPermissionEngine wires the permission engine so role/user/permission
// mutations can invalidate its in-process cache immediately, instead of
// waiting for the 30s TTL to drain. Optional but recommended.
func (s *Service) SetPermissionEngine(e *permission.Engine) { s.perm = e }

func (s *Service) invalidatePerms() {
	if s.perm != nil {
		s.perm.Invalidate()
	}
}

// ---- Users ----

// userSelect — common projection. ip_allowlist is rendered as text[] using host()+masklen()
// so the API surfaces consistent "10.0.0.0/24" strings regardless of pgx driver version.
const userSelect = `
	SELECT u.id, u.email, u.full_name, u.enabled, u.locale, u.time_zone, u.is_system,
	       coalesce(array_agg(DISTINCT ur.role_id)   FILTER (WHERE ur.role_id   IS NOT NULL), '{}'),
	       coalesce(array_agg(DISTINCT uc.company_id) FILTER (WHERE uc.company_id IS NOT NULL), '{}'),
	       coalesce(array(SELECT host(c) || '/' || masklen(c) FROM unnest(u.ip_allowlist) c), '{}'),
	       u.created_at, u.updated_at
	FROM users u
	LEFT JOIN user_role    ur ON ur.user_id = u.id
	LEFT JOIN user_company uc ON uc.user_id = u.id`

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, userSelect+` GROUP BY u.id ORDER BY u.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.Enabled, &u.Locale, &u.TimeZone, &u.IsSystem,
			&u.Roles, &u.Companies, &u.IPAllowlist, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, userSelect+` WHERE u.id = $1 GROUP BY u.id`, id).
		Scan(&u.ID, &u.Email, &u.FullName, &u.Enabled, &u.Locale, &u.TimeZone, &u.IsSystem,
			&u.Roles, &u.Companies, &u.IPAllowlist, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user %s: not found", id)
	}
	return &u, err
}

func (s *Service) CreateUser(ctx context.Context, in UserCreateInput) (*User, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("identity: unauthenticated")
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email == "" || in.Password == "" {
		return nil, errors.New("user: email and password are required")
	}
	if len(in.Password) < 8 {
		return nil, errors.New("user.password: at least 8 characters")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	id := dbx.NewIDWithPrefix("usr")
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, full_name, password_hash, enabled, created_by, updated_by)
			VALUES ($1,$2,$3,$4,true,$5,$5)`,
			id, in.Email, in.FullName, hash, p.UserID); err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("user: email already exists")
			}
			return err
		}
		if err := setUserRoles(ctx, tx, id, in.Roles); err != nil {
			return err
		}
		if err := setUserCompanies(ctx, tx, id, in.Companies); err != nil {
			return err
		}
		return audit.Record(ctx, tx, DoctypeUser, id, p.UserID, audit.ActionCreate, audit.Diff{After: map[string]any{
			"email": in.Email, "full_name": in.FullName, "roles": in.Roles, "companies": in.Companies,
		}})
	})
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

func (s *Service) UpdateUser(ctx context.Context, id string, in UserUpdateInput) (*User, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("identity: unauthenticated")
	}
	cur, err := s.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.IsSystem && in.Enabled != nil && !*in.Enabled {
		return nil, errors.New("user: cannot disable a system user")
	}
	fullName := cur.FullName
	enabled := cur.Enabled
	locale := cur.Locale
	tz := cur.TimeZone
	if in.FullName != nil { fullName = *in.FullName }
	if in.Enabled != nil  { enabled = *in.Enabled }
	if in.Locale != nil   { locale = *in.Locale }
	if in.TimeZone != nil { tz = *in.TimeZone }

	// Validate and parse incoming allowlist (if supplied) — reject early on bad CIDRs.
	var allowlist []string
	updateAllowlist := in.IPAllowlist != nil
	if updateAllowlist {
		for _, c := range *in.IPAllowlist {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(c); err != nil {
				return nil, fmt.Errorf("ip_allowlist: %q is not a valid CIDR (use e.g. 203.0.113.0/24 or 198.51.100.42/32)", c)
			}
			allowlist = append(allowlist, c)
		}
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET full_name = $2, enabled = $3, locale = $4, time_zone = $5, updated_by = $6
			WHERE id = $1`, id, fullName, enabled, locale, tz, p.UserID); err != nil {
			return err
		}
		if updateAllowlist {
			if _, err := tx.Exec(ctx,
				`UPDATE users SET ip_allowlist = $2::cidr[] WHERE id = $1`,
				id, allowlist); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, DoctypeUser, id, p.UserID, audit.ActionUpdate,
			audit.Diff{Before: cur, After: map[string]any{
				"full_name": fullName, "enabled": enabled, "locale": locale, "time_zone": tz,
				"ip_allowlist": allowlist,
			}})
	})
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

func (s *Service) SetPassword(ctx context.Context, id, newPassword string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("identity: unauthenticated")
	}
	if len(newPassword) < 8 {
		return errors.New("password: at least 8 characters")
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, updated_by = $3 WHERE id = $1`, id, hash, p.UserID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("user %s: not found", id)
		}
		// Revoke all sessions so they re-auth with the new password.
		if _, err := tx.Exec(ctx, `UPDATE user_session SET revoked_at = now()
			WHERE user_id = $1 AND revoked_at IS NULL`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, DoctypeUser, id, p.UserID, audit.ActionUpdate,
			audit.Diff{After: map[string]any{"password": "(changed)"}})
	})
}

func (s *Service) SetUserRoles(ctx context.Context, id string, roles []string) (*User, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("identity: unauthenticated")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if err := setUserRoles(ctx, tx, id, roles); err != nil {
			return err
		}
		return audit.Record(ctx, tx, DoctypeUser, id, p.UserID, audit.ActionUpdate,
			audit.Diff{After: map[string]any{"roles": roles}})
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePerms()
	return s.GetUser(ctx, id)
}

func (s *Service) SetUserCompanies(ctx context.Context, id string, companies []string) (*User, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("identity: unauthenticated")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if err := setUserCompanies(ctx, tx, id, companies); err != nil {
			return err
		}
		return audit.Record(ctx, tx, DoctypeUser, id, p.UserID, audit.ActionUpdate,
			audit.Diff{After: map[string]any{"companies": companies}})
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePerms()
	return s.GetUser(ctx, id)
}

func (s *Service) ListSessions(ctx context.Context, userID string) ([]SessionRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, issued_at, expires_at, coalesce(user_agent,''), coalesce(host(ip),''), (revoked_at IS NOT NULL)
		FROM user_session WHERE user_id = $1 ORDER BY issued_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionRow, 0)
	for rows.Next() {
		var sr SessionRow
		if err := rows.Scan(&sr.ID, &sr.IssuedAt, &sr.ExpiresAt, &sr.UserAgent, &sr.IP, &sr.Revoked); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	ct, err := s.db.Exec(ctx, `UPDATE user_session SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sessionID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("session: not found or already revoked")
	}
	return nil
}

func setUserRoles(ctx context.Context, tx pgx.Tx, userID string, roles []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_role WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, r := range roles {
		if _, err := tx.Exec(ctx, `INSERT INTO user_role (user_id, role_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			userID, r); err != nil {
			return err
		}
	}
	return nil
}

func setUserCompanies(ctx context.Context, tx pgx.Tx, userID string, companies []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_company WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, c := range companies {
		if _, err := tx.Exec(ctx, `INSERT INTO user_company (user_id, company_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			userID, c); err != nil {
			return err
		}
	}
	return nil
}

// ---- Roles ----

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.name, r.description, r.is_system, r.created_at,
		       (SELECT count(*) FROM user_role WHERE role_id = r.id)
		FROM role r ORDER BY r.is_system DESC, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Role, 0)
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.IsSystem, &r.CreatedAt, &r.UserCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) CreateRole(ctx context.Context, in RoleSaveInput) (*Role, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("role.name: required")
	}
	id := dbx.NewIDWithPrefix("rol")
	_, err := s.db.Exec(ctx, `INSERT INTO role (id, name, description, is_system) VALUES ($1,$2,$3,false)`,
		id, in.Name, in.Description)
	if err != nil {
		if dbx.IsUniqueViolation(err) {
			return nil, errors.New("role: name already used")
		}
		return nil, err
	}
	return s.getRole(ctx, id)
}

func (s *Service) UpdateRole(ctx context.Context, id string, in RoleSaveInput) (*Role, error) {
	if in.Name == "" {
		return nil, errors.New("role.name: required")
	}
	ct, err := s.db.Exec(ctx, `UPDATE role SET name = $2, description = $3 WHERE id = $1 AND is_system = false`,
		id, in.Name, in.Description)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, errors.New("role: not found, or is a system role (not editable)")
	}
	return s.getRole(ctx, id)
}

func (s *Service) DeleteRole(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM role WHERE id = $1 AND is_system = false`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("role: not found, or is a system role (not deletable)")
	}
	return nil
}

func (s *Service) getRole(ctx context.Context, id string) (*Role, error) {
	var r Role
	err := s.db.QueryRow(ctx, `
		SELECT id, name, description, is_system, created_at,
		       (SELECT count(*) FROM user_role WHERE role_id = $1)
		FROM role WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.IsSystem, &r.CreatedAt, &r.UserCount)
	return &r, err
}

func (s *Service) ListRolePermissions(ctx context.Context, roleID string) ([]PermissionRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT doctype, can_read, can_write, can_create, can_delete,
		       can_submit, can_cancel, can_amend, can_print, can_export
		FROM role_permission WHERE role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PermissionRow, 0)
	for rows.Next() {
		var pr PermissionRow
		if err := rows.Scan(&pr.Doctype, &pr.CanRead, &pr.CanWrite, &pr.CanCreate, &pr.CanDelete,
			&pr.CanSubmit, &pr.CanCancel, &pr.CanAmend, &pr.CanPrint, &pr.CanExport); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// SetRolePermissions replaces the permission grid for a role atomically.
func (s *Service) SetRolePermissions(ctx context.Context, roleID string, perms []PermissionRow) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("identity: unauthenticated")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM role_permission WHERE role_id = $1`, roleID); err != nil {
			return err
		}
		for _, pr := range perms {
			if !pr.CanRead && !pr.CanWrite && !pr.CanCreate && !pr.CanDelete &&
				!pr.CanSubmit && !pr.CanCancel && !pr.CanAmend && !pr.CanPrint && !pr.CanExport {
				continue // skip all-false rows to keep the table tidy
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permission (id, role_id, doctype,
					can_read, can_write, can_create, can_delete,
					can_submit, can_cancel, can_amend, can_print, can_export)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				dbx.NewIDWithPrefix("rp"), roleID, pr.Doctype,
				pr.CanRead, pr.CanWrite, pr.CanCreate, pr.CanDelete,
				pr.CanSubmit, pr.CanCancel, pr.CanAmend, pr.CanPrint, pr.CanExport); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, DoctypeRolePermission, roleID, p.UserID, audit.ActionUpdate,
			audit.Diff{After: map[string]any{"rows": len(perms)}})
	})
	if err == nil {
		// Drop cached rolePerm rows so newly-granted access takes effect
		// immediately instead of after the 30s TTL.
		s.invalidatePerms()
	}
	return err
}

// ===========================================================================
// HTTP
// ===========================================================================

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	// ---- Users ----
	huma.Register(api, huma.Operation{
		OperationID: "list-users", Method: http.MethodGet,
		Path: "/admin/users", Summary: "List users",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, _ *struct{}) (*userListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		us, err := h.Service.ListUsers(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userListOut{Body: userListBody{Items: us}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-user", Method: http.MethodPost,
		Path: "/admin/users", Summary: "Create a user",
		Tags: []string{"Admin / Identity"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *userCreateIn) (*userItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		u, err := h.Service.CreateUser(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userItemOut{Body: *u}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-user", Method: http.MethodGet,
		Path: "/admin/users/{id}", Summary: "Get a user",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *userByID) (*userItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		u, err := h.Service.GetUser(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userItemOut{Body: *u}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-user", Method: http.MethodPut,
		Path: "/admin/users/{id}", Summary: "Update a user",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *userUpdateIn) (*userItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		u, err := h.Service.UpdateUser(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userItemOut{Body: *u}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-user-password", Method: http.MethodPost,
		Path: "/admin/users/{id}/password", Summary: "Set a user's password",
		Tags: []string{"Admin / Identity"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *userPasswordIn) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.SetPassword(ctx, in.ID, in.Body.NewPassword); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-user-roles", Method: http.MethodPut,
		Path: "/admin/users/{id}/roles", Summary: "Assign roles",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *userRolesIn) (*userItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		u, err := h.Service.SetUserRoles(ctx, in.ID, in.Body.Roles)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userItemOut{Body: *u}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-user-companies", Method: http.MethodPut,
		Path: "/admin/users/{id}/companies", Summary: "Grant company access",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *userCompaniesIn) (*userItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		u, err := h.Service.SetUserCompanies(ctx, in.ID, in.Body.Companies)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &userItemOut{Body: *u}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-user-sessions", Method: http.MethodGet,
		Path: "/admin/users/{id}/sessions", Summary: "List a user's sessions",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *userByID) (*sessionListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ss, err := h.Service.ListSessions(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &sessionListOut{Body: sessionListBody{Items: ss}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "revoke-user-session", Method: http.MethodDelete,
		Path: "/admin/users/{id}/sessions/{session_id}", Summary: "Revoke a session",
		Tags: []string{"Admin / Identity"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *userSessionIn) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeUser, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.RevokeSession(ctx, in.ID, in.SessionID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- Roles ----
	huma.Register(api, huma.Operation{
		OperationID: "list-roles", Method: http.MethodGet,
		Path: "/admin/roles", Summary: "List roles",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, _ *struct{}) (*roleListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeRole, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		rs, err := h.Service.ListRoles(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &roleListOut{Body: roleListBody{Items: rs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-role", Method: http.MethodPost,
		Path: "/admin/roles", Summary: "Create a role",
		Tags: []string{"Admin / Identity"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *roleSaveIn) (*roleItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeRole, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.CreateRole(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &roleItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-role", Method: http.MethodPut,
		Path: "/admin/roles/{id}", Summary: "Update a role",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *roleUpdateIn) (*roleItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeRole, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.UpdateRole(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &roleItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-role", Method: http.MethodDelete,
		Path: "/admin/roles/{id}", Summary: "Delete a role",
		Tags: []string{"Admin / Identity"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *roleByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeRole, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteRole(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- Role permissions ----
	huma.Register(api, huma.Operation{
		OperationID: "list-role-permissions", Method: http.MethodGet,
		Path: "/admin/roles/{id}/permissions", Summary: "Get the permission matrix for a role",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *roleByID) (*permListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeRolePermission, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ps, err := h.Service.ListRolePermissions(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &permListOut{Body: permListBody{Items: ps}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-role-permissions", Method: http.MethodPut,
		Path: "/admin/roles/{id}/permissions", Summary: "Replace the permission matrix for a role",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, in *permSetIn) (*struct{ Body permListBody }, error) {
		if err := h.Perm.Check(ctx, DoctypeRolePermission, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.SetRolePermissions(ctx, in.ID, in.Body.Items); err != nil {
			return nil, httpx.MapError(err)
		}
		ps, _ := h.Service.ListRolePermissions(ctx, in.ID)
		return &struct{ Body permListBody }{Body: permListBody{Items: ps}}, nil
	})

	// ---- Doctype catalog (for matrix UI) ----
	huma.Register(api, huma.Operation{
		OperationID: "list-known-doctypes", Method: http.MethodGet,
		Path: "/admin/doctypes", Summary: "List doctypes the permission matrix can address",
		Tags: []string{"Admin / Identity"},
	}, func(ctx context.Context, _ *struct{}) (*dtListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeRolePermission, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &dtListOut{Body: dtListBody{Items: KnownDoctypes}}, nil
	})
}

// ===========================================================================
// I/O DTOs
// ===========================================================================

type (
	userListOut  struct{ Body userListBody }
	userListBody struct {
		Items []User `json:"items"`
	}
	userItemOut struct{ Body User }
	userByID    struct {
		ID string `path:"id"`
	}
	userSessionIn struct {
		ID        string `path:"id"`
		SessionID string `path:"session_id"`
	}
	userCreateIn   struct{ Body UserCreateInput }
	userUpdateIn   struct {
		ID   string `path:"id"`
		Body UserUpdateInput
	}
	userPasswordIn struct {
		ID   string `path:"id"`
		Body PasswordInput
	}
	userRolesIn struct {
		ID   string `path:"id"`
		Body AssignRolesInput
	}
	userCompaniesIn struct {
		ID   string `path:"id"`
		Body AssignCompaniesInput
	}
	sessionListOut  struct{ Body sessionListBody }
	sessionListBody struct {
		Items []SessionRow `json:"items"`
	}

	roleListOut  struct{ Body roleListBody }
	roleListBody struct {
		Items []Role `json:"items"`
	}
	roleItemOut struct{ Body Role }
	roleByID    struct {
		ID string `path:"id"`
	}
	roleSaveIn   struct{ Body RoleSaveInput }
	roleUpdateIn struct {
		ID   string `path:"id"`
		Body RoleSaveInput
	}

	permListOut  struct{ Body permListBody }
	permListBody struct {
		Items []PermissionRow `json:"items"`
	}
	permSetIn struct {
		ID   string `path:"id"`
		Body permListBody
	}

	dtListOut  struct{ Body dtListBody }
	dtListBody struct {
		Items []string `json:"items"`
	}
)
