// dispatcher.go — runtime side of notification_rule. Services call
// Dispatcher.Fire(eventKey, payload) when something noteworthy happens;
// dispatch runs ASYNC in a background goroutine so the firing request
// completes immediately regardless of SMTP latency or rule count.
//
// Channel implementations:
//   in_app     — inserts a row into the existing `notification` table
//   email      — calls email.Service.SendTemplated(eventKey, recipient, vars)
//   whatsapp   — logged-only for now; no Meta/Wablas transport wired yet
package notifrules

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// EmailSender — the minimal contract the dispatcher needs from the email
// package. Defined locally so notifrules doesn't import email (which would
// create a heavy dependency cycle).
type EmailSender interface {
	SendTemplated(ctx context.Context, eventKey, to string, vars map[string]any) (string, error)
}

type Dispatcher struct {
	db    *dbx.DB
	email EmailSender // optional; nil = email channel skipped
	log   *slog.Logger
}

func NewDispatcher(db *dbx.DB, email EmailSender) *Dispatcher {
	return &Dispatcher{
		db: db, email: email,
		log: slog.With("component", "notifrules.dispatcher"),
	}
}

// Fire schedules a dispatch of `eventKey` with `payload`. Returns immediately.
// Caller's context isn't reused — a fresh background context is used so dispatch
// survives request completion.
//
// Payload shape: arbitrary map. Common keys used by the dispatcher:
//
//	"company_id"        — scopes rule lookup (optional; nil-company rules also match)
//	"doctype"           — for in-app notification deep-link
//	"document_id"       — for in-app notification deep-link
//	"document_name"     — used in default subject (e.g. "SI-2026-00042")
//	"summary"           — used in default in-app body
//	<condition_field>   — any field referenced by a rule's condition (e.g. "grand_total")
func (d *Dispatcher) Fire(eventKey string, payload map[string]any) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.dispatch(ctx, eventKey, payload); err != nil {
			d.log.Error("dispatch", "event", eventKey, "err", err)
		}
	}()
}

// dispatch runs the actual fan-out. Exported for tests; production callers use Fire.
func (d *Dispatcher) dispatch(ctx context.Context, eventKey string, payload map[string]any) error {
	companyID, _ := payload["company_id"].(string)

	rules, err := d.loadActive(ctx, eventKey, companyID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}

	subject, body := defaultMessage(eventKey, payload)

	var (
		linkDoctype = strOrEmpty(payload["doctype"])
		linkDocID   = strOrEmpty(payload["document_id"])
	)

	for _, r := range rules {
		if !ruleMatches(r, payload) {
			continue
		}
		users, err := d.expandRecipients(ctx, r.Recipients)
		if err != nil {
			d.log.Warn("recipient expand", "rule", r.ID, "err", err)
			continue
		}
		for _, ch := range r.Channels {
			for _, u := range users {
				switch ch {
				case "in_app":
					if err := d.deliverInApp(ctx, u.id, subject, body, linkDoctype, linkDocID); err != nil {
						d.log.Warn("in_app", "user", u.id, "err", err)
					}
				case "email":
					if d.email == nil {
						d.log.Debug("email skipped — no transport wired")
						continue
					}
					vars := mergedVars(payload, u)
					if _, err := d.email.SendTemplated(ctx, eventKey, u.email, vars); err != nil {
						d.log.Warn("email", "user", u.email, "err", err)
					}
				case "whatsapp":
					d.log.Info("whatsapp skipped — transport unwired",
						"user", u.email, "event", eventKey)
				}
			}
		}
	}
	return nil
}

// ---- internals ----

type loadedRule struct {
	ID             string
	Recipients     []string
	Channels       []string
	ConditionField string
	ConditionOp    string
	ConditionValue string
}

type userRow struct {
	id    string
	email string
	name  string
}

func (d *Dispatcher) loadActive(ctx context.Context, eventKey, companyID string) ([]loadedRule, error) {
	rows, err := d.db.Query(ctx, `
		SELECT id, recipients, channels,
		       coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,'')
		FROM notification_rule
		WHERE is_active = true AND event_key = $1
		  AND (company_id IS NULL OR company_id = $2)`,
		eventKey, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []loadedRule{}
	for rows.Next() {
		var r loadedRule
		if err := rows.Scan(&r.ID, &r.Recipients, &r.Channels,
			&r.ConditionField, &r.ConditionOp, &r.ConditionValue); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// expandRecipients turns "user:<id>" / "role:<id>" tokens into a deduped
// list of enabled users.
func (d *Dispatcher) expandRecipients(ctx context.Context, recipients []string) ([]userRow, error) {
	var userIDs, roleIDs []string
	for _, r := range recipients {
		switch {
		case strings.HasPrefix(r, "user:"):
			userIDs = append(userIDs, strings.TrimPrefix(r, "user:"))
		case strings.HasPrefix(r, "role:"):
			roleIDs = append(roleIDs, strings.TrimPrefix(r, "role:"))
		}
	}

	seen := map[string]userRow{}

	if len(userIDs) > 0 {
		rows, err := d.db.Query(ctx, `
			SELECT id, email, coalesce(full_name,'') FROM users
			WHERE enabled = true AND id = ANY($1)`, userIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var u userRow
			if err := rows.Scan(&u.id, &u.email, &u.name); err != nil {
				rows.Close()
				return nil, err
			}
			seen[u.id] = u
		}
		rows.Close()
	}

	if len(roleIDs) > 0 {
		rows, err := d.db.Query(ctx, `
			SELECT DISTINCT u.id, u.email, coalesce(u.full_name,'')
			FROM users u
			JOIN user_role ur ON ur.user_id = u.id
			WHERE u.enabled = true AND ur.role_id = ANY($1)`, roleIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var u userRow
			if err := rows.Scan(&u.id, &u.email, &u.name); err != nil {
				rows.Close()
				return nil, err
			}
			seen[u.id] = u
		}
		rows.Close()
	}

	out := make([]userRow, 0, len(seen))
	for _, u := range seen {
		out = append(out, u)
	}
	return out, nil
}

func (d *Dispatcher) deliverInApp(ctx context.Context, userID, subject, body, linkDoctype, linkDocID string) error {
	_, err := d.db.Exec(ctx, `
		INSERT INTO notification (id, user_id, subject, body, link_doctype, link_document_id)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), NULLIF($6,''))`,
		dbx.NewIDWithPrefix("notif"), userID, subject, body, linkDoctype, linkDocID)
	return err
}

// defaultMessage builds a reasonable subject + body from the event key when
// no template overrides exist. Specific event_keys can be enriched here.
func defaultMessage(eventKey string, p map[string]any) (subject, body string) {
	name := strOrEmpty(p["document_name"])
	switch eventKey {
	case "invoice.issued":
		subject = "Invoice " + name + " issued"
	case "invoice.payment_received":
		subject = "Payment received for " + name
	case "invoice.overdue":
		subject = "Invoice " + name + " is overdue"
	case "po.sent":
		subject = "Purchase order " + name + " sent to supplier"
	case "approval.requested":
		subject = "Approval needed: " + name
	case "approval.decided":
		decision := strOrEmpty(p["decision"])
		subject = "Approval " + decision + ": " + name
	default:
		subject = "Logica ERP: " + eventKey
		if name != "" {
			subject += " (" + name + ")"
		}
	}

	if summary, ok := p["summary"].(string); ok && summary != "" {
		body = summary
	} else {
		j, _ := json.Marshal(p)
		body = "Event payload: " + string(j)
	}
	return
}

// ruleMatches — same shape as workflow/approval.go's ruleMatches. Kept local
// so notifrules has no upstream dep on workflow.
func ruleMatches(r loadedRule, fields map[string]any) bool {
	if r.ConditionField == "" {
		return true
	}
	raw, ok := fields[r.ConditionField]
	if !ok {
		return false
	}
	got, gotOK := toFloat(raw)
	want, wantOK := toFloat(r.ConditionValue)
	if gotOK && wantOK {
		switch r.ConditionOp {
		case ">":  return got >  want
		case ">=": return got >= want
		case "<":  return got <  want
		case "<=": return got <= want
		case "=":  return got == want
		case "<>": return got != want
		}
	}
	gotStr := fmt.Sprintf("%v", raw)
	switch r.ConditionOp {
	case "=":  return gotStr == r.ConditionValue
	case "<>": return gotStr != r.ConditionValue
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	if d, ok := v.(interface{ Float64() (float64, bool) }); ok {
		f, _ := d.Float64()
		return f, true
	}
	return 0, false
}

func strOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// mergedVars decorates payload with the recipient's identity so templates can
// reference {{.RecipientEmail}} / {{.RecipientName}}.
func mergedVars(payload map[string]any, u userRow) map[string]any {
	out := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		out[k] = v
	}
	out["RecipientEmail"] = u.email
	out["RecipientName"] = u.name
	return out
}

