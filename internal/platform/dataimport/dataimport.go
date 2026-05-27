// Package dataimport implements a small bulk-import pipeline. Each importable
// doctype registers a Recipe (field defs + a row-builder). The service has
// two phases:
//
//   Preview — runs every row through the recipe's validators, returns per-row
//             results without touching the DB. Used by the UI to show
//             "what's wrong" before commit.
//   Commit  — actually creates the records, one row per transaction. Records
//             the run in import_job for audit.
//
// Recipes do direct SQL inserts. The masters covered here (customer, supplier,
// item, account) have no pre-create business logic to speak of; bulk loaders
// that mirror service-layer Create can be added later as needs surface.
package dataimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "import_job"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type FieldDef struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Required    bool     `json:"required"`
	Type        string   `json:"type"`              // text, number, bool, date, email, select, lookup
	Description string   `json:"description,omitempty"`
	Options     []string `json:"options,omitempty"` // for `select`
	LookupHint  string   `json:"lookup_hint,omitempty"`
}

type Recipe struct {
	Doctype       string     `json:"doctype"`
	Label         string     `json:"label"`
	Description   string     `json:"description,omitempty"`
	CompanyScoped bool       `json:"company_scoped"`
	Fields        []FieldDef `json:"fields"`

	// Build constructs the SQL insert + arg list from a normalized row.
	// Returns the human-readable identifier created (e.g. account_number or display_name)
	// and an error if validation fails. Not serializable; the list endpoint
	// only returns the schema metadata, never the builder.
	Build func(ctx context.Context, tx pgx.Tx, companyID, userID string, row map[string]string) (createdName string, err error) `json:"-"`
}

// RowResult is what Preview/Commit return per input row.
type RowResult struct {
	RowNo   int               `json:"row_no"`
	Status  string            `json:"status"`             // ok | error
	Name    string            `json:"name,omitempty"`     // identifier for the created/would-create record
	Message string            `json:"message,omitempty"`  // error msg when status=error
	Fields  map[string]string `json:"fields,omitempty"`   // normalized values, for the UI
}

type ImportJob struct {
	ID           string    `json:"id"`
	Doctype      string    `json:"doctype"`
	CompanyID    string    `json:"company_id,omitempty"`
	TotalRows    int       `json:"total_rows"`
	SuccessRows  int       `json:"success_rows"`
	ErrorRows    int       `json:"error_rows"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
}

// ---------------------------------------------------------------------------
// Recipe registry — populated by recipes.go's init()
// ---------------------------------------------------------------------------

var registry = map[string]Recipe{}

func register(r Recipe) {
	registry[r.Doctype] = r
}

func recipes() []Recipe {
	out := make([]Recipe, 0, len(registry))
	for _, r := range registry {
		out = append(out, r)
	}
	// Stable order by Label for the UI.
	sortByLabel(out)
	return out
}

func sortByLabel(rs []Recipe) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Label < rs[j-1].Label; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Recipes() []Recipe { return recipes() }

// Apply runs a recipe against rows. `commit=false` does validation only (a
// short-lived rollback transaction per row). `commit=true` actually persists.
func (s *Service) Apply(ctx context.Context, doctype string, mapping map[string]string, rows [][]string, commit bool) ([]RowResult, ImportJob, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, ImportJob{}, errors.New("import: unauthenticated")
	}
	recipe, ok := registry[doctype]
	if !ok {
		return nil, ImportJob{}, fmt.Errorf("import: unknown doctype %q", doctype)
	}
	companyID := auth.CompanyFromContext(ctx)
	if recipe.CompanyScoped && companyID == "" {
		return nil, ImportJob{}, errors.New("import: X-Company-Id header required for this doctype")
	}

	if len(rows) == 0 {
		return []RowResult{}, ImportJob{}, errors.New("import: no rows")
	}
	header := rows[0]
	body := rows[1:]

	// Build header-index map from CSV column -> field key (via user-supplied mapping).
	colToField := map[int]string{}
	for ci, colName := range header {
		if field, ok := mapping[colName]; ok && field != "" && field != "ignore" {
			colToField[ci] = field
		}
	}
	// Sanity check: every required field must be mapped.
	mappedFields := map[string]bool{}
	for _, f := range colToField {
		mappedFields[f] = true
	}
	for _, fd := range recipe.Fields {
		if fd.Required && !mappedFields[fd.Key] {
			return nil, ImportJob{}, fmt.Errorf("import: required field %q is not mapped", fd.Key)
		}
	}

	results := make([]RowResult, 0, len(body))
	success := 0
	for i, raw := range body {
		// Skip completely empty rows quietly.
		if isAllEmpty(raw) {
			continue
		}
		row := map[string]string{}
		for ci, val := range raw {
			if field, ok := colToField[ci]; ok {
				row[field] = strings.TrimSpace(val)
			}
		}
		rr := RowResult{RowNo: i + 2, Fields: row} // +2 = 1-based, plus header row

		// Required-field check before hitting recipe.Build.
		if missing := requiredMissing(recipe, row); missing != "" {
			rr.Status = "error"
			rr.Message = "missing required field: " + missing
			results = append(results, rr)
			continue
		}

		err := s.db.Tx(ctx, func(tx pgx.Tx) error {
			name, err := recipe.Build(ctx, tx, companyID, p.UserID, row)
			rr.Name = name
			if err != nil {
				return err
			}
			if !commit {
				return errSimulated // forces rollback after validation passes
			}
			return nil
		})
		switch {
		case err == nil:
			rr.Status = "ok"
			success++
		case errors.Is(err, errSimulated):
			rr.Status = "ok"
			success++
		default:
			rr.Status = "error"
			rr.Message = err.Error()
		}
		results = append(results, rr)
	}

	job := ImportJob{
		ID: dbx.NewIDWithPrefix("imp"), Doctype: doctype, CompanyID: companyID,
		TotalRows: len(results), SuccessRows: success, ErrorRows: len(results) - success,
		Status: "preview", CreatedAt: time.Now().UTC(), CreatedBy: p.UserID,
	}
	if commit {
		job.Status = "committed"
		errsPayload, _ := json.Marshal(filterErrors(results))
		mappingPayload, _ := json.Marshal(mapping)
		if _, err := s.db.Exec(ctx, `
			INSERT INTO import_job (id, doctype, company_id, mapping, total_rows, success_rows, error_rows, status, errors, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			job.ID, job.Doctype, nullable(job.CompanyID), mappingPayload,
			job.TotalRows, job.SuccessRows, job.ErrorRows, job.Status, errsPayload, job.CreatedBy); err != nil {
			return nil, ImportJob{}, err
		}
	}

	return results, job, nil
}

func (s *Service) ListJobs(ctx context.Context, limit int) ([]ImportJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, doctype, coalesce(company_id,''), total_rows, success_rows, error_rows, status, created_at, created_by
		FROM import_job ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ImportJob, 0)
	for rows.Next() {
		var j ImportJob
		if err := rows.Scan(&j.ID, &j.Doctype, &j.CompanyID, &j.TotalRows, &j.SuccessRows, &j.ErrorRows, &j.Status, &j.CreatedAt, &j.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

var errSimulated = errors.New("import: preview rollback")

func filterErrors(rs []RowResult) []RowResult {
	out := []RowResult{}
	for _, r := range rs {
		if r.Status == "error" {
			out = append(out, r)
		}
	}
	return out
}

func requiredMissing(r Recipe, row map[string]string) string {
	for _, fd := range r.Fields {
		if !fd.Required {
			continue
		}
		v, ok := row[fd.Key]
		if !ok || strings.TrimSpace(v) == "" {
			return fd.Key
		}
	}
	return ""
}

func isAllEmpty(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// helpers used by recipes ----------------------------------------------------

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no", "n", "tidak":
		return false, nil
	case "1", "true", "yes", "y", "ya":
		return true, nil
	}
	return false, fmt.Errorf("not a boolean: %q", s)
}

func parseDecimalString(s string) (string, error) {
	t := strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if t == "" {
		return "0", nil
	}
	if _, err := strconv.ParseFloat(t, 64); err != nil {
		return "", fmt.Errorf("not a number: %q", s)
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-import-recipes",
		Method:      http.MethodGet,
		Path:        "/admin/imports/recipes",
		Summary:     "List importable doctypes and their fields",
		Tags:        []string{"Admin / Import"},
	}, func(ctx context.Context, _ *struct{}) (*impRecipesOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &impRecipesOut{Body: impRecipesBody{Items: h.Service.Recipes()}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-import",
		Method:      http.MethodPost,
		Path:        "/admin/imports/preview",
		Summary:     "Validate parsed CSV rows without committing",
		Tags:        []string{"Admin / Import"},
	}, func(ctx context.Context, in *impApplyIn) (*impApplyOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		res, job, err := h.Service.Apply(ctx, in.Body.Doctype, in.Body.Mapping, in.Body.Rows, false)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &impApplyOut{Body: impApplyBody{Job: job, Results: res}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "commit-import",
		Method:      http.MethodPost,
		Path:        "/admin/imports/commit",
		Summary:     "Commit parsed CSV rows; one row per micro-transaction",
		Tags:        []string{"Admin / Import"},
	}, func(ctx context.Context, in *impApplyIn) (*impApplyOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		res, job, err := h.Service.Apply(ctx, in.Body.Doctype, in.Body.Mapping, in.Body.Rows, true)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &impApplyOut{Body: impApplyBody{Job: job, Results: res}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-import-jobs",
		Method:      http.MethodGet,
		Path:        "/admin/imports/jobs",
		Summary:     "Recent import jobs",
		Tags:        []string{"Admin / Import"},
	}, func(ctx context.Context, in *impJobsIn) (*impJobsOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		js, err := h.Service.ListJobs(ctx, in.Limit)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &impJobsOut{Body: impJobsBody{Items: js}}, nil
	})
}

type (
	impRecipesOut  struct{ Body impRecipesBody }
	impRecipesBody struct {
		Items []Recipe `json:"items"`
	}
	impApplyIn struct{ Body impApplyInput }
	impApplyInput struct {
		Doctype string            `json:"doctype"`
		Mapping map[string]string `json:"mapping"`            // csv_col → field_key | "ignore"
		Rows    [][]string        `json:"rows"`               // row 0 = header
	}
	impApplyOut  struct{ Body impApplyBody }
	impApplyBody struct {
		Job     ImportJob   `json:"job"`
		Results []RowResult `json:"results"`
	}
	impJobsIn  struct { Limit int `query:"limit"` }
	impJobsOut struct{ Body impJobsBody }
	impJobsBody struct {
		Items []ImportJob `json:"items"`
	}
)
