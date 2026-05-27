// Package webhooks implements outbound HTTP webhook subscriptions with
// HMAC-SHA256 signing and a per-attempt delivery log.
//
// Wire path:
//   service.Fire(ctx, "invoice.submitted", payload) →
//     selects matching subscriptions → posts payload synchronously with
//     signed headers → writes a webhook_delivery row per attempt.
//
// For most ERPs synchronous delivery is fine; this can be promoted to a
// background River job later by swapping Fire to enqueue instead of POST.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "webhook_subscription"

// EventCatalog — events the rest of the system can fire. Surfaced to the UI
// so admins know which keys are valid when subscribing.
var EventCatalog = []WebhookEventDef{
	{Key: "invoice.submitted",        Label: "Sales invoice submitted"},
	{Key: "invoice.cancelled",        Label: "Sales invoice cancelled"},
	{Key: "purchase_invoice.submitted", Label: "Purchase invoice submitted"},
	{Key: "payment.received",         Label: "Payment received"},
	{Key: "payment.made",             Label: "Payment made"},
	{Key: "journal_entry.submitted",  Label: "Journal entry submitted"},
	{Key: "approval.requested",       Label: "Approval requested"},
	{Key: "approval.decided",         Label: "Approval decided"},
	{Key: "user.invited",             Label: "User invited"},
}

type WebhookEventDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ---- Types ------------------------------------------------------------------

type Subscription struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	HasSecret  bool      `json:"has_secret"`
	Events     []string  `json:"events"`
	IsEnabled  bool      `json:"is_enabled"`
	RetryMax   int       `json:"retry_max"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type SubscriptionInput struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Secret     *string  `json:"secret,omitempty" doc:"Omit to keep existing; empty string to regenerate"`
	Events     []string `json:"events"`
	IsEnabled  *bool    `json:"is_enabled,omitempty"`
	RetryMax   int      `json:"retry_max,omitempty"`
}

type Delivery struct {
	ID             string    `json:"id"`
	SubscriptionID string    `json:"subscription_id"`
	Event          string    `json:"event"`
	Attempt        int       `json:"attempt"`
	Status         string    `json:"status"`
	ResponseCode   int       `json:"response_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
}

// ---- Service ----------------------------------------------------------------

type Service struct {
	db     *dbx.DB
	client *http.Client
}

func NewService(db *dbx.DB) *Service {
	return &Service{db: db, client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Service) List(ctx context.Context) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, url, (secret <> ''), events, is_enabled, retry_max, created_at, updated_at
		FROM webhook_subscription ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Subscription, 0)
	for rows.Next() {
		var sb Subscription
		if err := rows.Scan(&sb.ID, &sb.Name, &sb.URL, &sb.HasSecret, &sb.Events, &sb.IsEnabled, &sb.RetryMax, &sb.CreatedAt, &sb.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

func (s *Service) Upsert(ctx context.Context, id string, in SubscriptionInput) (*Subscription, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("webhook: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	if in.Name == "" || in.URL == "" {
		return nil, errors.New("webhook: name and url required")
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return nil, errors.New("webhook.url: must start with http(s)://")
	}
	retry := in.RetryMax
	if retry <= 0 {
		retry = 5
	}
	enabled := true
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}

	if id == "" {
		id = dbx.NewIDWithPrefix("wh")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Resolve secret: keep existing unless input supplies one. Empty string = regen.
		var secret string
		if err := tx.QueryRow(ctx, `SELECT coalesce(secret,'') FROM webhook_subscription WHERE id = $1`, id).Scan(&secret); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.Secret != nil {
			if *in.Secret == "" {
				secret = randomSecret()
			} else {
				secret = *in.Secret
			}
		}
		if secret == "" {
			secret = randomSecret()
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO webhook_subscription (id, name, url, secret, events, is_enabled, retry_max, created_by, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,now())
			ON CONFLICT (id) DO UPDATE SET
			  name = EXCLUDED.name, url = EXCLUDED.url, secret = EXCLUDED.secret,
			  events = EXCLUDED.events, is_enabled = EXCLUDED.is_enabled, retry_max = EXCLUDED.retry_max,
			  updated_by = EXCLUDED.updated_by, updated_at = now()`,
			id, in.Name, in.URL, secret, in.Events, enabled, retry, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM webhook_subscription WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("webhook: not found")
	}
	return nil
}

func (s *Service) get(ctx context.Context, id string) (*Subscription, error) {
	var sb Subscription
	err := s.db.QueryRow(ctx, `
		SELECT id, name, url, (secret <> ''), events, is_enabled, retry_max, created_at, updated_at
		FROM webhook_subscription WHERE id = $1`, id).
		Scan(&sb.ID, &sb.Name, &sb.URL, &sb.HasSecret, &sb.Events, &sb.IsEnabled, &sb.RetryMax, &sb.CreatedAt, &sb.UpdatedAt)
	return &sb, err
}

func (s *Service) RecentDeliveries(ctx context.Context, subID string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, subscription_id, event, attempt, status, coalesce(response_code,0), error_message, created_at, coalesce(delivered_at, 'epoch'::timestamptz)
	      FROM webhook_delivery`
	args := []any{limit}
	if subID != "" {
		q += " WHERE subscription_id = $2"
		args = append(args, subID)
	}
	q += " ORDER BY created_at DESC LIMIT $1"
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Delivery, 0)
	for rows.Next() {
		var d Delivery
		var delivered time.Time
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.Event, &d.Attempt, &d.Status, &d.ResponseCode, &d.ErrorMessage, &d.CreatedAt, &delivered); err != nil {
			return nil, err
		}
		if !delivered.IsZero() && delivered.Year() > 1970 {
			d.DeliveredAt = delivered
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Fire dispatches an event to every enabled subscription that listens for it.
// Synchronous; each subscription gets at most one attempt per call. Failed
// deliveries can be re-fired via Replay (or the UI replay button).
func (s *Service) Fire(ctx context.Context, event string, payload any) error {
	rows, err := s.db.Query(ctx, `
		SELECT id, url, secret FROM webhook_subscription
		WHERE is_enabled = true AND $1 = ANY(events)`, event)
	if err != nil {
		return err
	}
	defer rows.Close()
	type sub struct{ id, url, secret string }
	var subs []sub
	for rows.Next() {
		var x sub
		if err := rows.Scan(&x.id, &x.url, &x.secret); err != nil {
			return err
		}
		subs = append(subs, x)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for _, x := range subs {
		s.deliver(ctx, x.id, x.url, x.secret, event, body, 1)
	}
	return nil
}

// Replay re-sends a past delivery (looks up the original payload + subscription).
func (s *Service) Replay(ctx context.Context, deliveryID string) (*Delivery, error) {
	var (
		subID, event string
		payload      []byte
		url, secret  string
		attempt      int
	)
	if err := s.db.QueryRow(ctx, `
		SELECT d.subscription_id, d.event, d.payload::text, d.attempt, s.url, s.secret
		FROM webhook_delivery d JOIN webhook_subscription s ON s.id = d.subscription_id
		WHERE d.id = $1`, deliveryID).Scan(&subID, &event, &payload, &attempt, &url, &secret); err != nil {
		return nil, err
	}
	s.deliver(ctx, subID, url, secret, event, payload, attempt+1)
	// Return the newest delivery row for that subscription/event.
	rows, err := s.RecentDeliveries(ctx, subID, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Service) deliver(ctx context.Context, subID, url, secret, event string, body []byte, attempt int) {
	signature := signHMAC(secret, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.recordDelivery(ctx, subID, event, body, attempt, "failed", 0, "", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Logica-ERP/0.1 webhooks")
	req.Header.Set("X-Logica-Event", event)
	req.Header.Set("X-Logica-Signature", "sha256="+signature)
	req.Header.Set("X-Logica-Delivery-Attempt", fmt.Sprintf("%d", attempt))

	resp, err := s.client.Do(req)
	if err != nil {
		s.recordDelivery(ctx, subID, event, body, attempt, "failed", 0, "", err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	status := "failed"
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		status = "succeeded"
	}
	s.recordDelivery(ctx, subID, event, body, attempt, status, resp.StatusCode, string(respBody), "")
}

func (s *Service) recordDelivery(ctx context.Context, subID, event string, payload []byte, attempt int, status string, code int, respBody, errMsg string) {
	var codeArg any = nil
	if code > 0 {
		codeArg = code
	}
	var delivered any = time.Now().UTC()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO webhook_delivery (id, subscription_id, event, payload, attempt, status, response_code, response_body, error_message, delivered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		dbx.NewIDWithPrefix("whdel"), subID, event, payload, attempt, status, codeArg, respBody, errMsg, delivered)
}

func signHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "whsec_" + hex.EncodeToString(b)
}

// ---- HTTP -------------------------------------------------------------------

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-webhook-events", Method: http.MethodGet,
		Path: "/admin/webhooks/events", Summary: "List events you can subscribe to",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, _ *struct{}) (*whkEvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &whkEvOut{Body: whkEvBody{Items: EventCatalog}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-webhooks", Method: http.MethodGet,
		Path: "/admin/webhooks", Summary: "List webhook subscriptions",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, _ *struct{}) (*whkListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ss, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whkListOut{Body: whkListBody{Items: ss}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-webhook", Method: http.MethodPost,
		Path: "/admin/webhooks", Summary: "Create a webhook subscription",
		Tags: []string{"Admin / Webhooks"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *whkCreateIn) (*whkItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		s, err := h.Service.Upsert(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whkItemOut{Body: *s}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-webhook", Method: http.MethodPut,
		Path: "/admin/webhooks/{id}", Summary: "Update a webhook subscription",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, in *whkUpdateIn) (*whkItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		s, err := h.Service.Upsert(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whkItemOut{Body: *s}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-webhook", Method: http.MethodDelete,
		Path: "/admin/webhooks/{id}", Summary: "Delete a webhook subscription",
		Tags: []string{"Admin / Webhooks"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *whkByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-webhook-deliveries", Method: http.MethodGet,
		Path: "/admin/webhooks/deliveries", Summary: "Recent webhook delivery attempts",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, in *whkDelListIn) (*whkDelListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ds, err := h.Service.RecentDeliveries(ctx, in.SubscriptionID, in.Limit)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whkDelListOut{Body: whkDelListBody{Items: ds}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "replay-webhook-delivery", Method: http.MethodPost,
		Path: "/admin/webhooks/deliveries/{id}/replay", Summary: "Re-send a past delivery",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, in *whkByID) (*whkDelItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		d, err := h.Service.Replay(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		if d == nil {
			return &whkDelItemOut{}, nil
		}
		return &whkDelItemOut{Body: *d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-webhook", Method: http.MethodPost,
		Path: "/admin/webhooks/{id}/test", Summary: "Fire a synthetic 'webhook.test' payload",
		Tags: []string{"Admin / Webhooks"},
	}, func(ctx context.Context, in *whkByID) (*whkDelItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		// Re-use deliver() directly so we don't need an "is enabled" check.
		var url, secret string
		if err := h.Service.db.QueryRow(ctx, `SELECT url, secret FROM webhook_subscription WHERE id = $1`, in.ID).Scan(&url, &secret); err != nil {
			return nil, httpx.MapError(err)
		}
		body, _ := json.Marshal(map[string]any{
			"event": "webhook.test", "fired_at": time.Now().UTC(), "message": "Hello from Logica ERP",
		})
		h.Service.deliver(ctx, in.ID, url, secret, "webhook.test", body, 1)
		rows, _ := h.Service.RecentDeliveries(ctx, in.ID, 1)
		if len(rows) > 0 {
			return &whkDelItemOut{Body: rows[0]}, nil
		}
		return &whkDelItemOut{}, nil
	})
}

type (
	whkListOut  struct{ Body whkListBody }
	whkListBody struct {
		Items []Subscription `json:"items"`
	}
	whkItemOut  struct{ Body Subscription }
	whkByID     struct {
		ID string `path:"id"`
	}
	whkCreateIn struct{ Body SubscriptionInput }
	whkUpdateIn struct {
		ID   string `path:"id"`
		Body SubscriptionInput
	}
	whkEvOut  struct{ Body whkEvBody }
	whkEvBody struct {
		Items []WebhookEventDef `json:"items"`
	}
	whkDelListIn struct {
		SubscriptionID string `query:"subscription_id"`
		Limit          int    `query:"limit"`
	}
	whkDelListOut  struct{ Body whkDelListBody }
	whkDelListBody struct {
		Items []Delivery `json:"items"`
	}
	whkDelItemOut struct{ Body Delivery }
)
