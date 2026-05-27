// Package lead implements the Lead doctype with a Convert action that creates
// a Customer and flags the lead as Converted.
package lead

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

const Doctype = "lead"

type Lead struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	LeadName            string    `json:"lead_name"`
	ContactEmail        string    `json:"contact_email,omitempty"`
	ContactPhone        string    `json:"contact_phone,omitempty"`
	Source              string    `json:"source,omitempty"`
	Status              string    `json:"status"`
	TerritoryID         string    `json:"territory_id,omitempty"`
	ConvertedCustomerID string    `json:"converted_customer_id,omitempty"`
	Remarks             string    `json:"remarks,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type LeadCreateInput struct {
	LeadName     string `json:"lead_name"`
	ContactEmail string `json:"contact_email,omitempty"`
	ContactPhone string `json:"contact_phone,omitempty"`
	Source       string `json:"source,omitempty"`
	TerritoryID  string `json:"territory_id,omitempty"`
	Remarks      string `json:"remarks,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in LeadCreateInput) (*Lead, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("lead: unauthenticated")
	}
	in.LeadName = strings.TrimSpace(in.LeadName)
	if in.LeadName == "" {
		return nil, errors.New("lead.lead_name: required")
	}
	id := dbx.NewIDWithPrefix("lead")
	var l Lead
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickNamingSeries(ctx, tx, Doctype, "")
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO lead (id, name, lead_name, contact_email, contact_phone, source, territory_id, remarks, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			RETURNING id, name, lead_name, coalesce(contact_email,''), coalesce(contact_phone,''),
			          coalesce(source,''), status, coalesce(territory_id,''), coalesce(converted_customer_id,''),
			          coalesce(remarks,''), created_at, updated_at`,
			id, name, in.LeadName, nullable(in.ContactEmail), nullable(in.ContactPhone),
			nullable(in.Source), nullable(in.TerritoryID), nullable(in.Remarks), p.UserID).
			Scan(&l.ID, &l.Name, &l.LeadName, &l.ContactEmail, &l.ContactPhone,
				&l.Source, &l.Status, &l.TerritoryID, &l.ConvertedCustomerID, &l.Remarks, &l.CreatedAt, &l.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, l.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &l, err
}

// Convert turns a Lead into a Customer. Returns the new customer id and updates the lead.
func (s *Service) Convert(ctx context.Context, leadID string) (customerID string, err error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return "", errors.New("lead: unauthenticated")
	}
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var (
			leadName, contactEmail, contactPhone string
			converted                            *string
		)
		err := tx.QueryRow(ctx, `
			SELECT lead_name, coalesce(contact_email,''), coalesce(contact_phone,''), converted_customer_id
			FROM lead WHERE id = $1 AND is_deleted = false`, leadID).
			Scan(&leadName, &contactEmail, &contactPhone, &converted)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lead %s not found", leadID)
		}
		if err != nil {
			return err
		}
		if converted != nil && *converted != "" {
			return errors.New("lead is already converted")
		}
		custID := dbx.NewIDWithPrefix("cust")
		// Generate a deterministic-ish customer code from the lead name.
		code := slugify(leadName)
		if code == "" {
			code = "CUST-" + leadID[len(leadID)-6:]
		}
		// Avoid collisions.
		for tries := 0; tries < 5; tries++ {
			var exists int
			if err := tx.QueryRow(ctx, `SELECT 1 FROM customer WHERE name = $1`, code).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
				break
			}
			code = code + "-" + leadID[len(leadID)-4:]
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO customer (id, name, display_name, default_currency, email, phone, created_by, updated_by)
			VALUES ($1,$2,$3,'IDR',$4,$5,$6,$6)`,
			custID, code, leadName, nullable(contactEmail), nullable(contactPhone), p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE lead SET status = 'Converted', converted_customer_id = $1, updated_by = $2
			WHERE id = $3`, custID, p.UserID, leadID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, leadID, p.UserID, audit.ActionUpdate, audit.Diff{After: map[string]string{"converted_customer_id": custID}}); err != nil {
			return err
		}
		customerID = custID
		return nil
	})
	return customerID, err
}

func (s *Service) Get(ctx context.Context, id string) (*Lead, error) {
	var l Lead
	err := s.db.QueryRow(ctx, `
		SELECT id, name, lead_name, coalesce(contact_email,''), coalesce(contact_phone,''),
		       coalesce(source,''), status, coalesce(territory_id,''), coalesce(converted_customer_id,''),
		       coalesce(remarks,''), created_at, updated_at
		FROM lead WHERE id = $1 AND is_deleted = false`, id).
		Scan(&l.ID, &l.Name, &l.LeadName, &l.ContactEmail, &l.ContactPhone,
			&l.Source, &l.Status, &l.TerritoryID, &l.ConvertedCustomerID, &l.Remarks, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lead %s not found", id)
	}
	return &l, err
}

func (s *Service) List(ctx context.Context) ([]Lead, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, lead_name, coalesce(contact_email,''), coalesce(contact_phone,''),
		       coalesce(source,''), status, coalesce(territory_id,''), coalesce(converted_customer_id,''),
		       coalesce(remarks,''), created_at, updated_at
		FROM lead WHERE is_deleted = false ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lead
	for rows.Next() {
		var l Lead
		if err := rows.Scan(&l.ID, &l.Name, &l.LeadName, &l.ContactEmail, &l.ContactPhone,
			&l.Source, &l.Status, &l.TerritoryID, &l.ConvertedCustomerID, &l.Remarks, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---- helpers ----

func slugify(s string) string {
	var b strings.Builder
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c)
			prev = c
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - 32)
			prev = c - 32
		case c >= '0' && c <= '9':
			b.WriteByte(c)
			prev = c
		default:
			if prev != '-' && b.Len() > 0 {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func pickNamingSeries(ctx context.Context, tx pgx.Tx, doctype, _ string) (string, string, error) {
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

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-leads", Method: http.MethodGet,
		Path: "/crm/leads", Summary: "List leads", Tags: []string{"CRM / Lead"},
	}, func(ctx context.Context, _ *struct{}) (*leadListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ls, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &leadListOut{Body: leadListBody{Items: ls}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-lead", Method: http.MethodPost,
		Path: "/crm/leads", Summary: "Create a lead",
		Tags: []string{"CRM / Lead"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *leadCreateIn) (*leadOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &leadOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-lead", Method: http.MethodGet,
		Path: "/crm/leads/{id}", Summary: "Get a lead", Tags: []string{"CRM / Lead"},
	}, func(ctx context.Context, in *leadGetIn) (*leadOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &leadOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "convert-lead", Method: http.MethodPost,
		Path: "/crm/leads/{id}/convert", Summary: "Convert a lead to a customer",
		Tags: []string{"CRM / Lead"},
	}, func(ctx context.Context, in *leadGetIn) (*leadConvertOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		cid, err := h.Service.Convert(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &leadConvertOut{Body: leadConvertBody{CustomerID: cid}}, nil
	})
}

type (
	leadCreateIn struct{ Body LeadCreateInput }
	leadOut      struct{ Body Lead }
	leadListOut  struct{ Body leadListBody }
	leadListBody struct {
		Items []Lead `json:"items"`
	}
	leadGetIn struct {
		ID string `path:"id"`
	}
	leadConvertOut struct{ Body leadConvertBody }
	leadConvertBody struct {
		CustomerID string `json:"customer_id"`
	}
)
