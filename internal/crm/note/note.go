// Package note is the user-emitted free-text annotation doctype. Attaches
// to any record via (parent_doctype, parent_id) dynamic link. The Timeline
// component reads notes alongside audit_log so the user sees one stream of
// "what happened on this record."
//
// Edits + soft-delete are author-only — anyone with read access to the
// parent can READ; only the original creator (or a system admin) can
// EDIT/DELETE their own notes.
package note

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

const Doctype = "note"

// parentAllowlist is the set of doctypes a note can hang off. New doctypes
// opt in here. Keeps the dynamic-link pattern from becoming a free-for-all.
var parentAllowlist = map[string]bool{
	"customer":     true,
	"supplier":     true,
	"lead":         true,
	"contact":      true,
	"opportunity":  true,
	"asset":        true,
	"purchase_order":   true,
	"sales_invoice":    true,
	"purchase_invoice": true,
}

type Note struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	ParentDoctype string    `json:"parent_doctype"`
	ParentID      string    `json:"parent_id"`
	Body          string    `json:"body"`
	IsDeleted     bool      `json:"is_deleted"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by"`
}

type NoteCreateInput struct {
	ParentDoctype string `json:"parent_doctype"`
	ParentID      string `json:"parent_id"`
	Body          string `json:"body"`
	CompanyID     string `json:"company_id,omitempty"`
}

type NoteUpdateInput struct {
	Body string `json:"body"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in NoteCreateInput) (*Note, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("note: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("note.company_id: required")
	}
	if !parentAllowlist[in.ParentDoctype] {
		return nil, fmt.Errorf("note.parent_doctype: %q not allowed (allowlist in internal/crm/note)", in.ParentDoctype)
	}
	if strings.TrimSpace(in.ParentID) == "" {
		return nil, errors.New("note.parent_id: required")
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return nil, errors.New("note.body: required")
	}

	id := dbx.NewIDWithPrefix("note")
	var out Note
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO note (id, company_id, parent_doctype, parent_id, body, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			id, in.CompanyID, in.ParentDoctype, in.ParentID, in.Body, p.UserID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Update(ctx context.Context, id string, in NoteUpdateInput) (*Note, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("note: unauthenticated")
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return nil, errors.New("note.body: required")
	}
	var out Note
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Author-only edit: anyone with row visibility can READ the note,
		// but only the original creator (or a system admin) can mutate it.
		var creator string
		if err := tx.QueryRow(ctx,
			`SELECT created_by FROM note WHERE id = $1 AND is_deleted = false`, id).Scan(&creator); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("note %s not found", id)
			}
			return err
		}
		if creator != p.UserID && !p.IsSystem {
			return errors.New("note: only the author can edit")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE note SET body = $1, updated_by = $2 WHERE id = $3`,
			in.Body, p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("note: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		var creator string
		if err := tx.QueryRow(ctx,
			`SELECT created_by FROM note WHERE id = $1 AND is_deleted = false`, id).Scan(&creator); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("note %s not found", id)
			}
			return err
		}
		if creator != p.UserID && !p.IsSystem {
			return errors.New("note: only the author can delete")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE note SET is_deleted = true, updated_by = $1 WHERE id = $2`, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

func (s *Service) Get(ctx context.Context, id string) (*Note, error) {
	var out *Note
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		n, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = n
		return nil
	})
	return out, err
}

// List returns notes in the active company. When parent_doctype + parent_id
// are supplied, narrows to that parent — the typical Timeline read.
// Most recent first; capped at 200 to keep responses bounded.
func (s *Service) List(ctx context.Context, companyID, parentDoctype, parentID string) ([]Note, error) {
	args := []any{companyID}
	q := `SELECT id, company_id, parent_doctype, parent_id, body,
	             is_deleted, created_at, updated_at, created_by
	      FROM note WHERE company_id = $1 AND is_deleted = false`
	if parentDoctype != "" && parentID != "" {
		q += ` AND parent_doctype = $2 AND parent_id = $3`
		args = append(args, parentDoctype, parentID)
	}
	q += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.CompanyID, &n.ParentDoctype, &n.ParentID, &n.Body,
			&n.IsDeleted, &n.CreatedAt, &n.UpdatedAt, &n.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func load(ctx context.Context, tx pgx.Tx, id string) (*Note, error) {
	var n Note
	err := tx.QueryRow(ctx, `
		SELECT id, company_id, parent_doctype, parent_id, body,
		       is_deleted, created_at, updated_at, created_by
		FROM note WHERE id = $1`, id).
		Scan(&n.ID, &n.CompanyID, &n.ParentDoctype, &n.ParentID, &n.Body,
			&n.IsDeleted, &n.CreatedAt, &n.UpdatedAt, &n.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("note %s not found", id)
	}
	return &n, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-notes", Method: http.MethodGet,
		Path: "/crm/notes", Summary: "List notes (optionally narrowed to one parent)",
		Tags: []string{"CRM / Note"},
	}, func(ctx context.Context, in *noteListIn) (*noteListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ns, err := h.Service.List(ctx, co, in.ParentDoctype, in.ParentID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &noteListOut{Body: noteListBody{Items: ns}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-note", Method: http.MethodPost,
		Path: "/crm/notes", Summary: "Create a note attached to a record",
		Tags: []string{"CRM / Note"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *noteCreateIn) (*noteOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		n, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &noteOut{Body: *n}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-note", Method: http.MethodGet,
		Path: "/crm/notes/{id}", Summary: "Get a note",
		Tags: []string{"CRM / Note"},
	}, func(ctx context.Context, in *noteGetIn) (*noteOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		n, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &noteOut{Body: *n}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-note", Method: http.MethodPut,
		Path: "/crm/notes/{id}", Summary: "Edit a note (author-only)",
		Tags: []string{"CRM / Note"},
	}, func(ctx context.Context, in *noteUpdateIn) (*noteOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		n, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &noteOut{Body: *n}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-note", Method: http.MethodDelete,
		Path: "/crm/notes/{id}", Summary: "Soft-delete a note (author-only)",
		Tags: []string{"CRM / Note"},
	}, func(ctx context.Context, in *noteGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	noteCreateIn struct{ Body NoteCreateInput }
	noteUpdateIn struct {
		ID   string `path:"id"`
		Body NoteUpdateInput
	}
	noteGetIn struct {
		ID string `path:"id"`
	}
	noteListIn struct {
		ParentDoctype string `query:"parent_doctype"`
		ParentID      string `query:"parent_id"`
	}
	noteOut     struct{ Body Note }
	noteListOut struct{ Body noteListBody }
	noteListBody struct {
		Items []Note `json:"items"`
	}
)
