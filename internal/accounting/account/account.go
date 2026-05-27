// Package account holds the Account master (Chart of Accounts node).
// Phase 0 ships read + flat create only; the tree management UI is Phase 1.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "account"

type Account struct {
	ID              string    `json:"id"`
	CompanyID       string    `json:"company_id"`
	Name            string    `json:"name"`
	AccountNumber   string    `json:"account_number,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	IsGroup         bool      `json:"is_group"`
	RootType        string    `json:"root_type"`
	AccountType     string    `json:"account_type,omitempty"`
	AccountCurrency string    `json:"account_currency"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AccountCreateInput struct {
	CompanyID       string `json:"company_id,omitempty"`
	Name            string `json:"name"`
	AccountNumber   string `json:"account_number,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	IsGroup         bool   `json:"is_group,omitempty"`
	RootType        string `json:"root_type"`
	AccountType     string `json:"account_type,omitempty"`
	AccountCurrency string `json:"account_currency,omitempty"`
}

type Service struct {
	db *dbx.DB
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

var validRoots = map[string]bool{
	"asset": true, "liability": true, "equity": true, "income": true, "expense": true,
}

func (s *Service) Create(ctx context.Context, in AccountCreateInput) (*Account, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("account: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.RootType = strings.ToLower(strings.TrimSpace(in.RootType))
	if in.Name == "" {
		return nil, errors.New("account.name: required")
	}
	if in.CompanyID == "" {
		return nil, errors.New("account.company_id: required")
	}
	if !validRoots[in.RootType] {
		return nil, fmt.Errorf("account.root_type: invalid %q", in.RootType)
	}
	if in.AccountCurrency == "" {
		// inherit company currency
		var cur string
		if err := s.db.QueryRow(ctx, `SELECT default_currency FROM company WHERE id = $1`, in.CompanyID).Scan(&cur); err != nil {
			return nil, fmt.Errorf("account: lookup company currency: %w", err)
		}
		in.AccountCurrency = cur
	}
	id := dbx.NewIDWithPrefix("acc")
	var a Account
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO account (id, company_id, name, account_number, parent_id, is_group, root_type, account_type, account_currency, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
			RETURNING id, company_id, name, coalesce(account_number,''), coalesce(parent_id,''),
			          is_group, root_type, coalesce(account_type,''), account_currency, created_at, updated_at`,
			id, in.CompanyID, in.Name, nullable(in.AccountNumber), nullable(in.ParentID),
			in.IsGroup, in.RootType, nullable(in.AccountType), in.AccountCurrency, p.UserID)
		if err := row.Scan(&a.ID, &a.CompanyID, &a.Name, &a.AccountNumber, &a.ParentID, &a.IsGroup,
			&a.RootType, &a.AccountType, &a.AccountCurrency, &a.CreatedAt, &a.UpdatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("account: duplicate name within company")
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, a.ID, p.UserID, audit.ActionCreate, audit.Diff{After: a})
	})
	return &a, err
}

// AccountUpdateInput mirrors AccountCreateInput minus the immutable
// account_number (the natural identifier used by GL/reports) and minus
// parent_id. Reparenting requires rebuilding the lft/rgt nested-set indexes
// for the whole subtree, and no helper for that exists yet — adding one
// piecemeal here would corrupt the tree. When the tree-management helper
// lands (Phase 1), expose ParentID here and call it before the UPDATE.
// company_id and root_type stay immutable to avoid cross-company moves and
// to keep the existing root-balance reports consistent.
type AccountUpdateInput struct {
	Name         string         `json:"name"`
	IsGroup      bool           `json:"is_group,omitempty"`
	AccountType  string         `json:"account_type,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in AccountUpdateInput) (*Account, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("account: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("account.name: required")
	}
	var before Account
	if err := s.db.QueryRow(ctx, `SELECT id, company_id, account_number FROM account WHERE id = $1 AND is_deleted = false`, id).
		Scan(&before.ID, &before.CompanyID, &before.AccountNumber); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account %s not found", id)
		}
		return nil, err
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE account SET
			  name          = $2,
			  is_group      = $3,
			  account_type  = $4,
			  custom_fields = $5,
			  updated_by    = $6,
			  updated_at    = now()
			WHERE id = $1 AND is_deleted = false`,
			id, in.Name, in.IsGroup, nullable(in.AccountType), cf, p.UserID)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("account: duplicate name within company")
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*Account, error) {
	var a Account
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, name, coalesce(account_number,''), coalesce(parent_id,''),
		       is_group, root_type, coalesce(account_type,''), account_currency, created_at, updated_at
		FROM account WHERE id = $1 AND is_deleted = false`, id).
		Scan(&a.ID, &a.CompanyID, &a.Name, &a.AccountNumber, &a.ParentID, &a.IsGroup,
			&a.RootType, &a.AccountType, &a.AccountCurrency, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("account %s not found", id)
	}
	return &a, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Account, error) {
	if companyID == "" {
		return nil, errors.New("account: company_id required")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, name, coalesce(account_number,''), coalesce(parent_id,''),
		       is_group, root_type, coalesce(account_type,''), account_currency, created_at, updated_at
		FROM account
		WHERE company_id = $1 AND is_deleted = false
		ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.CompanyID, &a.Name, &a.AccountNumber, &a.ParentID, &a.IsGroup,
			&a.RootType, &a.AccountType, &a.AccountCurrency, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-accounts",
		Method:      http.MethodGet,
		Path:        "/accounting/accounts",
		Summary:     "List accounts in the active company",
		Tags:        []string{"Accounting / Account"},
	}, func(ctx context.Context, _ *struct{}) (*accountListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id header is required")
		}
		as, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &accountListOut{Body: AccountList{Items: as}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-account",
		Method:        http.MethodPost,
		Path:          "/accounting/accounts",
		Summary:       "Create an account",
		Tags:          []string{"Accounting / Account"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *accountCreateIn) (*accountCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		if in.Body.CompanyID == "" {
			in.Body.CompanyID = auth.CompanyFromContext(ctx)
		}
		a, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &accountCreateOut{Body: *a}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-account",
		Method:      http.MethodPut,
		Path:        "/accounting/accounts/{id}",
		Summary:     "Update an account",
		Tags:        []string{"Accounting / Account"},
	}, func(ctx context.Context, in *accountUpdateIn) (*accountCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &accountCreateOut{Body: *a}, nil
	})
}

type (
	accountCreateIn  struct{ Body AccountCreateInput }
	accountCreateOut struct{ Body Account }
	accountListOut   struct{ Body AccountList }
	AccountList  struct {
		Items []Account `json:"items"`
	}
	accountUpdateIn struct {
		ID   string `path:"id"`
		Body AccountUpdateInput
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
