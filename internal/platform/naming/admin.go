// admin.go exposes management endpoints for the naming_series + counter tables.
// The pattern engine itself lives in series.go and is unchanged.
package naming

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "naming_series"

// Series is the public shape returned by the admin API.
type Series struct {
	ID            string    `json:"id"`
	Doctype       string    `json:"doctype"`
	CompanyID     string    `json:"company_id,omitempty"`
	Pattern       string    `json:"pattern"`
	IsDefault     bool      `json:"is_default"`
	CreatedAt     time.Time `json:"created_at"`
	// CurrentValue is the max counter value across all scopes for this series.
	// Useful as a "where are we?" indicator in the UI.
	CurrentValue int64 `json:"current_value"`
}

type SeriesCreateInput struct {
	Doctype   string `json:"doctype"   doc:"Doctype the series produces names for (e.g. 'sales_invoice')"`
	CompanyID string `json:"company_id,omitempty" doc:"Scope to a single company; omit for an all-company series"`
	Pattern   string `json:"pattern"   doc:"e.g. SI-.YYYY.-.####"`
	IsDefault bool   `json:"is_default,omitempty"`
}

type SeriesUpdateInput struct {
	Pattern   string `json:"pattern,omitempty"`
	IsDefault *bool  `json:"is_default,omitempty"`
}

// AdminService — CRUD over naming_series + counter, with sanity checks.
type AdminService struct{ db *dbx.DB }

func NewAdminService(db *dbx.DB) *AdminService { return &AdminService{db: db} }

func (s *AdminService) List(ctx context.Context, doctype, companyID string) ([]Series, error) {
	q := `
		SELECT ns.id, ns.doctype, coalesce(ns.company_id,''), ns.pattern, ns.is_default, ns.created_at,
		       coalesce((SELECT max(current_value) FROM naming_series_counter WHERE series_id = ns.id), 0)
		FROM naming_series ns
		WHERE ($1 = '' OR ns.doctype = $1)
		  AND ($2 = '' OR ns.company_id = $2 OR ns.company_id IS NULL)
		ORDER BY ns.doctype, ns.is_default DESC, ns.pattern`
	rows, err := s.db.Query(ctx, q, doctype, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Series, 0)
	for rows.Next() {
		var ss Series
		if err := rows.Scan(&ss.ID, &ss.Doctype, &ss.CompanyID, &ss.Pattern, &ss.IsDefault, &ss.CreatedAt, &ss.CurrentValue); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *AdminService) Create(ctx context.Context, in SeriesCreateInput) (*Series, error) {
	in.Doctype = strings.TrimSpace(in.Doctype)
	in.Pattern = strings.TrimSpace(in.Pattern)
	if in.Doctype == "" {
		return nil, errors.New("naming_series.doctype: required")
	}
	if in.Pattern == "" {
		return nil, errors.New("naming_series.pattern: required")
	}
	// Compile once to surface bad patterns before the row hits the DB.
	if _, _, _, err := compile(in.Pattern, time.Now().UTC(), allowAnyPlaceholder); err != nil {
		return nil, fmt.Errorf("naming_series.pattern: %w", err)
	}

	id := dbx.NewIDWithPrefix("nms")
	var ss Series
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// If marking default, demote any existing default for this (doctype, company).
		if in.IsDefault {
			if _, err := tx.Exec(ctx, `
				UPDATE naming_series SET is_default = false
				WHERE doctype = $1 AND coalesce(company_id,'') = $2`, in.Doctype, in.CompanyID); err != nil {
				return err
			}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
			VALUES ($1,$2,$3,$4,$5)
			RETURNING id, doctype, coalesce(company_id,''), pattern, is_default, created_at`,
			id, in.Doctype, nullable(in.CompanyID), in.Pattern, in.IsDefault).
			Scan(&ss.ID, &ss.Doctype, &ss.CompanyID, &ss.Pattern, &ss.IsDefault, &ss.CreatedAt)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("naming_series: a series with this (doctype, company, pattern) already exists")
			}
			return err
		}
		return nil
	})
	return &ss, err
}

func (s *AdminService) Update(ctx context.Context, id string, in SeriesUpdateInput) (*Series, error) {
	var current Series
	if err := s.db.QueryRow(ctx, `
		SELECT id, doctype, coalesce(company_id,''), pattern, is_default, created_at
		FROM naming_series WHERE id = $1`, id).
		Scan(&current.ID, &current.Doctype, &current.CompanyID, &current.Pattern, &current.IsDefault, &current.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("naming_series %s: not found", id)
		}
		return nil, err
	}

	if in.Pattern != "" {
		if _, _, _, err := compile(in.Pattern, time.Now().UTC(), allowAnyPlaceholder); err != nil {
			return nil, fmt.Errorf("naming_series.pattern: %w", err)
		}
	}

	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if in.IsDefault != nil && *in.IsDefault {
			if _, err := tx.Exec(ctx, `
				UPDATE naming_series SET is_default = false
				WHERE doctype = $1 AND coalesce(company_id,'') = $2 AND id <> $3`,
				current.Doctype, current.CompanyID, id); err != nil {
				return err
			}
		}
		newPattern := current.Pattern
		if in.Pattern != "" {
			newPattern = in.Pattern
		}
		newDefault := current.IsDefault
		if in.IsDefault != nil {
			newDefault = *in.IsDefault
		}
		_, err := tx.Exec(ctx, `
			UPDATE naming_series SET pattern = $2, is_default = $3
			WHERE id = $1`, id, newPattern, newDefault)
		if err != nil && dbx.IsUniqueViolation(err) {
			return errors.New("naming_series: a series with this (doctype, company, pattern) already exists")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.get(ctx, id)
	return out, err
}

func (s *AdminService) Delete(ctx context.Context, id string) error {
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM naming_series WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("naming_series %s: not found", id)
		}
		return nil
	})
}

// ResetCounter wipes every counter scope for the series, so the next allocation
// starts at 1 again. Use with care — generates colliding names if old docs exist.
func (s *AdminService) ResetCounter(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM naming_series_counter WHERE series_id = $1`, id)
	return err
}

// Preview renders what the *next* allocation would look like for a given pattern
// without actually incrementing any counter. Useful for UI feedback.
func (s *AdminService) Preview(ctx context.Context, pattern, abbr string) (string, error) {
	resolve := func(p string) (string, bool) {
		switch p {
		case "company_abbr":
			return abbr, true
		}
		return "", false
	}
	parts, _, width, err := compile(pattern, time.Now().UTC(), resolve)
	if err != nil {
		return "", err
	}
	for i, p := range parts {
		if p == "\x00COUNTER" {
			parts[i] = fmt.Sprintf("%0*d", width, 1)
			break
		}
	}
	return strings.Join(parts, ""), nil
}

func (s *AdminService) get(ctx context.Context, id string) (*Series, error) {
	var ss Series
	err := s.db.QueryRow(ctx, `
		SELECT ns.id, ns.doctype, coalesce(ns.company_id,''), ns.pattern, ns.is_default, ns.created_at,
		       coalesce((SELECT max(current_value) FROM naming_series_counter WHERE series_id = ns.id), 0)
		FROM naming_series ns WHERE ns.id = $1`, id).
		Scan(&ss.ID, &ss.Doctype, &ss.CompanyID, &ss.Pattern, &ss.IsDefault, &ss.CreatedAt, &ss.CurrentValue)
	if err != nil {
		return nil, err
	}
	return &ss, nil
}

// Resolver that permits any non-counter placeholder during compile-only validation.
// Used by Create/Update so the pattern parses against the *grammar*, not the values
// (which can vary per-document).
func allowAnyPlaceholder(p string) (string, bool) {
	return "<" + p + ">", true
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
	huma.Register(api, huma.Operation{
		OperationID: "list-naming-series", Method: http.MethodGet,
		Path: "/admin/naming-series", Summary: "List naming series",
		Tags: []string{"Admin / Numbering"},
	}, func(ctx context.Context, in *nsListIn) (*nsListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := in.CompanyID
		if co == "" {
			co = auth.CompanyFromContext(ctx)
		}
		items, err := h.Service.List(ctx, in.Doctype, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nsListOut{Body: nsListBody{Items: items}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-naming-series", Method: http.MethodPost,
		Path: "/admin/naming-series", Summary: "Create a naming series",
		Tags: []string{"Admin / Numbering"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *nsCreateIn) (*nsItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		ss, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nsItemOut{Body: *ss}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-naming-series", Method: http.MethodPut,
		Path: "/admin/naming-series/{id}", Summary: "Update a naming series",
		Tags: []string{"Admin / Numbering"},
	}, func(ctx context.Context, in *nsUpdateIn) (*nsItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		ss, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nsItemOut{Body: *ss}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-naming-series", Method: http.MethodDelete,
		Path: "/admin/naming-series/{id}", Summary: "Delete a naming series",
		Tags: []string{"Admin / Numbering"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *nsByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "reset-naming-series-counter", Method: http.MethodPost,
		Path: "/admin/naming-series/{id}/reset", Summary: "Reset the counter for a naming series",
		Tags: []string{"Admin / Numbering"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *nsByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.ResetCounter(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-naming-series", Method: http.MethodGet,
		Path: "/admin/naming-series/preview", Summary: "Preview the next name a pattern would generate",
		Tags: []string{"Admin / Numbering"},
	}, func(ctx context.Context, in *nsPreviewIn) (*nsPreviewOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.Preview(ctx, in.Pattern, in.Abbr)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nsPreviewOut{Body: nsPreviewBody{Preview: v}}, nil
	})
}

type (
	nsListIn struct {
		Doctype   string `query:"doctype"    doc:"Filter by doctype"`
		CompanyID string `query:"company_id" doc:"Filter by company"`
	}
	nsListOut   struct{ Body nsListBody }
	nsListBody  struct {
		Items []Series `json:"items"`
	}
	nsCreateIn  struct{ Body SeriesCreateInput }
	nsUpdateIn  struct {
		ID   string `path:"id"`
		Body SeriesUpdateInput
	}
	nsByID struct {
		ID string `path:"id"`
	}
	nsItemOut struct{ Body Series }
	nsPreviewIn struct {
		Pattern string `query:"pattern" required:"true"`
		Abbr    string `query:"abbr"    doc:"Substitution for .company_abbr."`
	}
	nsPreviewOut  struct{ Body nsPreviewBody }
	nsPreviewBody struct {
		Preview string `json:"preview"`
	}
)
