// Package savedview is the generic per-user/per-doctype view persistence
// layer. Used by ListView on the FE to remember filter/sort/group/column
// configurations. v1 stores body as opaque JSONB; service doesn't validate
// shape because the FE owns it.
package savedview

import (
	"context"
	"encoding/json"
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
)

const Doctype = "saved_view"

type SavedView struct {
	ID        string          `json:"id"`
	CompanyID string          `json:"company_id"`
	UserID    string          `json:"user_id"`
	Doctype   string          `json:"doctype"`
	Name      string          `json:"name"`
	IsShared  bool            `json:"is_shared"`
	Body      json.RawMessage `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type SavedViewInput struct {
	Doctype  string          `json:"doctype"`
	Name     string          `json:"name"`
	IsShared bool            `json:"is_shared,omitempty"`
	Body     json.RawMessage `json:"body"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// Create — per-user view. Same user+doctype+name is rejected by the unique
// index; service surfaces a friendly error.
func (s *Service) Create(ctx context.Context, in SavedViewInput) (*SavedView, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("saved_view: unauthenticated")
	}
	companyID := auth.CompanyFromContext(ctx)
	if companyID == "" {
		return nil, errors.New("saved_view: X-Company-Id required")
	}
	in.Doctype = strings.TrimSpace(in.Doctype)
	in.Name = strings.TrimSpace(in.Name)
	if in.Doctype == "" || in.Name == "" {
		return nil, errors.New("saved_view: doctype + name required")
	}
	id := dbx.NewIDWithPrefix("sv")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO saved_view (id, company_id, user_id, doctype, name, is_shared, body)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, companyID, p.UserID, in.Doctype, in.Name, in.IsShared, in.Body); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("saved_view: name %q already in use for this doctype", in.Name)
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Update — owner-only. System admins can mutate any view (e.g. to fix a
// broken shared view); otherwise editor must equal owner.
func (s *Service) Update(ctx context.Context, id string, in SavedViewInput) (*SavedView, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("saved_view: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("saved_view.name: required")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT user_id FROM saved_view WHERE id = $1`, id).Scan(&owner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("saved_view %s not found", id)
			}
			return err
		}
		if owner != p.UserID && !p.IsSystem {
			return errors.New("saved_view: only the owner can edit")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE saved_view SET name = $1, is_shared = $2, body = $3
			WHERE id = $4`,
			in.Name, in.IsShared, in.Body, id); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("saved_view: name %q already in use for this doctype", in.Name)
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("saved_view: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT user_id FROM saved_view WHERE id = $1`, id).Scan(&owner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("saved_view %s not found", id)
			}
			return err
		}
		if owner != p.UserID && !p.IsSystem {
			return errors.New("saved_view: only the owner can delete")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM saved_view WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

func (s *Service) Get(ctx context.Context, id string) (*SavedView, error) {
	var v SavedView
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, user_id, doctype, name, is_shared, body, created_at, updated_at
		FROM saved_view WHERE id = $1`, id).
		Scan(&v.ID, &v.CompanyID, &v.UserID, &v.Doctype, &v.Name, &v.IsShared, &v.Body, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("saved_view %s not found", id)
	}
	return &v, err
}

// List returns the caller's own views + everyone-shared views for the
// given doctype, in alphabetical order. The FE renders these as a
// dropdown in the list header.
func (s *Service) List(ctx context.Context, doctype string) ([]SavedView, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("saved_view: unauthenticated")
	}
	companyID := auth.CompanyFromContext(ctx)
	if companyID == "" {
		return nil, errors.New("saved_view: X-Company-Id required")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, user_id, doctype, name, is_shared, body, created_at, updated_at
		FROM saved_view
		WHERE company_id = $1 AND doctype = $2
		  AND (user_id = $3 OR is_shared = true)
		ORDER BY is_shared DESC, name`,
		companyID, doctype, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedView
	for rows.Next() {
		var v SavedView
		if err := rows.Scan(&v.ID, &v.CompanyID, &v.UserID, &v.Doctype, &v.Name, &v.IsShared, &v.Body, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- HTTP ----

type Handler struct {
	Service *Service
}

func Register(api huma.API, h *Handler) {
	// No permission gate on saved_view per se — owner-scoped reads + author-
	// only writes are enforced in the service. Anyone authenticated can save
	// their own views.
	huma.Register(api, huma.Operation{
		OperationID: "list-saved-views", Method: http.MethodGet,
		Path: "/platform/saved-views", Summary: "List saved views for a doctype",
		Tags: []string{"Platform / Saved Views"},
	}, func(ctx context.Context, in *svListIn) (*svListOut, error) {
		if in.Doctype == "" {
			return nil, huma.NewError(http.StatusBadRequest, "doctype query param required")
		}
		vs, err := h.Service.List(ctx, in.Doctype)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &svListOut{Body: svListBody{Items: vs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-saved-view", Method: http.MethodPost,
		Path: "/platform/saved-views", Summary: "Save a view",
		Tags: []string{"Platform / Saved Views"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *svCreateIn) (*svOut, error) {
		v, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &svOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-saved-view", Method: http.MethodPut,
		Path: "/platform/saved-views/{id}", Summary: "Update a saved view (owner-only)",
		Tags: []string{"Platform / Saved Views"},
	}, func(ctx context.Context, in *svUpdateIn) (*svOut, error) {
		v, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &svOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-saved-view", Method: http.MethodDelete,
		Path: "/platform/saved-views/{id}", Summary: "Delete a saved view (owner-only)",
		Tags: []string{"Platform / Saved Views"},
	}, func(ctx context.Context, in *svGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	svCreateIn struct{ Body SavedViewInput }
	svUpdateIn struct {
		ID   string `path:"id"`
		Body SavedViewInput
	}
	svGetIn struct {
		ID string `path:"id"`
	}
	svListIn struct {
		Doctype string `query:"doctype" doc:"the doctype to list views for"`
	}
	svOut     struct{ Body SavedView }
	svListOut struct{ Body svListBody }
	svListBody struct {
		Items []SavedView `json:"items"`
	}
)
