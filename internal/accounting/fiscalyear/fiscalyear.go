// Package fiscalyear manages the fiscal_year master + per-company linkage.
// The closing workflow itself lives in periodclosing.
package fiscalyear

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

const Doctype = "fiscal_year"

type FiscalYear struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	IsClosed  bool      `json:"is_closed"`
	Companies []string  `json:"companies"`
}

type CreateInput struct {
	Name      string   `json:"name"      doc:"e.g. 2026 or FY-2026"`
	StartDate string   `json:"start_date" doc:"YYYY-MM-DD"`
	EndDate   string   `json:"end_date"   doc:"YYYY-MM-DD"`
	Companies []string `json:"companies,omitempty" doc:"company_ids to link; omit to apply to all"`
}

type UpdateInput struct {
	IsClosed *bool `json:"is_closed,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) List(ctx context.Context, companyID string) ([]FiscalYear, error) {
	rows, err := s.db.Query(ctx, `
		SELECT fy.id, fy.name, fy.start_date, fy.end_date, fy.is_closed,
		       coalesce(array_agg(fyc.company_id ORDER BY fyc.company_id)
		                FILTER (WHERE fyc.company_id IS NOT NULL), '{}')
		FROM fiscal_year fy
		LEFT JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
		WHERE ($1 = '' OR fyc.company_id = $1 OR NOT EXISTS (
		         SELECT 1 FROM fiscal_year_company x WHERE x.fiscal_year_id = fy.id))
		GROUP BY fy.id
		ORDER BY fy.start_date DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FiscalYear, 0)
	for rows.Next() {
		var fy FiscalYear
		if err := rows.Scan(&fy.ID, &fy.Name, &fy.StartDate, &fy.EndDate, &fy.IsClosed, &fy.Companies); err != nil {
			return nil, err
		}
		out = append(out, fy)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*FiscalYear, error) {
	if _ = auth.FromContext(ctx); auth.FromContext(ctx) == nil {
		return nil, errors.New("fiscal_year: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("fiscal_year.name: required")
	}
	start, err := time.Parse("2006-01-02", in.StartDate)
	if err != nil {
		return nil, fmt.Errorf("start_date: %w", err)
	}
	end, err := time.Parse("2006-01-02", in.EndDate)
	if err != nil {
		return nil, fmt.Errorf("end_date: %w", err)
	}
	if !end.After(start) {
		return nil, errors.New("end_date must be after start_date")
	}

	id := dbx.NewIDWithPrefix("fy")
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO fiscal_year (id, name, start_date, end_date, is_closed)
			VALUES ($1,$2,$3,$4,false)`, id, in.Name, start, end); err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("fiscal_year: name already used")
			}
			return err
		}
		for _, c := range in.Companies {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fiscal_year_company (fiscal_year_id, company_id) VALUES ($1,$2)
				ON CONFLICT DO NOTHING`, id, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*FiscalYear, error) {
	if in.IsClosed != nil {
		if _, err := s.db.Exec(ctx, `UPDATE fiscal_year SET is_closed = $2 WHERE id = $1`, id, *in.IsClosed); err != nil {
			return nil, err
		}
	}
	return s.get(ctx, id)
}

func (s *Service) get(ctx context.Context, id string) (*FiscalYear, error) {
	var fy FiscalYear
	err := s.db.QueryRow(ctx, `
		SELECT fy.id, fy.name, fy.start_date, fy.end_date, fy.is_closed,
		       coalesce(array_agg(fyc.company_id ORDER BY fyc.company_id)
		                FILTER (WHERE fyc.company_id IS NOT NULL), '{}')
		FROM fiscal_year fy
		LEFT JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
		WHERE fy.id = $1
		GROUP BY fy.id`, id).
		Scan(&fy.ID, &fy.Name, &fy.StartDate, &fy.EndDate, &fy.IsClosed, &fy.Companies)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("fiscal_year %s: not found", id)
	}
	return &fy, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-fiscal-years", Method: http.MethodGet,
		Path: "/admin/fiscal-years", Summary: "List fiscal years",
		Tags: []string{"Admin / Fiscal"},
	}, func(ctx context.Context, _ *struct{}) (*fyListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		ys, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fyListOut{Body: fyListBody{Items: ys}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-fiscal-year", Method: http.MethodPost,
		Path: "/admin/fiscal-years", Summary: "Create a fiscal year",
		Tags: []string{"Admin / Fiscal"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *fyCreateIn) (*fyItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		fy, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fyItemOut{Body: *fy}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-fiscal-year", Method: http.MethodPut,
		Path: "/admin/fiscal-years/{id}", Summary: "Update a fiscal year (close / re-open)",
		Tags: []string{"Admin / Fiscal"},
	}, func(ctx context.Context, in *fyUpdateIn) (*fyItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		fy, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fyItemOut{Body: *fy}, nil
	})
}

type (
	fyItemOut struct{ Body FiscalYear }
	fyCreateIn struct{ Body CreateInput }
	fyUpdateIn struct {
		ID   string `path:"id"`
		Body UpdateInput
	}
	fyListOut  struct{ Body fyListBody }
	fyListBody struct {
		Items []FiscalYear `json:"items"`
	}
)
