// admin.go exposes the management API for letterheads + print templates, and
// the document-side resolver used by per-doctype print endpoints.
package print

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const (
	DoctypeLetterhead = "letterhead"
	DoctypeTemplate   = "print_template"
)

// SupportedDoctypes lists the doctypes that have a print pipeline and can
// therefore be customized via the admin UI. Add to this list as new print
// endpoints come online.
var SupportedDoctypes = []DoctypeDef{
	{Key: "sales_invoice",    Label: "Sales Invoice",     HasBundled: true},
	{Key: "purchase_invoice", Label: "Purchase Invoice",  HasBundled: false},
	{Key: "payment_entry",    Label: "Payment Entry",     HasBundled: false},
	{Key: "journal_entry",    Label: "Journal Entry",     HasBundled: false},
}

type DoctypeDef struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	HasBundled bool   `json:"has_bundled"`
}

// ---- Types ----

type Letterhead struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	CompanyID     string          `json:"company_id,omitempty"`
	IsDefault     bool            `json:"is_default"`
	LogoURL       string          `json:"logo_url,omitempty"`
	HeaderHTML    string          `json:"header_html,omitempty"`
	FooterHTML    string          `json:"footer_html,omitempty"`
	PaperSize     string          `json:"paper_size"`
	MarginTop     decimal.Decimal `json:"margin_top"`
	MarginBottom  decimal.Decimal `json:"margin_bottom"`
	MarginLeft    decimal.Decimal `json:"margin_left"`
	MarginRight   decimal.Decimal `json:"margin_right"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type LetterheadInput struct {
	Name         string  `json:"name"`
	CompanyID    string  `json:"company_id,omitempty"`
	IsDefault    bool    `json:"is_default,omitempty"`
	LogoURL      string  `json:"logo_url,omitempty"`
	HeaderHTML   string  `json:"header_html,omitempty"`
	FooterHTML   string  `json:"footer_html,omitempty"`
	PaperSize    string  `json:"paper_size,omitempty"`
	MarginTop    string  `json:"margin_top,omitempty"`
	MarginBottom string  `json:"margin_bottom,omitempty"`
	MarginLeft   string  `json:"margin_left,omitempty"`
	MarginRight  string  `json:"margin_right,omitempty"`
}

type PrintTemplate struct {
	ID            string    `json:"id"`
	Doctype       string    `json:"doctype"`
	Name          string    `json:"name"`
	CompanyID     string    `json:"company_id,omitempty"`
	IsDefault     bool      `json:"is_default"`
	LetterheadID  string    `json:"letterhead_id,omitempty"`
	BodyHTML      string    `json:"body_html"`
	IsEnabled     bool      `json:"is_enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type PrintTemplateInput struct {
	Doctype      string `json:"doctype"`
	Name         string `json:"name"`
	CompanyID    string `json:"company_id,omitempty"`
	IsDefault    bool   `json:"is_default,omitempty"`
	LetterheadID string `json:"letterhead_id,omitempty"`
	BodyHTML     string `json:"body_html"`
	IsEnabled    *bool  `json:"is_enabled,omitempty"`
}

// ---- Service ----

type AdminService struct {
	db       *dbx.DB
	renderer Renderer
}

func NewAdminService(db *dbx.DB, renderer Renderer) *AdminService {
	return &AdminService{db: db, renderer: renderer}
}

// --- letterhead CRUD ---

func (s *AdminService) ListLetterheads(ctx context.Context) ([]Letterhead, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, coalesce(company_id,''), is_default, logo_url,
		       header_html, footer_html, paper_size,
		       margin_top, margin_bottom, margin_left, margin_right, updated_at
		FROM letterhead ORDER BY company_id NULLS FIRST, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Letterhead, 0)
	for rows.Next() {
		var l Letterhead
		if err := rows.Scan(&l.ID, &l.Name, &l.CompanyID, &l.IsDefault, &l.LogoURL,
			&l.HeaderHTML, &l.FooterHTML, &l.PaperSize,
			&l.MarginTop, &l.MarginBottom, &l.MarginLeft, &l.MarginRight, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *AdminService) UpsertLetterhead(ctx context.Context, id string, in LetterheadInput) (*Letterhead, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("letterhead: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("letterhead.name: required")
	}
	paper := in.PaperSize
	if paper == "" {
		paper = "A4"
	}
	mt := parseMargin(in.MarginTop, "0.5")
	mb := parseMargin(in.MarginBottom, "0.5")
	ml := parseMargin(in.MarginLeft, "0.5")
	mr := parseMargin(in.MarginRight, "0.5")

	if id == "" {
		id = dbx.NewIDWithPrefix("lh")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if in.IsDefault {
			if _, err := tx.Exec(ctx, `
				UPDATE letterhead SET is_default = false
				WHERE coalesce(company_id,'') = $1 AND id <> $2`, in.CompanyID, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO letterhead (id, name, company_id, is_default, logo_url, header_html, footer_html,
			                       paper_size, margin_top, margin_bottom, margin_left, margin_right,
			                       created_by, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,now())
			ON CONFLICT (id) DO UPDATE SET
			  name = EXCLUDED.name, is_default = EXCLUDED.is_default, logo_url = EXCLUDED.logo_url,
			  header_html = EXCLUDED.header_html, footer_html = EXCLUDED.footer_html,
			  paper_size = EXCLUDED.paper_size,
			  margin_top = EXCLUDED.margin_top, margin_bottom = EXCLUDED.margin_bottom,
			  margin_left = EXCLUDED.margin_left, margin_right = EXCLUDED.margin_right,
			  updated_by = EXCLUDED.updated_by, updated_at = now()`,
			id, in.Name, nullable(in.CompanyID), in.IsDefault, in.LogoURL, in.HeaderHTML, in.FooterHTML,
			paper, mt, mb, ml, mr, p.UserID)
		if err != nil && dbx.IsUniqueViolation(err) {
			return errors.New("letterhead: a letterhead with this (company, name) already exists")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.getLetterhead(ctx, id)
}

func (s *AdminService) DeleteLetterhead(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM letterhead WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("letterhead: not found")
	}
	return nil
}

func (s *AdminService) getLetterhead(ctx context.Context, id string) (*Letterhead, error) {
	var l Letterhead
	err := s.db.QueryRow(ctx, `
		SELECT id, name, coalesce(company_id,''), is_default, logo_url,
		       header_html, footer_html, paper_size,
		       margin_top, margin_bottom, margin_left, margin_right, updated_at
		FROM letterhead WHERE id = $1`, id).
		Scan(&l.ID, &l.Name, &l.CompanyID, &l.IsDefault, &l.LogoURL,
			&l.HeaderHTML, &l.FooterHTML, &l.PaperSize,
			&l.MarginTop, &l.MarginBottom, &l.MarginLeft, &l.MarginRight, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("letterhead %s: not found", id)
	}
	return &l, err
}

// --- template CRUD ---

func (s *AdminService) ListTemplates(ctx context.Context) ([]PrintTemplate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, doctype, name, coalesce(company_id,''), is_default,
		       coalesce(letterhead_id,''), body_html, is_enabled, updated_at
		FROM print_template ORDER BY doctype, company_id NULLS FIRST, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PrintTemplate, 0)
	for rows.Next() {
		var t PrintTemplate
		if err := rows.Scan(&t.ID, &t.Doctype, &t.Name, &t.CompanyID, &t.IsDefault,
			&t.LetterheadID, &t.BodyHTML, &t.IsEnabled, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *AdminService) UpsertTemplate(ctx context.Context, id string, in PrintTemplateInput) (*PrintTemplate, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("print_template: unauthenticated")
	}
	in.Doctype = strings.TrimSpace(in.Doctype)
	in.Name = strings.TrimSpace(in.Name)
	if in.Doctype == "" || in.Name == "" {
		return nil, errors.New("print_template: doctype and name are required")
	}
	if !isSupportedDoctype(in.Doctype) {
		return nil, fmt.Errorf("print_template: doctype %q has no print pipeline", in.Doctype)
	}
	// Validate template parses.
	if _, err := template.New("body").Parse(in.BodyHTML); err != nil {
		return nil, fmt.Errorf("print_template.body_html: %w", err)
	}
	enabled := true
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}

	if id == "" {
		id = dbx.NewIDWithPrefix("pt")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if in.IsDefault {
			if _, err := tx.Exec(ctx, `
				UPDATE print_template SET is_default = false
				WHERE doctype = $1 AND coalesce(company_id,'') = $2 AND id <> $3`,
				in.Doctype, in.CompanyID, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO print_template (id, doctype, name, company_id, is_default,
			                            letterhead_id, body_html, is_enabled,
			                            created_by, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,now())
			ON CONFLICT (id) DO UPDATE SET
			  doctype = EXCLUDED.doctype, name = EXCLUDED.name, company_id = EXCLUDED.company_id,
			  is_default = EXCLUDED.is_default, letterhead_id = EXCLUDED.letterhead_id,
			  body_html = EXCLUDED.body_html, is_enabled = EXCLUDED.is_enabled,
			  updated_by = EXCLUDED.updated_by, updated_at = now()`,
			id, in.Doctype, in.Name, nullable(in.CompanyID), in.IsDefault,
			nullable(in.LetterheadID), in.BodyHTML, enabled, p.UserID)
		if err != nil && dbx.IsUniqueViolation(err) {
			return errors.New("print_template: a template with this (doctype, company, name) already exists")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.getTemplate(ctx, id)
}

func (s *AdminService) DeleteTemplate(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM print_template WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("print_template: not found")
	}
	return nil
}

func (s *AdminService) getTemplate(ctx context.Context, id string) (*PrintTemplate, error) {
	var t PrintTemplate
	err := s.db.QueryRow(ctx, `
		SELECT id, doctype, name, coalesce(company_id,''), is_default,
		       coalesce(letterhead_id,''), body_html, is_enabled, updated_at
		FROM print_template WHERE id = $1`, id).
		Scan(&t.ID, &t.Doctype, &t.Name, &t.CompanyID, &t.IsDefault,
			&t.LetterheadID, &t.BodyHTML, &t.IsEnabled, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("print_template %s: not found", id)
	}
	return &t, err
}

// --- resolver (document side) ---

// ResolvedTemplate is the picked template + letterhead pair for rendering.
type ResolvedTemplate struct {
	Template   *PrintTemplate
	Letterhead *Letterhead
}

// Resolve picks the most-specific template for (doctype, companyID), preferring
// company-scoped > all-companies and is_default within each group. Returns
// (nil, nil) if there's no custom row — the caller should fall back to bundled.
func (s *AdminService) Resolve(ctx context.Context, doctype, companyID string) (*ResolvedTemplate, error) {
	var t PrintTemplate
	err := s.db.QueryRow(ctx, `
		SELECT id, doctype, name, coalesce(company_id,''), is_default,
		       coalesce(letterhead_id,''), body_html, is_enabled, updated_at
		FROM print_template
		WHERE doctype = $1 AND is_enabled = true
		  AND (company_id = $2 OR company_id IS NULL)
		ORDER BY (company_id IS NULL) ASC, is_default DESC
		LIMIT 1`, doctype, companyID).
		Scan(&t.ID, &t.Doctype, &t.Name, &t.CompanyID, &t.IsDefault,
			&t.LetterheadID, &t.BodyHTML, &t.IsEnabled, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	res := &ResolvedTemplate{Template: &t}
	if t.LetterheadID != "" {
		lh, err := s.getLetterhead(ctx, t.LetterheadID)
		if err == nil {
			res.Letterhead = lh
		}
	}
	if res.Letterhead == nil {
		// Try the company default letterhead.
		var l Letterhead
		err := s.db.QueryRow(ctx, `
			SELECT id, name, coalesce(company_id,''), is_default, logo_url,
			       header_html, footer_html, paper_size,
			       margin_top, margin_bottom, margin_left, margin_right, updated_at
			FROM letterhead
			WHERE is_default = true AND (company_id = $1 OR company_id IS NULL)
			ORDER BY (company_id IS NULL) ASC LIMIT 1`, companyID).
			Scan(&l.ID, &l.Name, &l.CompanyID, &l.IsDefault, &l.LogoURL,
				&l.HeaderHTML, &l.FooterHTML, &l.PaperSize,
				&l.MarginTop, &l.MarginBottom, &l.MarginLeft, &l.MarginRight, &l.UpdatedAt)
		if err == nil {
			res.Letterhead = &l
		}
	}
	return res, nil
}

// RenderToPDF executes the template body with the given context and wraps it in
// the resolved letterhead. Returns the PDF bytes ready to serve.
func (s *AdminService) RenderToPDF(ctx context.Context, r *ResolvedTemplate, vars map[string]any) ([]byte, error) {
	html, err := composeHTML(r, vars)
	if err != nil {
		return nil, err
	}
	opts := Options{}
	if r.Letterhead != nil {
		opts.PaperWidth, opts.PaperHeight = paperDims(r.Letterhead.PaperSize)
		opts.MarginTop, _ = r.Letterhead.MarginTop.Float64()
		opts.MarginBottom, _ = r.Letterhead.MarginBottom.Float64()
		opts.MarginLeft, _ = r.Letterhead.MarginLeft.Float64()
		opts.MarginRight, _ = r.Letterhead.MarginRight.Float64()
	}
	return s.renderer.RenderHTML(ctx, html, opts)
}

// composeHTML produces final HTML by wrapping the template body in the letterhead
// frame (or just a minimal wrapper if no letterhead).
func composeHTML(r *ResolvedTemplate, vars map[string]any) ([]byte, error) {
	bodyTpl, err := template.New("body").Parse(r.Template.BodyHTML)
	if err != nil {
		return nil, fmt.Errorf("body parse: %w", err)
	}
	var body bytes.Buffer
	if err := bodyTpl.Execute(&body, vars); err != nil {
		return nil, fmt.Errorf("body execute: %w", err)
	}

	header, footer := "", ""
	logo := ""
	if r.Letterhead != nil {
		header = r.Letterhead.HeaderHTML
		footer = r.Letterhead.FooterHTML
		if r.Letterhead.LogoURL != "" {
			logo = fmt.Sprintf(`<img src="%s" style="max-height:64px;max-width:240px;" alt="">`, r.Letterhead.LogoURL)
		}
		// Render header/footer as templates too so they can use {{.Company.Name}} etc.
		header = renderInline(header, vars)
		footer = renderInline(footer, vars)
	}

	wrap := fmt.Sprintf(`<!doctype html><html lang="id"><head><meta charset="utf-8">
<style>
  body { font-family: -apple-system,'Segoe UI',Inter,system-ui,sans-serif; font-size: 11pt; color: #111; margin: 0; }
  .lh-header { padding: 0 0 16px; border-bottom: 1px solid #e5e5e5; margin-bottom: 16px; display: flex; align-items: center; gap: 16px; }
  .lh-header .logo { flex-shrink: 0; }
  .lh-footer { padding: 16px 0 0; border-top: 1px solid #e5e5e5; margin-top: 32px; font-size: 9pt; color: #777; }
</style></head><body>
<div class="lh-header">%s<div class="lh-header-body">%s</div></div>
<div class="lh-body">%s</div>
<div class="lh-footer">%s</div>
</body></html>`, logo, header, body.String(), footer)
	return []byte(wrap), nil
}

func renderInline(src string, vars map[string]any) string {
	if src == "" {
		return ""
	}
	t, err := template.New("inline").Parse(src)
	if err != nil {
		return src
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return src
	}
	return buf.String()
}

func paperDims(size string) (w, h float64) {
	switch strings.ToUpper(size) {
	case "LETTER":
		return 8.5, 11.0
	case "LEGAL":
		return 8.5, 14.0
	default: // A4
		return 8.27, 11.69
	}
}

func parseMargin(s, def string) decimal.Decimal {
	if s == "" {
		s = def
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		d, _ = decimal.NewFromString(def)
	}
	return d
}

func isSupportedDoctype(dt string) bool {
	for _, d := range SupportedDoctypes {
		if d.Key == dt {
			return true
		}
	}
	return false
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type AdminHandler struct {
	Service *AdminService
	Perm    *permission.Engine
}

func RegisterAdmin(api huma.API, h *AdminHandler) {
	// Letterheads
	huma.Register(api, huma.Operation{
		OperationID: "list-letterheads", Method: http.MethodGet,
		Path: "/admin/letterheads", Summary: "List letterheads",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, _ *struct{}) (*prtLhListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeLetterhead, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ls, err := h.Service.ListLetterheads(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtLhListOut{Body: prtLhListBody{Items: ls}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-letterhead", Method: http.MethodPost,
		Path: "/admin/letterheads", Summary: "Create a letterhead",
		Tags: []string{"Admin / Print"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *prtLhCreateIn) (*prtLhItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeLetterhead, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.UpsertLetterhead(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtLhItemOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-letterhead", Method: http.MethodPut,
		Path: "/admin/letterheads/{id}", Summary: "Update a letterhead",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, in *prtLhUpdateIn) (*prtLhItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeLetterhead, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.UpsertLetterhead(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtLhItemOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-letterhead", Method: http.MethodDelete,
		Path: "/admin/letterheads/{id}", Summary: "Delete a letterhead",
		Tags: []string{"Admin / Print"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *prtLhByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeLetterhead, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteLetterhead(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// Templates
	huma.Register(api, huma.Operation{
		OperationID: "list-print-templates", Method: http.MethodGet,
		Path: "/admin/print-templates", Summary: "List print templates",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, _ *struct{}) (*prtTplListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.ListTemplates(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtTplListOut{Body: prtTplListBody{Items: ts}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-print-template", Method: http.MethodPost,
		Path: "/admin/print-templates", Summary: "Create a print template",
		Tags: []string{"Admin / Print"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *prtTplCreateIn) (*prtTplItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.UpsertTemplate(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtTplItemOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-print-template", Method: http.MethodPut,
		Path: "/admin/print-templates/{id}", Summary: "Update a print template",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, in *prtTplUpdateIn) (*prtTplItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.UpsertTemplate(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtTplItemOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-print-template", Method: http.MethodDelete,
		Path: "/admin/print-templates/{id}", Summary: "Delete a print template",
		Tags: []string{"Admin / Print"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *prtTplByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteTemplate(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// Catalog (for the UI's "what can I customize" dropdown)
	huma.Register(api, huma.Operation{
		OperationID: "list-print-doctypes", Method: http.MethodGet,
		Path: "/admin/print-templates/doctypes", Summary: "Doctypes with a print pipeline",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, _ *struct{}) (*prtDtListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtDtListOut{Body: prtDtListBody{Items: SupportedDoctypes}}, nil
	})

	// Bundled-source helper for "start from default".
	huma.Register(api, huma.Operation{
		OperationID: "get-print-bundled", Method: http.MethodGet,
		Path: "/admin/print-templates/bundled/{doctype}", Summary: "Get the bundled template source for a doctype",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, in *prtBundledIn) (*prtBundledOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		src, ok := BundledTemplateSource(in.Doctype)
		if !ok {
			return &prtBundledOut{Body: prtBundledBody{HasBundled: false}}, nil
		}
		return &prtBundledOut{Body: prtBundledBody{HasBundled: true, BodyHTML: src}}, nil
	})

	// Preview — render arbitrary body+letterhead with sample data, return PDF bytes.
	huma.Register(api, huma.Operation{
		OperationID: "preview-print", Method: http.MethodPost,
		Path: "/admin/print-templates/preview", Summary: "Render a preview PDF from supplied body + letterhead",
		Tags: []string{"Admin / Print"},
	}, func(ctx context.Context, in *prtPreviewIn) (*prtPdfOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		body := in.Body.BodyHTML
		if body == "" {
			if src, ok := BundledTemplateSource(in.Body.Doctype); ok {
				body = src
			}
		}
		tpl := &PrintTemplate{Doctype: in.Body.Doctype, BodyHTML: body, IsEnabled: true}
		lh := &Letterhead{
			Name:        "preview",
			LogoURL:     in.Body.LogoURL,
			HeaderHTML:  in.Body.HeaderHTML,
			FooterHTML:  in.Body.FooterHTML,
			PaperSize:   safeStr(in.Body.PaperSize, "A4"),
			MarginTop:   parseMargin(in.Body.MarginTop, "0.5"),
			MarginBottom: parseMargin(in.Body.MarginBottom, "0.5"),
			MarginLeft:  parseMargin(in.Body.MarginLeft, "0.5"),
			MarginRight: parseMargin(in.Body.MarginRight, "0.5"),
		}
		vars := SampleContext(in.Body.Doctype)
		vars["Company"] = SampleCompany()
		pdf, err := h.Service.RenderToPDF(ctx, &ResolvedTemplate{Template: tpl, Letterhead: lh}, vars)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prtPdfOut{
			ContentType:        "application/pdf",
			ContentDisposition: `inline; filename="preview.pdf"`,
			Body:               pdf,
		}, nil
	})
}

func safeStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

type (
	prtLhListOut  struct{ Body prtLhListBody }
	prtLhListBody struct {
		Items []Letterhead `json:"items"`
	}
	prtLhItemOut  struct{ Body Letterhead }
	prtLhCreateIn struct{ Body LetterheadInput }
	prtLhUpdateIn struct {
		ID   string `path:"id"`
		Body LetterheadInput
	}
	prtLhByID struct {
		ID string `path:"id"`
	}

	prtTplListOut  struct{ Body prtTplListBody }
	prtTplListBody struct {
		Items []PrintTemplate `json:"items"`
	}
	prtTplItemOut  struct{ Body PrintTemplate }
	prtTplCreateIn struct{ Body PrintTemplateInput }
	prtTplUpdateIn struct {
		ID   string `path:"id"`
		Body PrintTemplateInput
	}
	prtTplByID struct {
		ID string `path:"id"`
	}

	prtDtListOut  struct{ Body prtDtListBody }
	prtDtListBody struct {
		Items []DoctypeDef `json:"items"`
	}

	prtBundledIn struct {
		Doctype string `path:"doctype"`
	}
	prtBundledOut  struct{ Body prtBundledBody }
	prtBundledBody struct {
		HasBundled bool   `json:"has_bundled"`
		BodyHTML   string `json:"body_html,omitempty"`
	}

	prtPreviewIn struct {
		Body PreviewInput
	}
	prtPdfOut struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}
)

type PreviewInput struct {
	Doctype      string `json:"doctype"`
	BodyHTML     string `json:"body_html,omitempty"`
	HeaderHTML   string `json:"header_html,omitempty"`
	FooterHTML   string `json:"footer_html,omitempty"`
	LogoURL      string `json:"logo_url,omitempty"`
	PaperSize    string `json:"paper_size,omitempty"`
	MarginTop    string `json:"margin_top,omitempty"`
	MarginBottom string `json:"margin_bottom,omitempty"`
	MarginLeft   string `json:"margin_left,omitempty"`
	MarginRight  string `json:"margin_right,omitempty"`
}
