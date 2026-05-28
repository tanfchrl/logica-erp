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

// dispatch matches rules + materializes one notification_dispatch row per
// (rule, channel, recipient). Rows are committed even if subsequent rows
// fail to insert. A background worker (Dispatcher.RunWorker) processes the
// queue with exponential backoff.
func (d *Dispatcher) dispatch(ctx context.Context, eventKey string, payload map[string]any) error {
	companyID, _ := payload["company_id"].(string)

	rules, err := d.loadActive(ctx, eventKey, companyID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

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
				addr := u.email
				if ch == "in_app" {
					addr = ""
				}
				if _, err := d.db.Exec(ctx, `
					INSERT INTO notification_dispatch
					  (id, rule_id, event_key, channel, recipient_user, recipient_addr, payload)
					VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					dbx.NewIDWithPrefix("ndsp"), r.ID, eventKey, ch, u.id, addr, payloadJSON); err != nil {
					d.log.Warn("dispatch enqueue", "rule", r.ID, "channel", ch, "user", u.id, "err", err)
				}
			}
		}
	}

	// Best-effort kick: try delivery of any due rows immediately so well-
	// behaved channels don't wait for the next worker tick.
	go d.drain(context.Background())
	return nil
}

// RunWorker starts a background loop that processes the dispatch queue. Call
// it once from main.go. Stops when ctx is cancelled.
func (d *Dispatcher) RunWorker(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.drain(ctx)
		}
	}
}

// drain claims due rows and attempts delivery. Each row gets its own short
// transaction-less attempt — the dispatch row is the unit of work, not a tx.
func (d *Dispatcher) drain(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	rows, err := d.db.Query(ctx, `
		SELECT id, event_key, channel, recipient_user, recipient_addr,
		       payload, attempt, max_attempts
		FROM notification_dispatch
		WHERE status = 'pending' AND next_attempt_at <= now()
		ORDER BY next_attempt_at
		LIMIT 50`)
	if err != nil {
		d.log.Error("drain query", "err", err)
		return
	}
	type row struct {
		id, eventKey, channel, userID, addr string
		payload                             []byte
		attempt, maxAttempts                int
	}
	var work []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.eventKey, &r.channel, &r.userID, &r.addr,
			&r.payload, &r.attempt, &r.maxAttempts); err != nil {
			rows.Close()
			d.log.Error("drain scan", "err", err)
			return
		}
		work = append(work, r)
	}
	rows.Close()

	for _, w := range work {
		var payload map[string]any
		_ = json.Unmarshal(w.payload, &payload)
		subject, body := defaultMessage(w.eventKey, payload)
		linkDoctype := strOrEmpty(payload["doctype"])
		linkDocID := strOrEmpty(payload["document_id"])

		var deliverErr error
		switch w.channel {
		case "in_app":
			deliverErr = d.deliverInApp(ctx, w.userID, subject, body, linkDoctype, linkDocID)
		case "email":
			if d.email == nil {
				deliverErr = errNoTransport("email")
			} else {
				vars := payload
				if vars == nil {
					vars = map[string]any{}
				}
				vars["RecipientEmail"] = w.addr
				_, deliverErr = d.email.SendTemplated(ctx, w.eventKey, w.addr, vars)
			}
		case "whatsapp":
			deliverErr = errNoTransport("whatsapp")
		}

		if deliverErr == nil {
			if _, err := d.db.Exec(ctx, `
				UPDATE notification_dispatch
				SET status = 'sent', attempt = attempt + 1, delivered_at = now(), last_error = ''
				WHERE id = $1`, w.id); err != nil {
				d.log.Error("mark sent", "id", w.id, "err", err)
			}
			continue
		}

		nextAttempt := w.attempt + 1
		if nextAttempt >= w.maxAttempts {
			if _, err := d.db.Exec(ctx, `
				UPDATE notification_dispatch
				SET status = 'permanently_failed', attempt = $2, last_error = $3
				WHERE id = $1`, w.id, nextAttempt, deliverErr.Error()); err != nil {
				d.log.Error("mark perm fail", "id", w.id, "err", err)
			}
			continue
		}
		// Exponential backoff: 1m, 2m, 4m, 8m, 16m (capped).
		backoff := time.Duration(1<<min(nextAttempt-1, 4)) * time.Minute
		if _, err := d.db.Exec(ctx, `
			UPDATE notification_dispatch
			SET attempt = $2, last_error = $3, next_attempt_at = now() + $4
			WHERE id = $1`, w.id, nextAttempt, deliverErr.Error(), backoff); err != nil {
			d.log.Error("mark retry", "id", w.id, "err", err)
		}
	}
}

type errNoTransport string

func (e errNoTransport) Error() string {
	return "no transport wired for channel " + string(e)
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
	case "so.submitted":
		subject = "Sales order " + name + " submitted"
	case "bill.received":
		subject = "Purchase invoice " + name + " received"
	case "journal_entry.submitted":
		subject = "Journal entry " + name + " submitted"
	case "mr.submitted":
		subject = "Material request " + name + " submitted"
	case "pr.received":
		subject = "Purchase receipt " + name + " submitted"
	case "stock_entry.submitted":
		purpose := strOrEmpty(p["purpose"])
		subject = "Stock entry " + name + " submitted"
		if purpose != "" {
			subject += " (" + purpose + ")"
		}
	case "bom.submitted":
		subject = "BOM " + name + " submitted"
	case "work_order.submitted":
		subject = "Work order " + name + " submitted"
	case "asset.acquired":
		subject = "Asset " + name + " acquired"
	case "payroll.run":
		subject = "Payroll " + name + " submitted"
	case "timesheet.submitted":
		subject = "Timesheet " + name + " submitted"
	case "pos_invoice.submitted":
		subject = "POS sale " + name + " submitted"
	case "period_closing.submitted":
		subject = "Period closing " + name + " submitted"
	case "asset_movement.submitted":
		subject = "Asset movement " + name + " submitted"
	case "asset_value_adjustment.submitted":
		kind := strOrEmpty(p["kind"])
		subject = "Asset value adjustment " + name + " submitted"
		if kind != "" {
			subject += " (" + kind + ")"
		}
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

