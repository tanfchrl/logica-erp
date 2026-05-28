// Package crmmasters owns the two admin-editable picklist masters that CRM
// forms use: Lead Source and Lost Reason. Tiny CRUDs — one Go file is
// enough for both.
package crmmasters

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
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// ---- types ----

type LeadSource struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type LostReason struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type MasterInput struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- shared helpers ----

func (s *Service) createMaster(ctx context.Context, table, prefix, doctype, companyID string, in MasterInput) (id, name string, err error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return "", "", errors.New(doctype + ": unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "", "", errors.New(doctype + ".name: required")
	}
	id = dbx.NewIDWithPrefix(prefix)
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		isActive := true
		if in.IsActive != nil {
			isActive = *in.IsActive
		}
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (id, company_id, name, is_active) VALUES ($1,$2,$3,$4)`, table),
			id, companyID, in.Name, isActive); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("%s: %q already exists", doctype, in.Name)
			}
			return err
		}
		return audit.Record(ctx, tx, doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return id, in.Name, err
}

func (s *Service) updateMaster(ctx context.Context, table, doctype, id string, in MasterInput) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New(doctype + ": unauthenticated")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New(doctype + ".name: required")
	}
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET name = $1, is_active = $2 WHERE id = $3`, table),
			in.Name, isActive, id)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("%s: %q already exists", doctype, in.Name)
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%s %s not found", doctype, id)
		}
		return audit.Record(ctx, tx, doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
}

func (s *Service) deleteMaster(ctx context.Context, table, doctype, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New(doctype + ": unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Hard-delete is OK — entries are referenced by name (free text)
		// on lead.source / opportunity.lost_reason, not by FK. Deleting
		// a master just removes the picklist entry; historic text values
		// keep showing.
		tag, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, table), id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%s %s not found", doctype, id)
		}
		return audit.Record(ctx, tx, doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

// ---- Lead Source ----

func (s *Service) CreateLeadSource(ctx context.Context, companyID string, in MasterInput) (*LeadSource, error) {
	id, _, err := s.createMaster(ctx, "lead_source", "lsrc", "lead_source", companyID, in)
	if err != nil {
		return nil, err
	}
	return s.getLeadSource(ctx, id)
}

func (s *Service) ListLeadSources(ctx context.Context, companyID string) ([]LeadSource, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, company_id, name, is_active, created_at FROM lead_source
		 WHERE company_id = $1 ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeadSource
	for rows.Next() {
		var v LeadSource
		if err := rows.Scan(&v.ID, &v.CompanyID, &v.Name, &v.IsActive, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) UpdateLeadSource(ctx context.Context, id string, in MasterInput) (*LeadSource, error) {
	if err := s.updateMaster(ctx, "lead_source", "lead_source", id, in); err != nil {
		return nil, err
	}
	return s.getLeadSource(ctx, id)
}

func (s *Service) DeleteLeadSource(ctx context.Context, id string) error {
	return s.deleteMaster(ctx, "lead_source", "lead_source", id)
}

func (s *Service) getLeadSource(ctx context.Context, id string) (*LeadSource, error) {
	var v LeadSource
	err := s.db.QueryRow(ctx,
		`SELECT id, company_id, name, is_active, created_at FROM lead_source WHERE id = $1`, id).
		Scan(&v.ID, &v.CompanyID, &v.Name, &v.IsActive, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lead_source %s not found", id)
	}
	return &v, err
}

// ---- Lost Reason ----

func (s *Service) CreateLostReason(ctx context.Context, companyID string, in MasterInput) (*LostReason, error) {
	id, _, err := s.createMaster(ctx, "lost_reason", "lrsn", "lost_reason", companyID, in)
	if err != nil {
		return nil, err
	}
	return s.getLostReason(ctx, id)
}

func (s *Service) ListLostReasons(ctx context.Context, companyID string) ([]LostReason, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, company_id, name, is_active, created_at FROM lost_reason
		 WHERE company_id = $1 ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LostReason
	for rows.Next() {
		var v LostReason
		if err := rows.Scan(&v.ID, &v.CompanyID, &v.Name, &v.IsActive, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) UpdateLostReason(ctx context.Context, id string, in MasterInput) (*LostReason, error) {
	if err := s.updateMaster(ctx, "lost_reason", "lost_reason", id, in); err != nil {
		return nil, err
	}
	return s.getLostReason(ctx, id)
}

func (s *Service) DeleteLostReason(ctx context.Context, id string) error {
	return s.deleteMaster(ctx, "lost_reason", "lost_reason", id)
}

func (s *Service) getLostReason(ctx context.Context, id string) (*LostReason, error) {
	var v LostReason
	err := s.db.QueryRow(ctx,
		`SELECT id, company_id, name, is_active, created_at FROM lost_reason WHERE id = $1`, id).
		Scan(&v.ID, &v.CompanyID, &v.Name, &v.IsActive, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lost_reason %s not found", id)
	}
	return &v, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	// Lead Source
	huma.Register(api, huma.Operation{
		OperationID: "list-lead-sources", Method: http.MethodGet,
		Path: "/crm/lead-sources", Summary: "List lead sources",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, _ *struct{}) (*lsListOut, error) {
		if err := h.Perm.Check(ctx, "lead_source", permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		vs, err := h.Service.ListLeadSources(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lsListOut{Body: lsListBody{Items: vs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-lead-source", Method: http.MethodPost,
		Path: "/crm/lead-sources", Summary: "Create a lead source",
		Tags: []string{"CRM / Masters"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *masterCreateIn) (*lsOut, error) {
		if err := h.Perm.Check(ctx, "lead_source", permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		v, err := h.Service.CreateLeadSource(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lsOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-lead-source", Method: http.MethodPut,
		Path: "/crm/lead-sources/{id}", Summary: "Update a lead source",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, in *masterUpdateIn) (*lsOut, error) {
		if err := h.Perm.Check(ctx, "lead_source", permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.UpdateLeadSource(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lsOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-lead-source", Method: http.MethodDelete,
		Path: "/crm/lead-sources/{id}", Summary: "Delete a lead source",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, in *masterGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, "lead_source", permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteLeadSource(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})

	// Lost Reason
	huma.Register(api, huma.Operation{
		OperationID: "list-lost-reasons", Method: http.MethodGet,
		Path: "/crm/lost-reasons", Summary: "List lost reasons",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, _ *struct{}) (*lrListOut, error) {
		if err := h.Perm.Check(ctx, "lost_reason", permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		vs, err := h.Service.ListLostReasons(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lrListOut{Body: lrListBody{Items: vs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-lost-reason", Method: http.MethodPost,
		Path: "/crm/lost-reasons", Summary: "Create a lost reason",
		Tags: []string{"CRM / Masters"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *masterCreateIn) (*lrOut, error) {
		if err := h.Perm.Check(ctx, "lost_reason", permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		v, err := h.Service.CreateLostReason(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lrOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-lost-reason", Method: http.MethodPut,
		Path: "/crm/lost-reasons/{id}", Summary: "Update a lost reason",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, in *masterUpdateIn) (*lrOut, error) {
		if err := h.Perm.Check(ctx, "lost_reason", permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.UpdateLostReason(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &lrOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-lost-reason", Method: http.MethodDelete,
		Path: "/crm/lost-reasons/{id}", Summary: "Delete a lost reason",
		Tags: []string{"CRM / Masters"},
	}, func(ctx context.Context, in *masterGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, "lost_reason", permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteLostReason(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	masterCreateIn struct{ Body MasterInput }
	masterUpdateIn struct {
		ID   string `path:"id"`
		Body MasterInput
	}
	masterGetIn struct {
		ID string `path:"id"`
	}
	lsOut     struct{ Body LeadSource }
	lsListOut struct{ Body lsListBody }
	lsListBody struct {
		Items []LeadSource `json:"items"`
	}
	lrOut     struct{ Body LostReason }
	lrListOut struct{ Body lrListBody }
	lrListBody struct {
		Items []LostReason `json:"items"`
	}
)
