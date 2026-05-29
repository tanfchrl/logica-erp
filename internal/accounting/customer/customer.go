// Package customer implements the Customer master and per-company defaults.
package customer

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

const Doctype = "customer"

var npwpRegex = regexp.MustCompile(`^[0-9]{16}$`)

type Customer struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	DisplayName     string             `json:"display_name"`
	CustomerGroupID string             `json:"customer_group_id,omitempty"`
	TerritoryID     string             `json:"territory_id,omitempty"`
	DefaultCurrency string             `json:"default_currency,omitempty"`
	NPWP            string             `json:"npwp,omitempty"`
	IsIndividual    bool               `json:"is_individual"`
	Email           string             `json:"email,omitempty"`
	Phone           string             `json:"phone,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Defaults        []CustomerDefault  `json:"defaults,omitempty"`
}

type CustomerDefault struct {
	CompanyID                   string `json:"company_id"`
	DefaultReceivableAccountID  string `json:"default_receivable_account_id,omitempty"`
	DefaultCurrency             string `json:"default_currency,omitempty"`
	DefaultTaxTemplateID        string `json:"default_tax_template_id,omitempty"`
}

type CustomerCreateInput struct {
	Name            string                  `json:"name"`
	DisplayName     string                  `json:"display_name"`
	CustomerGroupID string                  `json:"customer_group_id,omitempty"`
	DefaultCurrency string                  `json:"default_currency,omitempty"`
	NPWP            string                  `json:"npwp,omitempty"`
	IsIndividual    bool                    `json:"is_individual,omitempty"`
	Email           string                  `json:"email,omitempty"`
	Phone           string                  `json:"phone,omitempty"`
	CustomFields    map[string]any          `json:"custom_fields,omitempty"`
	Defaults        []CustomerDefaultInput  `json:"defaults,omitempty"`
}

type CustomerDefaultInput struct {
	CompanyID                  string `json:"company_id"`
	DefaultReceivableAccountID string `json:"default_receivable_account_id,omitempty"`
	DefaultCurrency            string `json:"default_currency,omitempty"`
	DefaultTaxTemplateID       string `json:"default_tax_template_id,omitempty"`
}

type Service struct {
	db *dbx.DB
	// Indexer is optional. When set, Create() upserts a global-search row.
	Indexer searchIndexer
}

type searchIndexer interface {
	IndexDocument(ctx context.Context, tx pgx.Tx, doctype, documentID, name, title, body, companyID string) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in CustomerCreateInput) (*Customer, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("customer: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.NPWP = strings.TrimSpace(in.NPWP)
	if in.Name == "" {
		return nil, errors.New("customer.name: required")
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Name
	}
	if in.NPWP != "" && !npwpRegex.MatchString(in.NPWP) {
		return nil, errors.New("customer.npwp: must be 16 digits")
	}

	id := dbx.NewIDWithPrefix("cust")
	var c Customer
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO customer (id, name, display_name, customer_group_id, default_currency,
			                     npwp, is_individual, email, phone, custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
			RETURNING id, name, display_name, coalesce(customer_group_id,''),
			          coalesce(default_currency,''), coalesce(npwp,''), is_individual,
			          coalesce(email,''), coalesce(phone,''), created_at, updated_at`,
			id, in.Name, in.DisplayName, nullable(in.CustomerGroupID), nullable(in.DefaultCurrency),
			nullable(in.NPWP), in.IsIndividual, nullable(in.Email), nullable(in.Phone),
			cf, p.UserID).Scan(
			&c.ID, &c.Name, &c.DisplayName, &c.CustomerGroupID,
			&c.DefaultCurrency, &c.NPWP, &c.IsIndividual, &c.Email, &c.Phone, &c.CreatedAt, &c.UpdatedAt)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("customer: duplicate name")
			}
			return err
		}
		for _, d := range in.Defaults {
			if err := upsertDefault(ctx, tx, c.ID, d); err != nil {
				return err
			}
			c.Defaults = append(c.Defaults, CustomerDefault{
				CompanyID:                  d.CompanyID,
				DefaultReceivableAccountID: d.DefaultReceivableAccountID,
				DefaultCurrency:            d.DefaultCurrency,
				DefaultTaxTemplateID:       d.DefaultTaxTemplateID,
			})
		}
		if s.Indexer != nil {
			body := strings.TrimSpace(c.Name + " " + c.NPWP + " " + c.Email)
			if err := s.Indexer.IndexDocument(ctx, tx, Doctype, c.ID, c.Name, c.DisplayName, body, ""); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, c.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &c, err
}

func upsertDefault(ctx context.Context, tx pgx.Tx, customerID string, d CustomerDefaultInput) error {
	if d.CompanyID == "" {
		return errors.New("customer_default.company_id: required")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_default (customer_id, company_id, default_receivable_account_id, default_currency, default_tax_template_id)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (customer_id, company_id) DO UPDATE SET
		  default_receivable_account_id = EXCLUDED.default_receivable_account_id,
		  default_currency              = EXCLUDED.default_currency,
		  default_tax_template_id       = EXCLUDED.default_tax_template_id`,
		customerID, d.CompanyID, nullable(d.DefaultReceivableAccountID), nullable(d.DefaultCurrency), nullable(d.DefaultTaxTemplateID))
	return err
}

// CustomerUpdateInput mirrors CustomerCreateInput. Name is immutable (used as
// a stable lookup key); display_name, contact info, and per-company defaults
// can be edited freely.
type CustomerUpdateInput struct {
	DisplayName     string                  `json:"display_name"`
	CustomerGroupID string                  `json:"customer_group_id,omitempty"`
	DefaultCurrency string                  `json:"default_currency,omitempty"`
	NPWP            string                  `json:"npwp,omitempty"`
	IsIndividual    bool                    `json:"is_individual,omitempty"`
	Email           string                  `json:"email,omitempty"`
	Phone           string                  `json:"phone,omitempty"`
	CustomFields    map[string]any          `json:"custom_fields,omitempty"`
	Defaults        []CustomerDefaultInput  `json:"defaults,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in CustomerUpdateInput) (*Customer, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("customer: unauthenticated")
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.NPWP = strings.TrimSpace(in.NPWP)
	if in.NPWP != "" && !npwpRegex.MatchString(in.NPWP) {
		return nil, errors.New("customer.npwp: must be 16 digits")
	}
	var before Customer
	if err := s.db.QueryRow(ctx, `SELECT id, name FROM customer WHERE id = $1 AND is_deleted = false`, id).
		Scan(&before.ID, &before.Name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("customer %s not found", id)
		}
		return nil, err
	}
	if in.DisplayName == "" {
		in.DisplayName = before.Name
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE customer SET
			  display_name = $2,
			  customer_group_id = $3,
			  default_currency = $4,
			  npwp = $5,
			  is_individual = $6,
			  email = $7,
			  phone = $8,
			  custom_fields = $9,
			  updated_by = $10,
			  updated_at = now()
			WHERE id = $1 AND is_deleted = false`,
			id, in.DisplayName, nullable(in.CustomerGroupID), nullable(in.DefaultCurrency),
			nullable(in.NPWP), in.IsIndividual, nullable(in.Email), nullable(in.Phone),
			cf, p.UserID)
		if err != nil {
			return err
		}
		for _, d := range in.Defaults {
			if err := upsertDefault(ctx, tx, id, d); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*Customer, error) {
	var c Customer
	err := s.db.QueryRow(ctx, `
		SELECT id, name, display_name, coalesce(customer_group_id,''),
		       coalesce(default_currency,''), coalesce(npwp,''), is_individual,
		       coalesce(email,''), coalesce(phone,''), created_at, updated_at
		FROM customer WHERE id = $1 AND is_deleted = false`, id).
		Scan(&c.ID, &c.Name, &c.DisplayName, &c.CustomerGroupID,
			&c.DefaultCurrency, &c.NPWP, &c.IsIndividual, &c.Email, &c.Phone, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("customer %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT company_id, coalesce(default_receivable_account_id,''),
		       coalesce(default_currency,''), coalesce(default_tax_template_id,'')
		FROM customer_default WHERE customer_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d CustomerDefault
		if err := rows.Scan(&d.CompanyID, &d.DefaultReceivableAccountID, &d.DefaultCurrency, &d.DefaultTaxTemplateID); err != nil {
			return nil, err
		}
		c.Defaults = append(c.Defaults, d)
	}
	return &c, rows.Err()
}

func (s *Service) List(ctx context.Context) ([]Customer, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, display_name, coalesce(customer_group_id,''),
		       coalesce(default_currency,''), coalesce(npwp,''), is_individual,
		       coalesce(email,''), coalesce(phone,''), created_at, updated_at
		FROM customer WHERE is_deleted = false ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.Name, &c.DisplayName, &c.CustomerGroupID,
			&c.DefaultCurrency, &c.NPWP, &c.IsIndividual, &c.Email, &c.Phone, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveDefault returns the per-company defaults, falling back to NULL fields.
func (s *Service) ResolveDefault(ctx context.Context, customerID, companyID string) (CustomerDefault, error) {
	var d CustomerDefault
	d.CompanyID = companyID
	err := s.db.QueryRow(ctx, `
		SELECT coalesce(default_receivable_account_id,''),
		       coalesce(default_currency,''),
		       coalesce(default_tax_template_id,'')
		FROM customer_default WHERE customer_id = $1 AND company_id = $2`,
		customerID, companyID).Scan(&d.DefaultReceivableAccountID, &d.DefaultCurrency, &d.DefaultTaxTemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	return d, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-customers",
		Method:      http.MethodGet,
		Path:        "/accounting/customers",
		Summary:     "List customers",
		Tags:        []string{"Accounting / Customer"},
	}, func(ctx context.Context, _ *struct{}) (*customerListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		cs, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &customerListOut{Body: customerListBody{Items: cs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-customer",
		Method:        http.MethodPost,
		Path:          "/accounting/customers",
		Summary:       "Create a customer",
		Tags:          []string{"Accounting / Customer"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *customerCreateIn) (*customerCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &customerCreateOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-customer",
		Method:      http.MethodGet,
		Path:        "/accounting/customers/{id}",
		Summary:     "Get a customer",
		Tags:        []string{"Accounting / Customer"},
	}, func(ctx context.Context, in *customerGetIn) (*customerCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &customerCreateOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-customer",
		Method:      http.MethodPut,
		Path:        "/accounting/customers/{id}",
		Summary:     "Update a customer",
		Tags:        []string{"Accounting / Customer"},
	}, func(ctx context.Context, in *customerUpdateIn) (*customerCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &customerCreateOut{Body: *c}, nil
	})
}

type (
	customerCreateIn  struct{ Body CustomerCreateInput }
	customerCreateOut struct{ Body Customer }
	customerListOut   struct{ Body customerListBody }
	customerListBody  struct {
		Items []Customer `json:"items"`
	}
	customerGetIn struct {
		ID string `path:"id"`
	}
	customerUpdateIn struct {
		ID   string `path:"id"`
		Body CustomerUpdateInput
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
