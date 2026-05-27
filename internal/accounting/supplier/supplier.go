// Package supplier implements the Supplier master and per-company defaults.
// Mirror of customer; kept as a separate package so future module boundaries
// (Buying vs Selling) line up cleanly.
package supplier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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

const Doctype = "supplier"

var npwpRegex = regexp.MustCompile(`^[0-9]{16}$`)

type Supplier struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	DisplayName     string             `json:"display_name"`
	SupplierGroupID string             `json:"supplier_group_id,omitempty"`
	DefaultCurrency string             `json:"default_currency,omitempty"`
	NPWP            string             `json:"npwp,omitempty"`
	IsIndividual    bool               `json:"is_individual"`
	Email           string             `json:"email,omitempty"`
	Phone           string             `json:"phone,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Defaults        []SupplierDefault  `json:"defaults,omitempty"`
}

type SupplierDefault struct {
	CompanyID                string `json:"company_id"`
	DefaultPayableAccountID  string `json:"default_payable_account_id,omitempty"`
	DefaultCurrency          string `json:"default_currency,omitempty"`
	DefaultTaxTemplateID     string `json:"default_tax_template_id,omitempty"`
}

type SupplierCreateInput struct {
	Name            string                 `json:"name"`
	DisplayName     string                 `json:"display_name"`
	SupplierGroupID string                 `json:"supplier_group_id,omitempty"`
	DefaultCurrency string                 `json:"default_currency,omitempty"`
	NPWP            string                 `json:"npwp,omitempty"`
	IsIndividual    bool                   `json:"is_individual,omitempty"`
	Email           string                 `json:"email,omitempty"`
	Phone           string                 `json:"phone,omitempty"`
	CustomFields    map[string]any         `json:"custom_fields,omitempty"`
	Defaults        []SupplierDefaultInput `json:"defaults,omitempty"`
}

type SupplierDefaultInput struct {
	CompanyID                string `json:"company_id"`
	DefaultPayableAccountID  string `json:"default_payable_account_id,omitempty"`
	DefaultCurrency          string `json:"default_currency,omitempty"`
	DefaultTaxTemplateID     string `json:"default_tax_template_id,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in SupplierCreateInput) (*Supplier, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("supplier: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.NPWP = strings.TrimSpace(in.NPWP)
	if in.Name == "" {
		return nil, errors.New("supplier.name: required")
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Name
	}
	if in.NPWP != "" && !npwpRegex.MatchString(in.NPWP) {
		return nil, errors.New("supplier.npwp: must be 16 digits")
	}
	id := dbx.NewIDWithPrefix("supp")
	var sup Supplier
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO supplier (id, name, display_name, supplier_group_id, default_currency,
			                     npwp, is_individual, email, phone, custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
			RETURNING id, name, display_name, coalesce(supplier_group_id,''),
			          coalesce(default_currency,''), coalesce(npwp,''), is_individual,
			          coalesce(email,''), coalesce(phone,''), created_at, updated_at`,
			id, in.Name, in.DisplayName, nullable(in.SupplierGroupID), nullable(in.DefaultCurrency),
			nullable(in.NPWP), in.IsIndividual, nullable(in.Email), nullable(in.Phone),
			cf, p.UserID).Scan(
			&sup.ID, &sup.Name, &sup.DisplayName, &sup.SupplierGroupID,
			&sup.DefaultCurrency, &sup.NPWP, &sup.IsIndividual, &sup.Email, &sup.Phone, &sup.CreatedAt, &sup.UpdatedAt)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("supplier: duplicate name")
			}
			return err
		}
		for _, d := range in.Defaults {
			if d.CompanyID == "" {
				return errors.New("supplier_default.company_id: required")
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO supplier_default (supplier_id, company_id, default_payable_account_id, default_currency, default_tax_template_id)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (supplier_id, company_id) DO UPDATE SET
				  default_payable_account_id = EXCLUDED.default_payable_account_id,
				  default_currency           = EXCLUDED.default_currency,
				  default_tax_template_id    = EXCLUDED.default_tax_template_id`,
				sup.ID, d.CompanyID, nullable(d.DefaultPayableAccountID), nullable(d.DefaultCurrency), nullable(d.DefaultTaxTemplateID))
			if err != nil {
				return err
			}
			sup.Defaults = append(sup.Defaults, SupplierDefault{
				CompanyID:               d.CompanyID,
				DefaultPayableAccountID: d.DefaultPayableAccountID,
				DefaultCurrency:         d.DefaultCurrency,
				DefaultTaxTemplateID:    d.DefaultTaxTemplateID,
			})
		}
		return audit.Record(ctx, tx, Doctype, sup.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &sup, err
}

func (s *Service) Get(ctx context.Context, id string) (*Supplier, error) {
	var sup Supplier
	err := s.db.QueryRow(ctx, `
		SELECT id, name, display_name, coalesce(supplier_group_id,''),
		       coalesce(default_currency,''), coalesce(npwp,''), is_individual,
		       coalesce(email,''), coalesce(phone,''), created_at, updated_at
		FROM supplier WHERE id = $1 AND is_deleted = false`, id).
		Scan(&sup.ID, &sup.Name, &sup.DisplayName, &sup.SupplierGroupID,
			&sup.DefaultCurrency, &sup.NPWP, &sup.IsIndividual, &sup.Email, &sup.Phone, &sup.CreatedAt, &sup.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("supplier %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT company_id, coalesce(default_payable_account_id,''),
		       coalesce(default_currency,''), coalesce(default_tax_template_id,'')
		FROM supplier_default WHERE supplier_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d SupplierDefault
		if err := rows.Scan(&d.CompanyID, &d.DefaultPayableAccountID, &d.DefaultCurrency, &d.DefaultTaxTemplateID); err != nil {
			return nil, err
		}
		sup.Defaults = append(sup.Defaults, d)
	}
	return &sup, rows.Err()
}

func (s *Service) List(ctx context.Context) ([]Supplier, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, display_name, coalesce(supplier_group_id,''),
		       coalesce(default_currency,''), coalesce(npwp,''), is_individual,
		       coalesce(email,''), coalesce(phone,''), created_at, updated_at
		FROM supplier WHERE is_deleted = false ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Supplier
	for rows.Next() {
		var sup Supplier
		if err := rows.Scan(&sup.ID, &sup.Name, &sup.DisplayName, &sup.SupplierGroupID,
			&sup.DefaultCurrency, &sup.NPWP, &sup.IsIndividual, &sup.Email, &sup.Phone, &sup.CreatedAt, &sup.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sup)
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
		OperationID: "list-suppliers",
		Method:      http.MethodGet,
		Path:        "/accounting/suppliers",
		Summary:     "List suppliers",
		Tags:        []string{"Accounting / Supplier"},
	}, func(ctx context.Context, _ *struct{}) (*supplierListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ss, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &supplierListOut{Body: supplierListBody{Items: ss}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-supplier",
		Method:        http.MethodPost,
		Path:          "/accounting/suppliers",
		Summary:       "Create a supplier",
		Tags:          []string{"Accounting / Supplier"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *supplierCreateIn) (*supplierCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		sup, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &supplierCreateOut{Body: *sup}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-supplier",
		Method:      http.MethodGet,
		Path:        "/accounting/suppliers/{id}",
		Summary:     "Get a supplier",
		Tags:        []string{"Accounting / Supplier"},
	}, func(ctx context.Context, in *supplierGetIn) (*supplierCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		sup, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &supplierCreateOut{Body: *sup}, nil
	})
}

type (
	supplierCreateIn  struct{ Body SupplierCreateInput }
	supplierCreateOut struct{ Body Supplier }
	supplierListOut   struct{ Body supplierListBody }
	supplierListBody  struct {
		Items []Supplier `json:"items"`
	}
	supplierGetIn struct {
		ID string `path:"id"`
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
