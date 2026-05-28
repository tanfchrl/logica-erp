package customfield

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

const adminDoctype = "custom_field_definition"

// FieldDefAdmin is the admin-facing view of custom_field_definition.
// Mirrors the table 1:1 with FE-friendly types.
type FieldDefAdmin struct {
	ID           string          `json:"id"`
	Doctype      string          `json:"doctype"`
	FieldName    string          `json:"field_name"`
	LabelID      string          `json:"label_id"`
	LabelEN      string          `json:"label_en"`
	FieldType    string          `json:"field_type"`
	IsRequired   bool            `json:"is_required"`
	DefaultValue string          `json:"default_value,omitempty"`
	Options      json.RawMessage `json:"options,omitempty"`
	Position     int             `json:"position"`
	IsIndexed    bool            `json:"is_indexed"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type FieldDefInput struct {
	Doctype      string          `json:"doctype"`
	FieldName    string          `json:"field_name" doc:"snake_case key sent to the API"`
	LabelID      string          `json:"label_id"`
	LabelEN      string          `json:"label_en"`
	FieldType    string          `json:"field_type" doc:"text | int | decimal | date | datetime | bool | select | link"`
	IsRequired   bool            `json:"is_required,omitempty"`
	DefaultValue string          `json:"default_value,omitempty"`
	Options      json.RawMessage `json:"options,omitempty" doc:"JSON object — for select use {\"values\":[\"a\",\"b\"]}, for link use {\"doctype\":\"customer\"}"`
	Position     int             `json:"position,omitempty"`
	IsIndexed    bool            `json:"is_indexed,omitempty"`
}

// AdminService — narrow CRUD on custom_field_definition. Sits next to the
// existing Validator in this package so they share the table reference.
type AdminService struct{ db *dbx.DB }

func NewAdminService(db *dbx.DB) *AdminService { return &AdminService{db: db} }

func (s *AdminService) Create(ctx context.Context, in FieldDefInput) (*FieldDefAdmin, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("custom_field_definition: unauthenticated")
	}
	if err := validateInput(in); err != nil {
		return nil, err
	}
	id := dbx.NewIDWithPrefix("cfd")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO custom_field_definition (
				id, doctype, field_name, label_id, label_en, field_type,
				is_required, default_value, options, position, is_indexed
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			id, in.Doctype, in.FieldName, in.LabelID, in.LabelEN, in.FieldType,
			in.IsRequired, nullable(in.DefaultValue), nullableJSON(in.Options),
			in.Position, in.IsIndexed); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("custom_field_definition: %q already defined on %s", in.FieldName, in.Doctype)
			}
			return err
		}
		return audit.Record(ctx, tx, adminDoctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *AdminService) Update(ctx context.Context, id string, in FieldDefInput) (*FieldDefAdmin, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("custom_field_definition: unauthenticated")
	}
	if err := validateInput(in); err != nil {
		return nil, err
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// doctype + field_name are the natural key; only allow updating
		// metadata to avoid orphaning existing payloads.
		tag, err := tx.Exec(ctx, `
			UPDATE custom_field_definition SET
			  label_id = $1, label_en = $2, field_type = $3,
			  is_required = $4, default_value = $5, options = $6,
			  position = $7, is_indexed = $8
			WHERE id = $9`,
			in.LabelID, in.LabelEN, in.FieldType,
			in.IsRequired, nullable(in.DefaultValue), nullableJSON(in.Options),
			in.Position, in.IsIndexed, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("custom_field_definition %s not found", id)
		}
		return audit.Record(ctx, tx, adminDoctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *AdminService) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("custom_field_definition: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Hard-delete is fine: the validator just won't accept the field
		// any more; existing payloads keep the value in their custom_fields
		// JSONB column (it'll surface as "unknown" on next save, prompting
		// the user to either restore the def or strip the key).
		tag, err := tx.Exec(ctx, `DELETE FROM custom_field_definition WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("custom_field_definition %s not found", id)
		}
		return audit.Record(ctx, tx, adminDoctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

func (s *AdminService) Get(ctx context.Context, id string) (*FieldDefAdmin, error) {
	var d FieldDefAdmin
	var defaultVal *string
	var options *string
	err := s.db.QueryRow(ctx, `
		SELECT id, doctype, field_name, label_id, label_en, field_type,
		       is_required, default_value, options::text, position, is_indexed,
		       created_at, updated_at
		FROM custom_field_definition WHERE id = $1`, id).
		Scan(&d.ID, &d.Doctype, &d.FieldName, &d.LabelID, &d.LabelEN, &d.FieldType,
			&d.IsRequired, &defaultVal, &options, &d.Position, &d.IsIndexed,
			&d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("custom_field_definition %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if defaultVal != nil {
		d.DefaultValue = *defaultVal
	}
	if options != nil {
		d.Options = json.RawMessage(*options)
	}
	return &d, nil
}

// List returns every definition. Optional `?doctype=X` narrows. Sorted
// by doctype then position so the admin UI groups cleanly.
func (s *AdminService) List(ctx context.Context, doctype string) ([]FieldDefAdmin, error) {
	args := []any{}
	q := `SELECT id FROM custom_field_definition`
	if doctype != "" {
		args = append(args, doctype)
		q += ` WHERE doctype = $1`
	}
	q += ` ORDER BY doctype, position, field_name`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make([]FieldDefAdmin, 0, len(ids))
	for _, id := range ids {
		d, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, nil
}

// ---- helpers ----

func validateInput(in FieldDefInput) error {
	in.Doctype = strings.TrimSpace(in.Doctype)
	in.FieldName = strings.TrimSpace(in.FieldName)
	in.LabelID = strings.TrimSpace(in.LabelID)
	in.LabelEN = strings.TrimSpace(in.LabelEN)
	if in.Doctype == "" || in.FieldName == "" || in.LabelID == "" || in.LabelEN == "" {
		return errors.New("custom_field_definition: doctype, field_name, label_id, label_en required")
	}
	// field_name must be snake_case-ish — the validator coerces by string
	// key so anything wild will work technically, but keeping a sane
	// shape prevents nonsense like spaces / dots in payloads.
	for _, r := range in.FieldName {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return fmt.Errorf("custom_field_definition.field_name: only [a-z0-9_] allowed (got %q)", in.FieldName)
		}
	}
	switch FieldType(in.FieldType) {
	case TypeText, TypeInt, TypeDecimal, TypeDate, TypeDatetime, TypeBool, TypeSelect, TypeLink, TypeTable:
	default:
		return fmt.Errorf("custom_field_definition.field_type: invalid %q", in.FieldType)
	}
	if FieldType(in.FieldType) == TypeSelect && len(in.Options) == 0 {
		return errors.New("custom_field_definition: options.values required for field_type=select")
	}
	if FieldType(in.FieldType) == TypeLink && len(in.Options) == 0 {
		return errors.New("custom_field_definition: options.doctype required for field_type=link")
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return string(j)
}

// ---- HTTP ----

type AdminHandler struct {
	Service *AdminService
}

func RegisterAdmin(api huma.API, h *AdminHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-custom-field-definitions", Method: http.MethodGet,
		Path: "/admin/custom-fields", Summary: "List custom field definitions",
		Tags: []string{"Admin / Custom Fields"},
	}, func(ctx context.Context, in *cfdListIn) (*cfdListOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		ds, err := h.Service.List(ctx, in.Doctype)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cfdListOut{Body: cfdListBody{Items: ds}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-custom-field-definition", Method: http.MethodPost,
		Path: "/admin/custom-fields", Summary: "Define a new custom field",
		Tags: []string{"Admin / Custom Fields"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *cfdCreateIn) (*cfdOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		d, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cfdOut{Body: *d}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-custom-field-definition", Method: http.MethodPut,
		Path: "/admin/custom-fields/{id}", Summary: "Update a custom field's metadata",
		Tags: []string{"Admin / Custom Fields"},
	}, func(ctx context.Context, in *cfdUpdateIn) (*cfdOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		d, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cfdOut{Body: *d}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-custom-field-definition", Method: http.MethodDelete,
		Path: "/admin/custom-fields/{id}", Summary: "Drop a custom field definition",
		Tags: []string{"Admin / Custom Fields"},
	}, func(ctx context.Context, in *cfdGetIn) (*struct{ Body map[string]string }, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

func requireSystem(ctx context.Context) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return huma.NewError(http.StatusUnauthorized, "unauthenticated")
	}
	if !p.IsSystem {
		return huma.NewError(http.StatusForbidden, "system administrators only")
	}
	return nil
}

type (
	cfdCreateIn struct{ Body FieldDefInput }
	cfdUpdateIn struct {
		ID   string `path:"id"`
		Body FieldDefInput
	}
	cfdGetIn struct {
		ID string `path:"id"`
	}
	cfdListIn struct {
		Doctype string `query:"doctype"`
	}
	cfdOut     struct{ Body FieldDefAdmin }
	cfdListOut struct{ Body cfdListBody }
	cfdListBody struct {
		Items []FieldDefAdmin `json:"items"`
	}
)
