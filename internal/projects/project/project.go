// Package project implements the Project doctype.
package project

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
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "project"

type Project struct {
	ID                    string          `json:"id"`
	Name                  string          `json:"name"`
	ProjectName           string          `json:"project_name"`
	CompanyID             string          `json:"company_id"`
	CustomerID            string          `json:"customer_id,omitempty"`
	Status                string          `json:"status"`
	StartDate             *time.Time      `json:"start_date,omitempty"`
	ExpectedEndDate       *time.Time      `json:"expected_end_date,omitempty"`
	ActualEndDate         *time.Time      `json:"actual_end_date,omitempty"`
	EstimatedCosting      decimal.Decimal `json:"estimated_costing"`
	TotalBillableAmount   decimal.Decimal `json:"total_billable_amount"`
	TotalBilledAmount     decimal.Decimal `json:"total_billed_amount"`
	Remarks               string          `json:"remarks,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type ProjectCreateInput struct {
	ProjectName     string `json:"project_name"`
	CompanyID       string `json:"company_id,omitempty"`
	CustomerID      string `json:"customer_id,omitempty"`
	StartDate       string `json:"start_date,omitempty"`
	ExpectedEndDate string `json:"expected_end_date,omitempty"`
	Remarks         string `json:"remarks,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in ProjectCreateInput) (*Project, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("project: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("project.company_id: required")
	}
	in.ProjectName = strings.TrimSpace(in.ProjectName)
	if in.ProjectName == "" {
		return nil, errors.New("project.project_name: required")
	}
	id := dbx.NewIDWithPrefix("proj")
	var pr Project
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickGlobalSeries(ctx, tx, Doctype)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO project (id, name, project_name, company_id, customer_id, start_date, expected_end_date, remarks, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			RETURNING id, name, project_name, company_id, coalesce(customer_id,''), status,
			          start_date, expected_end_date, actual_end_date,
			          estimated_costing, total_billable_amount, total_billed_amount,
			          coalesce(remarks,''), created_at, updated_at`,
			id, name, in.ProjectName, in.CompanyID, nullable(in.CustomerID),
			nullableDate(in.StartDate), nullableDate(in.ExpectedEndDate),
			nullable(in.Remarks), p.UserID).
			Scan(&pr.ID, &pr.Name, &pr.ProjectName, &pr.CompanyID, &pr.CustomerID, &pr.Status,
				&pr.StartDate, &pr.ExpectedEndDate, &pr.ActualEndDate,
				&pr.EstimatedCosting, &pr.TotalBillableAmount, &pr.TotalBilledAmount,
				&pr.Remarks, &pr.CreatedAt, &pr.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, pr.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &pr, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Project, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, project_name, company_id, coalesce(customer_id,''), status,
		       start_date, expected_end_date, actual_end_date,
		       estimated_costing, total_billable_amount, total_billed_amount,
		       coalesce(remarks,''), created_at, updated_at
		FROM project WHERE company_id = $1 AND is_deleted = false ORDER BY created_at DESC LIMIT 200`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var pr Project
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.ProjectName, &pr.CompanyID, &pr.CustomerID, &pr.Status,
			&pr.StartDate, &pr.ExpectedEndDate, &pr.ActualEndDate,
			&pr.EstimatedCosting, &pr.TotalBillableAmount, &pr.TotalBilledAmount,
			&pr.Remarks, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (*Project, error) {
	var pr Project
	err := s.db.QueryRow(ctx, `
		SELECT id, name, project_name, company_id, coalesce(customer_id,''), status,
		       start_date, expected_end_date, actual_end_date,
		       estimated_costing, total_billable_amount, total_billed_amount,
		       coalesce(remarks,''), created_at, updated_at
		FROM project WHERE id = $1 AND is_deleted = false`, id).
		Scan(&pr.ID, &pr.Name, &pr.ProjectName, &pr.CompanyID, &pr.CustomerID, &pr.Status,
			&pr.StartDate, &pr.ExpectedEndDate, &pr.ActualEndDate,
			&pr.EstimatedCosting, &pr.TotalBillableAmount, &pr.TotalBilledAmount,
			&pr.Remarks, &pr.CreatedAt, &pr.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("project %s not found", id)
	}
	return &pr, err
}

func pickGlobalSeries(ctx context.Context, tx pgx.Tx, doctype string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND company_id IS NULL LIMIT 1`, doctype).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no naming series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableDate(s string) any {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return t
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-projects", Method: http.MethodGet,
		Path: "/projects/projects", Summary: "List projects",
		Tags: []string{"Projects / Project"},
	}, func(ctx context.Context, _ *struct{}) (*projListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ps, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &projListOut{Body: projListBody{Items: ps}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-project", Method: http.MethodPost,
		Path: "/projects/projects", Summary: "Create a project",
		Tags: []string{"Projects / Project"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *projCreateIn) (*projOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		pr, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &projOut{Body: *pr}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-project", Method: http.MethodGet,
		Path: "/projects/projects/{id}", Summary: "Get a project",
		Tags: []string{"Projects / Project"},
	}, func(ctx context.Context, in *projGetIn) (*projOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		pr, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &projOut{Body: *pr}, nil
	})
}

type (
	projCreateIn struct{ Body ProjectCreateInput }
	projOut      struct{ Body Project }
	projListOut  struct{ Body projListBody }
	projListBody struct {
		Items []Project `json:"items"`
	}
	projGetIn struct {
		ID string `path:"id"`
	}
)
