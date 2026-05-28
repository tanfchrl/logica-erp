// Package communication is the activity-log doctype — calls / meetings /
// emails / WA messages a salesperson manually logs against a record. Twenty
// calls these "Activities"; we keep the more concrete name.
//
// SMTP outbox / WA Business API integrations come later; the column shape
// already accommodates a `source` field other than 'manual' for that.
package communication

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

const Doctype = "communication"

const (
	KindEmail    = "email"
	KindSMS      = "sms"
	KindPhone    = "phone"
	KindMeeting  = "meeting"
	KindWhatsApp = "whatsapp"
)

const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// parentAllowlist mirrors the FE TASKS_ALLOWED — broader than contacts
// because anything threadable should be loggable.
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

type Communication struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	ParentDoctype string    `json:"parent_doctype"`
	ParentID      string    `json:"parent_id"`
	Kind          string    `json:"kind"`
	Direction     string    `json:"direction"`
	Subject       string    `json:"subject"`
	Body          string    `json:"body,omitempty"`
	WithContact   string    `json:"with_contact,omitempty"`
	SentAt        time.Time `json:"sent_at"`
	Source        string    `json:"source"`
	IsDeleted     bool      `json:"is_deleted"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by"`
}

type CommunicationInput struct {
	ParentDoctype string `json:"parent_doctype"`
	ParentID      string `json:"parent_id"`
	Kind          string `json:"kind" doc:"email | sms | phone | meeting | whatsapp"`
	Direction     string `json:"direction,omitempty" doc:"in | out (default out)"`
	Subject       string `json:"subject"`
	Body          string `json:"body,omitempty"`
	WithContact   string `json:"with_contact,omitempty" doc:"name / email / phone of the other party"`
	SentAt        string `json:"sent_at,omitempty" doc:"RFC3339; defaults to now"`
	CompanyID     string `json:"company_id,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in CommunicationInput) (*Communication, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("communication: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("communication.company_id: required")
	}
	if !parentAllowlist[in.ParentDoctype] {
		return nil, fmt.Errorf("communication.parent_doctype: %q not allowed", in.ParentDoctype)
	}
	if strings.TrimSpace(in.ParentID) == "" {
		return nil, errors.New("communication.parent_id: required")
	}
	switch in.Kind {
	case KindEmail, KindSMS, KindPhone, KindMeeting, KindWhatsApp:
	default:
		return nil, fmt.Errorf("communication.kind: invalid %q", in.Kind)
	}
	dir := in.Direction
	if dir == "" {
		dir = DirectionOut
	}
	if dir != DirectionIn && dir != DirectionOut {
		return nil, fmt.Errorf("communication.direction: invalid %q", dir)
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("communication.subject: required")
	}
	sentAt := time.Now().UTC()
	if in.SentAt != "" {
		t, err := time.Parse(time.RFC3339, in.SentAt)
		if err != nil {
			return nil, fmt.Errorf("communication.sent_at: %w", err)
		}
		sentAt = t
	}

	id := dbx.NewIDWithPrefix("comm")
	var out Communication
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO communication (
				id, company_id, parent_doctype, parent_id,
				kind, direction, subject, body, with_contact,
				sent_at, source, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`,
			id, in.CompanyID, in.ParentDoctype, in.ParentID,
			in.Kind, dir, in.Subject, nullable(in.Body), nullable(in.WithContact),
			sentAt, "manual", p.UserID); err != nil {
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

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("communication: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Author-only delete — same pattern as notes.
		var creator string
		if err := tx.QueryRow(ctx,
			`SELECT created_by FROM communication WHERE id = $1 AND is_deleted = false`, id).Scan(&creator); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("communication %s not found", id)
			}
			return err
		}
		if creator != p.UserID && !p.IsSystem {
			return errors.New("communication: only the author can delete")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE communication SET is_deleted = true, updated_by = $1 WHERE id = $2`, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

func (s *Service) Get(ctx context.Context, id string) (*Communication, error) {
	var out *Communication
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

func (s *Service) List(ctx context.Context, companyID, parentDoctype, parentID string) ([]Communication, error) {
	args := []any{companyID}
	q := `SELECT id, company_id, parent_doctype, parent_id, kind, direction,
	             subject, coalesce(body, ''), coalesce(with_contact, ''),
	             sent_at, source, is_deleted, created_at, updated_at, created_by
	      FROM communication
	      WHERE company_id = $1 AND is_deleted = false`
	if parentDoctype != "" && parentID != "" {
		q += ` AND parent_doctype = $2 AND parent_id = $3`
		args = append(args, parentDoctype, parentID)
	}
	q += ` ORDER BY sent_at DESC LIMIT 200`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Communication
	for rows.Next() {
		var c Communication
		if err := rows.Scan(&c.ID, &c.CompanyID, &c.ParentDoctype, &c.ParentID, &c.Kind, &c.Direction,
			&c.Subject, &c.Body, &c.WithContact,
			&c.SentAt, &c.Source, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt, &c.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func load(ctx context.Context, tx pgx.Tx, id string) (*Communication, error) {
	var c Communication
	err := tx.QueryRow(ctx, `
		SELECT id, company_id, parent_doctype, parent_id, kind, direction,
		       subject, coalesce(body, ''), coalesce(with_contact, ''),
		       sent_at, source, is_deleted, created_at, updated_at, created_by
		FROM communication WHERE id = $1`, id).
		Scan(&c.ID, &c.CompanyID, &c.ParentDoctype, &c.ParentID, &c.Kind, &c.Direction,
			&c.Subject, &c.Body, &c.WithContact,
			&c.SentAt, &c.Source, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt, &c.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communication %s not found", id)
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
		OperationID: "list-communications", Method: http.MethodGet,
		Path: "/crm/communications", Summary: "List communications (optionally narrowed to one parent)",
		Tags: []string{"CRM / Communication"},
	}, func(ctx context.Context, in *commListIn) (*commListOut, error) {
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
		return &commListOut{Body: commListBody{Items: cs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-communication", Method: http.MethodPost,
		Path: "/crm/communications", Summary: "Log a communication against a record",
		Tags: []string{"CRM / Communication"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *commCreateIn) (*commOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &commOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-communication", Method: http.MethodGet,
		Path: "/crm/communications/{id}", Summary: "Get a communication",
		Tags: []string{"CRM / Communication"},
	}, func(ctx context.Context, in *commGetIn) (*commOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &commOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-communication", Method: http.MethodDelete,
		Path: "/crm/communications/{id}", Summary: "Soft-delete a communication (author-only)",
		Tags: []string{"CRM / Communication"},
	}, func(ctx context.Context, in *commGetIn) (*struct{ Body map[string]string }, error) {
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
	commCreateIn struct{ Body CommunicationInput }
	commGetIn    struct {
		ID string `path:"id"`
	}
	commListIn struct {
		ParentDoctype string `query:"parent_doctype"`
		ParentID      string `query:"parent_id"`
	}
	commOut     struct{ Body Communication }
	commListOut struct{ Body commListBody }
	commListBody struct {
		Items []Communication `json:"items"`
	}
)
