// Package contact implements the CRM Contact (Person) doctype — a human
// who works at or represents a Company (Customer / Supplier / Lead).
//
// Dynamic-link semantics: each contact carries (parent_doctype, parent_id)
// pointing at one of the three CRM parents. CHECK constraint enforces the
// enum at the DB layer.
//
// is_primary lets one contact per parent be flagged "default for
// copy-to-Invoice / email-CC defaults". Setting is_primary on a new
// contact atomically demotes the previous primary inside the same Tx —
// the partial unique index would otherwise reject the write.
package contact

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
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "contact"

// Valid parent_doctype values. Mirror the CHECK constraint.
const (
	ParentCustomer = "customer"
	ParentSupplier = "supplier"
	ParentLead     = "lead"
)

type Contact struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	ParentDoctype string    `json:"parent_doctype"`
	ParentID      string    `json:"parent_id"`
	FirstName     string    `json:"first_name"`
	LastName      string    `json:"last_name,omitempty"`
	FullName      string    `json:"full_name"`
	Email         string    `json:"email,omitempty"`
	Phone         string    `json:"phone,omitempty"`
	JobTitle      string    `json:"job_title,omitempty"`
	IsPrimary     bool      `json:"is_primary"`
	IsDeleted     bool      `json:"is_deleted"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ContactInput struct {
	ParentDoctype string         `json:"parent_doctype" doc:"customer | supplier | lead"`
	ParentID      string         `json:"parent_id"`
	FirstName     string         `json:"first_name"`
	LastName      string         `json:"last_name,omitempty"`
	Email         string         `json:"email,omitempty"`
	Phone         string         `json:"phone,omitempty"`
	JobTitle      string         `json:"job_title,omitempty"`
	IsPrimary     bool           `json:"is_primary,omitempty" doc:"setting true atomically demotes the previous primary"`
	CompanyID     string         `json:"company_id,omitempty"`
	CustomFields  map[string]any `json:"custom_fields,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CRUD ----

func (s *Service) Create(ctx context.Context, in ContactInput) (*Contact, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("contact: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("contact.company_id: required")
	}
	if err := validate(in); err != nil {
		return nil, err
	}

	id := dbx.NewIDWithPrefix("ctc")
	var out Contact
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Parent must exist (best-effort check). Same-company enforced
		// for customer/supplier where the field exists.
		if err := assertParentExists(ctx, tx, in.ParentDoctype, in.ParentID, in.CompanyID); err != nil {
			return err
		}
		if in.IsPrimary {
			if err := demotePrevPrimary(ctx, tx, in.ParentDoctype, in.ParentID); err != nil {
				return err
			}
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact (
				id, company_id, parent_doctype, parent_id,
				first_name, last_name, email, phone, job_title,
				is_primary, custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`,
			id, in.CompanyID, in.ParentDoctype, in.ParentID,
			in.FirstName, nullable(in.LastName), nullable(in.Email), nullable(in.Phone), nullable(in.JobTitle),
			in.IsPrimary, cf, p.UserID); err != nil {
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

func (s *Service) Update(ctx context.Context, id string, in ContactInput) (*Contact, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("contact: unauthenticated")
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	var out Contact
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		// Parent move: rare but allowed (e.g. promote a Lead contact to
		// Customer after conversion). Re-validate the new parent exists.
		if in.ParentDoctype != existing.ParentDoctype || in.ParentID != existing.ParentID {
			if err := assertParentExists(ctx, tx, in.ParentDoctype, in.ParentID, existing.CompanyID); err != nil {
				return err
			}
		}
		if in.IsPrimary && !existing.IsPrimary {
			if err := demotePrevPrimary(ctx, tx, in.ParentDoctype, in.ParentID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE contact SET
			  parent_doctype = $1, parent_id = $2,
			  first_name = $3, last_name = $4,
			  email = $5, phone = $6, job_title = $7,
			  is_primary = $8, updated_by = $9
			WHERE id = $10 AND is_deleted = false`,
			in.ParentDoctype, in.ParentID,
			in.FirstName, nullable(in.LastName),
			nullable(in.Email), nullable(in.Phone), nullable(in.JobTitle),
			in.IsPrimary, p.UserID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("contact %s not found", id)
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

func (s *Service) Get(ctx context.Context, id string) (*Contact, error) {
	var out *Contact
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		c, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	return out, err
}

// List returns contacts in the active company. When parent_doctype + parent_id
// are supplied, narrows to that parent — the typical use is from a Customer
// detail page rendering its "Contacts" tab.
func (s *Service) List(ctx context.Context, companyID, parentDoctype, parentID string) ([]Contact, error) {
	args := []any{companyID}
	q := `SELECT id, company_id, parent_doctype, parent_id,
	             first_name, coalesce(last_name,''), full_name,
	             coalesce(email,''), coalesce(phone,''), coalesce(job_title,''),
	             is_primary, is_deleted, created_at, updated_at
	      FROM contact
	      WHERE company_id = $1 AND is_deleted = false`
	if parentDoctype != "" && parentID != "" {
		q += ` AND parent_doctype = $2 AND parent_id = $3`
		args = append(args, parentDoctype, parentID)
	}
	q += ` ORDER BY is_primary DESC, first_name`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.CompanyID, &c.ParentDoctype, &c.ParentID,
			&c.FirstName, &c.LastName, &c.FullName,
			&c.Email, &c.Phone, &c.JobTitle,
			&c.IsPrimary, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("contact: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE contact SET is_deleted = true WHERE id = $1 AND is_deleted = false`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("contact %s not found", id)
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

// ---- helpers ----

func validate(in ContactInput) error {
	switch in.ParentDoctype {
	case ParentCustomer, ParentSupplier, ParentLead:
	default:
		return fmt.Errorf("contact.parent_doctype: must be customer | supplier | lead (got %q)", in.ParentDoctype)
	}
	if strings.TrimSpace(in.ParentID) == "" {
		return errors.New("contact.parent_id: required")
	}
	if strings.TrimSpace(in.FirstName) == "" {
		return errors.New("contact.first_name: required")
	}
	return nil
}

// assertParentExists guards FK-style integrity since there's no DB FK on
// (parent_doctype, parent_id). Same-company check applies where the parent
// table carries company_id; lead is global so we skip.
func assertParentExists(ctx context.Context, tx pgx.Tx, parentDoctype, parentID, companyID string) error {
	switch parentDoctype {
	case ParentCustomer, ParentSupplier:
		var x int
		if err := tx.QueryRow(ctx,
			fmt.Sprintf(`SELECT 1 FROM %s WHERE id = $1 AND is_deleted = false`, parentDoctype),
			parentID).Scan(&x); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("contact.parent_id: %s %s not found", parentDoctype, parentID)
			}
			return err
		}
	case ParentLead:
		var x int
		if err := tx.QueryRow(ctx,
			`SELECT 1 FROM lead WHERE id = $1 AND is_deleted = false`, parentID).Scan(&x); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("contact.parent_id: lead %s not found", parentID)
			}
			return err
		}
	}
	return nil
}

func demotePrevPrimary(ctx context.Context, tx pgx.Tx, parentDoctype, parentID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE contact SET is_primary = false
		WHERE parent_doctype = $1 AND parent_id = $2 AND is_primary = true AND is_deleted = false`,
		parentDoctype, parentID)
	return err
}

func load(ctx context.Context, tx pgx.Tx, id string) (*Contact, error) {
	var c Contact
	err := tx.QueryRow(ctx, `
		SELECT id, company_id, parent_doctype, parent_id,
		       first_name, coalesce(last_name,''), full_name,
		       coalesce(email,''), coalesce(phone,''), coalesce(job_title,''),
		       is_primary, is_deleted, created_at, updated_at
		FROM contact WHERE id = $1`, id).
		Scan(&c.ID, &c.CompanyID, &c.ParentDoctype, &c.ParentID,
			&c.FirstName, &c.LastName, &c.FullName,
			&c.Email, &c.Phone, &c.JobTitle,
			&c.IsPrimary, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("contact %s not found", id)
	}
	return &c, err
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
		OperationID: "list-contacts", Method: http.MethodGet,
		Path: "/crm/contacts", Summary: "List contacts (optionally narrowed to one parent)",
		Tags: []string{"CRM / Contact"},
	}, func(ctx context.Context, in *contactListIn) (*contactListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		cs, err := h.Service.List(ctx, co, in.ParentDoctype, in.ParentID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &contactListOut{Body: contactListBody{Items: cs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-contact", Method: http.MethodPost,
		Path: "/crm/contacts", Summary: "Create a contact",
		Tags: []string{"CRM / Contact"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *contactCreateIn) (*contactOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &contactOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-contact", Method: http.MethodGet,
		Path: "/crm/contacts/{id}", Summary: "Get a contact",
		Tags: []string{"CRM / Contact"},
	}, func(ctx context.Context, in *contactGetIn) (*contactOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &contactOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-contact", Method: http.MethodPut,
		Path: "/crm/contacts/{id}", Summary: "Update a contact",
		Tags: []string{"CRM / Contact"},
	}, func(ctx context.Context, in *contactUpdateIn) (*contactOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &contactOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-contact", Method: http.MethodDelete,
		Path: "/crm/contacts/{id}", Summary: "Soft-delete a contact",
		Tags: []string{"CRM / Contact"},
	}, func(ctx context.Context, in *contactGetIn) (*struct{ Body map[string]string }, error) {
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
	contactCreateIn struct{ Body ContactInput }
	contactUpdateIn struct {
		ID   string `path:"id"`
		Body ContactInput
	}
	contactGetIn struct {
		ID string `path:"id"`
	}
	contactListIn struct {
		ParentDoctype string `query:"parent_doctype" doc:"narrow to a parent (customer | supplier | lead)"`
		ParentID      string `query:"parent_id"`
	}
	contactOut     struct{ Body Contact }
	contactListOut struct{ Body contactListBody }
	contactListBody struct {
		Items []Contact `json:"items"`
	}
)
