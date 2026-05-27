// Package notifrules holds notification rule definitions. The engine that
// reads these rules and dispatches in-app / email / WhatsApp messages is a
// downstream piece; this package is the storage + admin API.
package notifrules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "notification_rule"

// Same event catalog as the email package — duplicated here on purpose so
// notifrules doesn't pull in the email package (which would tangle the
// dependency graph). Sync by hand when adding events.
var EventCatalog = []NotifEventDef{
	{Key: "invoice.issued",           Label: "Sales invoice issued"},
	{Key: "invoice.overdue",          Label: "Sales invoice overdue"},
	{Key: "invoice.payment_received", Label: "Payment received"},
	{Key: "po.sent",                  Label: "Purchase order sent"},
	{Key: "approval.requested",       Label: "Approval requested"},
	{Key: "approval.decided",         Label: "Approval decided"},
}

type NotifEventDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type Rule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	EventKey        string    `json:"event_key"`
	CompanyID       string    `json:"company_id,omitempty"`
	IsActive        bool      `json:"is_active"`
	Recipients      []string  `json:"recipients"` // user:<id> | role:<id>
	Channels        []string  `json:"channels"`   // in_app | email | whatsapp
	ConditionField  string    `json:"condition_field,omitempty"`
	ConditionOp     string    `json:"condition_op,omitempty"`
	ConditionValue  string    `json:"condition_value,omitempty"`
	Description     string    `json:"description,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type RuleInput struct {
	Name            string   `json:"name"`
	EventKey        string   `json:"event_key"`
	CompanyID       string   `json:"company_id,omitempty"`
	IsActive        *bool    `json:"is_active,omitempty"`
	Recipients      []string `json:"recipients"`
	Channels        []string `json:"channels,omitempty"`
	ConditionField  string   `json:"condition_field,omitempty"`
	ConditionOp     string   `json:"condition_op,omitempty"`
	ConditionValue  string   `json:"condition_value,omitempty"`
	Description     string   `json:"description,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) List(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, event_key, coalesce(company_id,''), is_active,
		       recipients, channels,
		       coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,''),
		       description, updated_at
		FROM notification_rule ORDER BY event_key, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Rule, 0)
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Name, &r.EventKey, &r.CompanyID, &r.IsActive,
			&r.Recipients, &r.Channels,
			&r.ConditionField, &r.ConditionOp, &r.ConditionValue,
			&r.Description, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) Upsert(ctx context.Context, id string, in RuleInput) (*Rule, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("notification_rule: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.EventKey = strings.TrimSpace(in.EventKey)
	if in.Name == "" || in.EventKey == "" {
		return nil, errors.New("notification_rule: name and event_key required")
	}
	if len(in.Recipients) == 0 {
		return nil, errors.New("notification_rule: at least one recipient required")
	}
	for _, r := range in.Recipients {
		if !strings.HasPrefix(r, "user:") && !strings.HasPrefix(r, "role:") {
			return nil, fmt.Errorf("recipient %q must be 'user:<id>' or 'role:<id>'", r)
		}
	}
	channels := in.Channels
	if len(channels) == 0 {
		channels = []string{"in_app"}
	}
	for _, c := range channels {
		switch c {
		case "in_app", "email", "whatsapp":
		default:
			return nil, fmt.Errorf("channel %q invalid (in_app|email|whatsapp)", c)
		}
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}
	if id == "" {
		id = dbx.NewIDWithPrefix("notr")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO notification_rule (id, name, event_key, company_id, is_active,
		                               recipients, channels,
		                               condition_field, condition_op, condition_value,
		                               description, created_by, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,now())
		ON CONFLICT (id) DO UPDATE SET
		  name = EXCLUDED.name, event_key = EXCLUDED.event_key, company_id = EXCLUDED.company_id,
		  is_active = EXCLUDED.is_active, recipients = EXCLUDED.recipients, channels = EXCLUDED.channels,
		  condition_field = EXCLUDED.condition_field, condition_op = EXCLUDED.condition_op,
		  condition_value = EXCLUDED.condition_value, description = EXCLUDED.description,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		id, in.Name, in.EventKey, nullable(in.CompanyID), active,
		in.Recipients, channels,
		nullable(in.ConditionField), nullable(in.ConditionOp), nullable(in.ConditionValue),
		in.Description, p.UserID)
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM notification_rule WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("notification_rule: not found")
	}
	return nil
}

func (s *Service) get(ctx context.Context, id string) (*Rule, error) {
	var r Rule
	err := s.db.QueryRow(ctx, `
		SELECT id, name, event_key, coalesce(company_id,''), is_active,
		       recipients, channels,
		       coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,''),
		       description, updated_at
		FROM notification_rule WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.EventKey, &r.CompanyID, &r.IsActive,
			&r.Recipients, &r.Channels,
			&r.ConditionField, &r.ConditionOp, &r.ConditionValue,
			&r.Description, &r.UpdatedAt)
	return &r, err
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
		OperationID: "list-notification-events", Method: http.MethodGet,
		Path: "/admin/notification-rules/events", Summary: "Events you can write rules against",
		Tags: []string{"Admin / Notifications"},
	}, func(ctx context.Context, _ *struct{}) (*nrEvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &nrEvOut{Body: nrEvBody{Items: EventCatalog}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-notification-rules", Method: http.MethodGet,
		Path: "/admin/notification-rules", Summary: "List notification rules",
		Tags: []string{"Admin / Notifications"},
	}, func(ctx context.Context, _ *struct{}) (*nrListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		rs, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nrListOut{Body: nrListBody{Items: rs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-notification-rule", Method: http.MethodPost,
		Path: "/admin/notification-rules", Summary: "Create a rule",
		Tags: []string{"Admin / Notifications"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *nrCreateIn) (*nrItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.Upsert(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nrItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-notification-rule", Method: http.MethodPut,
		Path: "/admin/notification-rules/{id}", Summary: "Update a rule",
		Tags: []string{"Admin / Notifications"},
	}, func(ctx context.Context, in *nrUpdateIn) (*nrItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.Upsert(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &nrItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-notification-rule", Method: http.MethodDelete,
		Path: "/admin/notification-rules/{id}", Summary: "Delete a rule",
		Tags: []string{"Admin / Notifications"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *nrByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})
}

type (
	nrListOut  struct{ Body nrListBody }
	nrListBody struct {
		Items []Rule `json:"items"`
	}
	nrItemOut  struct{ Body Rule }
	nrByID     struct {
		ID string `path:"id"`
	}
	nrCreateIn struct{ Body RuleInput }
	nrUpdateIn struct {
		ID   string `path:"id"`
		Body RuleInput
	}
	nrEvOut  struct{ Body nrEvBody }
	nrEvBody struct {
		Items []NotifEventDef `json:"items"`
	}
)
