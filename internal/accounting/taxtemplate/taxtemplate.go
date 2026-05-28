// Package taxtemplate manages Tax Template, Tax Category, and Withholding Tax Type masters.
// The pure tax-calculation logic lives in internal/platform/tax; this package only handles
// CRUD + loading a Template snapshot for use by invoice services.
package taxtemplate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/tax"
)

const (
	DoctypeCategory     = "tax_category"
	DoctypeTemplate     = "tax_template"
	DoctypeWithholding  = "withholding_tax_type"
)

// --- Tax category ---

type TaxCategory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type TaxCategoryCreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// --- Tax template ---

type TaxTemplate struct {
	ID            string            `json:"id"`
	CompanyID     string            `json:"company_id"`
	Name          string            `json:"name"`
	IsSales       bool              `json:"is_sales"`
	IsDefault     bool              `json:"is_default"`
	TaxCategoryID string            `json:"tax_category_id,omitempty"`
	Lines         []TaxTemplateLine `json:"lines"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type TaxTemplateLine struct {
	ID                  string          `json:"id"`
	RowIndex            int             `json:"row_index"`
	AccountID           string          `json:"account_id"`
	Description         string          `json:"description"`
	Rate                decimal.Decimal `json:"rate"`
	ChargeType          string          `json:"charge_type"`
	IncludedInBasicRate bool            `json:"included_in_basic_rate"`
	CostCenterID        string          `json:"cost_center_id,omitempty"`
}

type TaxTemplateCreateInput struct {
	CompanyID     string                 `json:"company_id,omitempty"`
	Name          string                 `json:"name"`
	IsSales       bool                   `json:"is_sales"`
	IsDefault     bool                   `json:"is_default,omitempty"`
	TaxCategoryID string                 `json:"tax_category_id,omitempty"`
	CustomFields  map[string]any         `json:"custom_fields,omitempty"`
	Lines         []TaxTemplateLineInput `json:"lines"`
}

// TaxTemplateUpdateInput mirrors TaxTemplateCreateInput minus the immutable
// natural key (company_id + name). Lines are fully replaced on update: the
// existing rows are deleted and the supplied rows are reinserted within the
// same transaction.
type TaxTemplateUpdateInput struct {
	Name          string                 `json:"name"`
	IsSales       bool                   `json:"is_sales"`
	IsDefault     bool                   `json:"is_default,omitempty"`
	TaxCategoryID string                 `json:"tax_category_id,omitempty"`
	CustomFields  map[string]any         `json:"custom_fields,omitempty"`
	Lines         []TaxTemplateLineInput `json:"lines"`
}

type TaxTemplateLineInput struct {
	AccountID           string `json:"account_id"`
	Description         string `json:"description"`
	Rate                string `json:"rate"`
	ChargeType          string `json:"charge_type,omitempty"`
	IncludedInBasicRate bool   `json:"included_in_basic_rate,omitempty"`
	CostCenterID        string `json:"cost_center_id,omitempty"`
}

// --- Withholding tax type ---

type WithholdingTaxType struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Rate       decimal.Decimal `json:"rate"`
	AccountID  string          `json:"account_id"`
	Threshold  decimal.Decimal `json:"threshold,omitempty"`
	Category   string          `json:"category,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type WithholdingCreateInput struct {
	Name      string `json:"name"`
	Rate      string `json:"rate"`
	AccountID string `json:"account_id"`
	Threshold string `json:"threshold,omitempty"`
	Category  string `json:"category,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- Categories ----

func (s *Service) CreateCategory(ctx context.Context, in TaxCategoryCreateInput) (*TaxCategory, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("tax_category.name: required")
	}
	id := dbx.NewIDWithPrefix("txc")
	var c TaxCategory
	err := s.db.QueryRow(ctx, `
		INSERT INTO tax_category (id, name, description) VALUES ($1,$2,$3)
		RETURNING id, name, coalesce(description,''), created_at`,
		id, in.Name, nullable(in.Description)).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	if err != nil && dbx.IsUniqueViolation(err) {
		return nil, errors.New("tax_category: duplicate name")
	}
	return &c, err
}

func (s *Service) ListCategories(ctx context.Context) ([]TaxCategory, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, coalesce(description,''), created_at FROM tax_category ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaxCategory
	for rows.Next() {
		var c TaxCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- Templates ----

func (s *Service) CreateTemplate(ctx context.Context, in TaxTemplateCreateInput) (*TaxTemplate, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("tax_template: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("tax_template.company_id: required")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("tax_template.name: required")
	}
	if len(in.Lines) == 0 {
		return nil, errors.New("tax_template.lines: at least one required")
	}

	id := dbx.NewIDWithPrefix("txt")
	var t TaxTemplate
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, DoctypeTemplate, in.CustomFields)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO tax_template (id, company_id, name, is_sales, is_default, tax_category_id, custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
			RETURNING id, company_id, name, is_sales, is_default, coalesce(tax_category_id,''), created_at, updated_at`,
			id, in.CompanyID, in.Name, in.IsSales, in.IsDefault, nullable(in.TaxCategoryID), cf, p.UserID).
			Scan(&t.ID, &t.CompanyID, &t.Name, &t.IsSales, &t.IsDefault, &t.TaxCategoryID, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("tax_template: duplicate name in company")
			}
			return err
		}
		lines, err := insertTemplateLines(ctx, tx, t.ID, in.Lines)
		if err != nil {
			return err
		}
		t.Lines = lines
		return audit.Record(ctx, tx, DoctypeTemplate, t.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &t, err
}

// insertTemplateLines validates and inserts the lines for a template. Shared
// between Create and Update.
func insertTemplateLines(ctx context.Context, tx pgx.Tx, templateID string, in []TaxTemplateLineInput) ([]TaxTemplateLine, error) {
	out := make([]TaxTemplateLine, 0, len(in))
	for i, ln := range in {
		rate, err := decimal.NewFromString(ln.Rate)
		if err != nil {
			return nil, fmt.Errorf("tax_template.lines[%d].rate: %w", i, err)
		}
		if ln.AccountID == "" {
			return nil, fmt.Errorf("tax_template.lines[%d].account_id: required", i)
		}
		ct := ln.ChargeType
		if ct == "" {
			ct = string(tax.ChargeOnNetTotal)
		}
		lid := dbx.NewIDWithPrefix("txtl")
		if _, err := tx.Exec(ctx, `
			INSERT INTO tax_template_line (id, template_id, row_index, account_id, description, rate,
			                               charge_type, included_in_basic_rate, cost_center_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			lid, templateID, i+1, ln.AccountID, ln.Description, rate, ct,
			ln.IncludedInBasicRate, nullable(ln.CostCenterID)); err != nil {
			return nil, err
		}
		out = append(out, TaxTemplateLine{
			ID: lid, RowIndex: i + 1, AccountID: ln.AccountID, Description: ln.Description,
			Rate: rate, ChargeType: ct, IncludedInBasicRate: ln.IncludedInBasicRate, CostCenterID: ln.CostCenterID,
		})
	}
	return out, nil
}

func (s *Service) UpdateTemplate(ctx context.Context, id string, in TaxTemplateUpdateInput) (*TaxTemplate, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("tax_template: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("tax_template.name: required")
	}
	if len(in.Lines) == 0 {
		return nil, errors.New("tax_template.lines: at least one required")
	}
	var existing TaxTemplate
	if err := s.db.QueryRow(ctx, `SELECT id, company_id FROM tax_template WHERE id = $1 AND is_deleted = false`, id).
		Scan(&existing.ID, &existing.CompanyID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("tax_template %s not found", id)
		}
		return nil, err
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, DoctypeTemplate, in.CustomFields)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tax_template SET
			  name = $2,
			  is_sales = $3,
			  is_default = $4,
			  tax_category_id = $5,
			  custom_fields = $6,
			  updated_by = $7,
			  updated_at = now()
			WHERE id = $1 AND is_deleted = false`,
			id, in.Name, in.IsSales, in.IsDefault, nullable(in.TaxCategoryID), cf, p.UserID); err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("tax_template: duplicate name in company")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM tax_template_line WHERE template_id = $1`, id); err != nil {
			return err
		}
		if _, err := insertTemplateLines(ctx, tx, id, in.Lines); err != nil {
			return err
		}
		return audit.Record(ctx, tx, DoctypeTemplate, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.GetTemplate(ctx, id)
}

func (s *Service) ListTemplates(ctx context.Context, companyID string) ([]TaxTemplate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, name, is_sales, is_default, coalesce(tax_category_id,''), created_at, updated_at
		FROM tax_template WHERE company_id = $1 AND is_deleted = false
		ORDER BY is_sales DESC, name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaxTemplate{}
	byID := map[string]int{} // template_id → index in out
	ids := []string{}
	for rows.Next() {
		t := TaxTemplate{Lines: []TaxTemplateLine{}}
		if err := rows.Scan(&t.ID, &t.CompanyID, &t.Name, &t.IsSales, &t.IsDefault, &t.TaxCategoryID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		byID[t.ID] = len(out)
		ids = append(ids, t.ID)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	lineRows, err := s.db.Query(ctx, `
		SELECT template_id, id, row_index, account_id, description, rate, charge_type, included_in_basic_rate, coalesce(cost_center_id,'')
		FROM tax_template_line WHERE template_id = ANY($1) ORDER BY template_id, row_index`, ids)
	if err != nil {
		return nil, err
	}
	defer lineRows.Close()
	for lineRows.Next() {
		var tplID string
		var l TaxTemplateLine
		if err := lineRows.Scan(&tplID, &l.ID, &l.RowIndex, &l.AccountID, &l.Description, &l.Rate,
			&l.ChargeType, &l.IncludedInBasicRate, &l.CostCenterID); err != nil {
			return nil, err
		}
		if i, ok := byID[tplID]; ok {
			out[i].Lines = append(out[i].Lines, l)
		}
	}
	return out, lineRows.Err()
}

func (s *Service) GetTemplate(ctx context.Context, id string) (*TaxTemplate, error) {
	var t TaxTemplate
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, name, is_sales, is_default, coalesce(tax_category_id,''), created_at, updated_at
		FROM tax_template WHERE id = $1 AND is_deleted = false`, id).
		Scan(&t.ID, &t.CompanyID, &t.Name, &t.IsSales, &t.IsDefault, &t.TaxCategoryID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tax_template %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, row_index, account_id, description, rate, charge_type, included_in_basic_rate, coalesce(cost_center_id,'')
		FROM tax_template_line WHERE template_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l TaxTemplateLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.AccountID, &l.Description, &l.Rate,
			&l.ChargeType, &l.IncludedInBasicRate, &l.CostCenterID); err != nil {
			return nil, err
		}
		t.Lines = append(t.Lines, l)
	}
	return &t, rows.Err()
}

// LoadForCalc fetches a template and returns the pure-Go Template view used by tax.Calculate.
// Uses the supplied tx for read consistency inside an invoice transaction.
func LoadForCalc(ctx context.Context, tx pgx.Tx, id string) (tax.Template, error) {
	var tpl tax.Template
	if err := tx.QueryRow(ctx, `SELECT id, is_sales FROM tax_template WHERE id = $1 AND is_deleted = false`, id).
		Scan(&tpl.ID, &tpl.IsSales); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tpl, fmt.Errorf("tax_template %s not found", id)
		}
		return tpl, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, account_id, description, rate, charge_type, included_in_basic_rate, coalesce(cost_center_id,'')
		FROM tax_template_line WHERE template_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return tpl, err
	}
	defer rows.Close()
	for rows.Next() {
		var l tax.TemplateLine
		var ct string
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Description, &l.Rate, &ct, &l.IncludedInBasicRate, &l.CostCenterID); err != nil {
			return tpl, err
		}
		l.ChargeType = tax.ChargeType(ct)
		tpl.Lines = append(tpl.Lines, l)
	}
	return tpl, rows.Err()
}

// ---- Withholding ----

func (s *Service) CreateWithholding(ctx context.Context, in WithholdingCreateInput) (*WithholdingTaxType, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("withholding: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("withholding.name: required")
	}
	if in.AccountID == "" {
		return nil, errors.New("withholding.account_id: required")
	}
	rate, err := decimal.NewFromString(in.Rate)
	if err != nil {
		return nil, fmt.Errorf("withholding.rate: %w", err)
	}
	var threshold decimal.Decimal
	if in.Threshold != "" {
		threshold, err = decimal.NewFromString(in.Threshold)
		if err != nil {
			return nil, fmt.Errorf("withholding.threshold: %w", err)
		}
	}
	id := dbx.NewIDWithPrefix("wht")
	var w WithholdingTaxType
	err = s.db.QueryRow(ctx, `
		INSERT INTO withholding_tax_type (id, name, rate, account_id, threshold, category, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
		RETURNING id, name, rate, account_id, coalesce(threshold,0), coalesce(category,''), created_at, updated_at`,
		id, in.Name, rate, in.AccountID,
		func() any { if in.Threshold == "" { return nil }; return threshold }(),
		nullable(in.Category), p.UserID).
		Scan(&w.ID, &w.Name, &w.Rate, &w.AccountID, &w.Threshold, &w.Category, &w.CreatedAt, &w.UpdatedAt)
	if err != nil && dbx.IsUniqueViolation(err) {
		return nil, errors.New("withholding: duplicate name")
	}
	return &w, err
}

func (s *Service) ListWithholding(ctx context.Context) ([]WithholdingTaxType, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, rate, account_id, coalesce(threshold,0), coalesce(category,''), created_at, updated_at
		FROM withholding_tax_type WHERE is_deleted = false ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WithholdingTaxType
	for rows.Next() {
		var w WithholdingTaxType
		if err := rows.Scan(&w.ID, &w.Name, &w.Rate, &w.AccountID, &w.Threshold, &w.Category, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
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
		OperationID: "list-tax-categories", Method: http.MethodGet,
		Path: "/accounting/tax-categories", Summary: "List tax categories",
		Tags: []string{"Accounting / Tax"},
	}, func(ctx context.Context, _ *struct{}) (*txcListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeCategory, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		cs, err := h.Service.ListCategories(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txcListOut{Body: txcListBody{Items: cs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-tax-category", Method: http.MethodPost,
		Path: "/accounting/tax-categories", Summary: "Create a tax category",
		Tags: []string{"Accounting / Tax"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *txcCreateIn) (*txcCreateOut, error) {
		if err := h.Perm.Check(ctx, DoctypeCategory, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.CreateCategory(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txcCreateOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-tax-templates", Method: http.MethodGet,
		Path: "/accounting/tax-templates", Summary: "List tax templates for the active company",
		Tags: []string{"Accounting / Tax"},
	}, func(ctx context.Context, _ *struct{}) (*txtListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ts, err := h.Service.ListTemplates(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txtListOut{Body: txtListBody{Items: ts}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-tax-template", Method: http.MethodPost,
		Path: "/accounting/tax-templates", Summary: "Create a tax template",
		Tags: []string{"Accounting / Tax"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *txtCreateIn) (*txtCreateOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.CreateTemplate(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txtCreateOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-tax-template", Method: http.MethodGet,
		Path: "/accounting/tax-templates/{id}", Summary: "Get a tax template",
		Tags: []string{"Accounting / Tax"},
	}, func(ctx context.Context, in *txtGetIn) (*txtCreateOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.GetTemplate(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txtCreateOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-tax-template", Method: http.MethodPut,
		Path: "/accounting/tax-templates/{id}", Summary: "Update a tax template",
		Tags: []string{"Accounting / Tax"},
	}, func(ctx context.Context, in *txtUpdateIn) (*txtCreateOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.UpdateTemplate(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &txtCreateOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-withholding-types", Method: http.MethodGet,
		Path: "/accounting/withholding-tax-types", Summary: "List withholding tax types",
		Tags: []string{"Accounting / Tax"},
	}, func(ctx context.Context, _ *struct{}) (*whtListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWithholding, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ws, err := h.Service.ListWithholding(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whtListOut{Body: whtListBody{Items: ws}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-withholding-type", Method: http.MethodPost,
		Path: "/accounting/withholding-tax-types", Summary: "Create a withholding tax type",
		Tags: []string{"Accounting / Tax"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *whtCreateIn) (*whtCreateOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWithholding, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.CreateWithholding(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whtCreateOut{Body: *w}, nil
	})
}

type (
	txcCreateIn  struct{ Body TaxCategoryCreateInput }
	txcCreateOut struct{ Body TaxCategory }
	txcListOut   struct{ Body txcListBody }
	txcListBody  struct {
		Items []TaxCategory `json:"items"`
	}
	txtCreateIn  struct{ Body TaxTemplateCreateInput }
	txtCreateOut struct{ Body TaxTemplate }
	txtListOut   struct{ Body txtListBody }
	txtListBody  struct {
		Items []TaxTemplate `json:"items"`
	}
	txtGetIn struct {
		ID string `path:"id"`
	}
	txtUpdateIn struct {
		ID   string `path:"id"`
		Body TaxTemplateUpdateInput
	}
	whtCreateIn  struct{ Body WithholdingCreateInput }
	whtCreateOut struct{ Body WithholdingTaxType }
	whtListOut   struct{ Body whtListBody }
	whtListBody  struct {
		Items []WithholdingTaxType `json:"items"`
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
