// Package issue implements the Helpdesk Issue/Ticket doctype with SLA target tracking.
package issue

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
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "issue"

type Issue struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	CompanyID          string     `json:"company_id"`
	Subject            string     `json:"subject"`
	Description        string     `json:"description,omitempty"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	CustomerID         string     `json:"customer_id,omitempty"`
	ContactEmail       string     `json:"contact_email,omitempty"`
	AssignedToUserID   string     `json:"assigned_to_user_id,omitempty"`
	SLAID              string     `json:"sla_id,omitempty"`
	OpenedAt           time.Time  `json:"opened_at"`
	ResponseDueAt      *time.Time `json:"response_due_at,omitempty"`
	ResolutionDueAt    *time.Time `json:"resolution_due_at,omitempty"`
	FirstRespondedAt   *time.Time `json:"first_responded_at,omitempty"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	ResolutionRemarks  string     `json:"resolution_remarks,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type IssueCreateInput struct {
	CompanyID    string `json:"company_id,omitempty"`
	Subject      string `json:"subject"`
	Description  string `json:"description,omitempty"`
	Priority     string `json:"priority,omitempty"`
	CustomerID   string `json:"customer_id,omitempty"`
	ContactEmail string `json:"contact_email,omitempty"`
	SLAID        string `json:"sla_id,omitempty"`
	AssignTo     string `json:"assign_to,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in IssueCreateInput) (*Issue, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("issue: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("issue.company_id: required")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("issue.subject: required")
	}
	priority := in.Priority
	if priority == "" {
		priority = "Medium"
	}

	id := dbx.NewIDWithPrefix("iss")
	var iss Issue
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}

		// Compute SLA due times if SLA provided.
		var responseDue, resolutionDue *time.Time
		if in.SLAID != "" {
			var responseHours, resolutionHours int
			if err := tx.QueryRow(ctx,
				`SELECT response_time_hours, resolution_time_hours FROM service_level_agreement WHERE id = $1 AND is_deleted = false`,
				in.SLAID).Scan(&responseHours, &resolutionHours); err != nil {
				return fmt.Errorf("sla lookup: %w", err)
			}
			now := time.Now().UTC()
			rd := now.Add(time.Duration(responseHours) * time.Hour)
			rs := now.Add(time.Duration(resolutionHours) * time.Hour)
			responseDue = &rd
			resolutionDue = &rs
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO issue (id, name, company_id, subject, description, priority, customer_id, contact_email, sla_id, assigned_to_user_id,
			                  response_due_at, resolution_due_at, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
			RETURNING id, name, company_id, subject, coalesce(description,''), status, priority,
			          coalesce(customer_id,''), coalesce(contact_email,''), coalesce(assigned_to_user_id,''), coalesce(sla_id,''),
			          opened_at, response_due_at, resolution_due_at, first_responded_at, resolved_at, closed_at, coalesce(resolution_remarks,''),
			          created_at, updated_at`,
			id, name, in.CompanyID, in.Subject, nullable(in.Description), priority,
			nullable(in.CustomerID), nullable(in.ContactEmail), nullable(in.SLAID), nullable(in.AssignTo),
			responseDue, resolutionDue, p.UserID).
			Scan(&iss.ID, &iss.Name, &iss.CompanyID, &iss.Subject, &iss.Description, &iss.Status, &iss.Priority,
				&iss.CustomerID, &iss.ContactEmail, &iss.AssignedToUserID, &iss.SLAID,
				&iss.OpenedAt, &iss.ResponseDueAt, &iss.ResolutionDueAt, &iss.FirstRespondedAt, &iss.ResolvedAt, &iss.ClosedAt, &iss.ResolutionRemarks,
				&iss.CreatedAt, &iss.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, iss.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &iss, err
}

// UpdateStatus transitions an issue: Open → In Progress → Resolved → Closed.
// Auto-stamps first_responded_at / resolved_at / closed_at where appropriate.
type StatusUpdateInput struct {
	Status            string `json:"status"`
	ResolutionRemarks string `json:"resolution_remarks,omitempty"`
}

func (s *Service) UpdateStatus(ctx context.Context, id string, in StatusUpdateInput) (*Issue, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("issue: unauthenticated")
	}
	valid := map[string]bool{"Open": true, "In Progress": true, "On Hold": true, "Resolved": true, "Closed": true, "Cancelled": true}
	if !valid[in.Status] {
		return nil, errors.New("issue.status: invalid")
	}
	now := time.Now().UTC()
	var stamp string
	switch in.Status {
	case "In Progress":
		stamp = `first_responded_at = coalesce(first_responded_at, $4)`
	case "Resolved":
		stamp = `resolved_at = coalesce(resolved_at, $4), first_responded_at = coalesce(first_responded_at, $4)`
	case "Closed":
		stamp = `closed_at = coalesce(closed_at, $4), resolved_at = coalesce(resolved_at, $4), first_responded_at = coalesce(first_responded_at, $4)`
	default:
		stamp = `updated_at = $4`
	}
	q := fmt.Sprintf(`UPDATE issue SET status = $1, resolution_remarks = coalesce($2, resolution_remarks), updated_by = $3, %s WHERE id = $5`, stamp)
	if _, err := s.db.Exec(ctx, q, in.Status, nullable(in.ResolutionRemarks), p.UserID, now, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*Issue, error) {
	var iss Issue
	err := s.db.QueryRow(ctx, `
		SELECT id, name, company_id, subject, coalesce(description,''), status, priority,
		       coalesce(customer_id,''), coalesce(contact_email,''), coalesce(assigned_to_user_id,''), coalesce(sla_id,''),
		       opened_at, response_due_at, resolution_due_at, first_responded_at, resolved_at, closed_at, coalesce(resolution_remarks,''),
		       created_at, updated_at
		FROM issue WHERE id = $1`, id).
		Scan(&iss.ID, &iss.Name, &iss.CompanyID, &iss.Subject, &iss.Description, &iss.Status, &iss.Priority,
			&iss.CustomerID, &iss.ContactEmail, &iss.AssignedToUserID, &iss.SLAID,
			&iss.OpenedAt, &iss.ResponseDueAt, &iss.ResolutionDueAt, &iss.FirstRespondedAt, &iss.ResolvedAt, &iss.ClosedAt, &iss.ResolutionRemarks,
			&iss.CreatedAt, &iss.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("issue %s not found", id)
	}
	return &iss, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Issue, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, company_id, subject, coalesce(description,''), status, priority,
		       coalesce(customer_id,''), coalesce(contact_email,''), coalesce(assigned_to_user_id,''), coalesce(sla_id,''),
		       opened_at, response_due_at, resolution_due_at, first_responded_at, resolved_at, closed_at, coalesce(resolution_remarks,''),
		       created_at, updated_at
		FROM issue WHERE company_id = $1 ORDER BY opened_at DESC LIMIT 200`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var iss Issue
		if err := rows.Scan(&iss.ID, &iss.Name, &iss.CompanyID, &iss.Subject, &iss.Description, &iss.Status, &iss.Priority,
			&iss.CustomerID, &iss.ContactEmail, &iss.AssignedToUserID, &iss.SLAID,
			&iss.OpenedAt, &iss.ResponseDueAt, &iss.ResolutionDueAt, &iss.FirstRespondedAt, &iss.ResolvedAt, &iss.ClosedAt, &iss.ResolutionRemarks,
			&iss.CreatedAt, &iss.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, _ string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `SELECT id, pattern FROM naming_series WHERE doctype = $1 AND is_default = true AND company_id IS NULL LIMIT 1`, doctype).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-issues", Method: http.MethodGet,
		Path: "/support/issues", Summary: "List issues",
		Tags: []string{"Support / Issue"},
	}, func(ctx context.Context, _ *struct{}) (*issListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		is, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &issListOut{Body: issListBody{Items: is}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-issue",
		Method:        http.MethodPost,
		Path:          "/support/issues",
		Summary:       "Create an issue",
		Tags:          []string{"Support / Issue"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *issCreateIn) (*issOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		i, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &issOut{Body: *i}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-issue-status", Method: http.MethodPost,
		Path: "/support/issues/{id}/status", Summary: "Transition issue status",
		Tags: []string{"Support / Issue"},
	}, func(ctx context.Context, in *issStatusIn) (*issOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		i, err := h.Service.UpdateStatus(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &issOut{Body: *i}, nil
	})
}

type (
	issCreateIn struct{ Body IssueCreateInput }
	issOut      struct{ Body Issue }
	issListOut  struct{ Body issListBody }
	issListBody struct {
		Items []Issue `json:"items"`
	}
	issStatusIn struct {
		ID   string            `path:"id"`
		Body StatusUpdateInput `json:""`
	}
)
