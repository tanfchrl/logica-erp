// Package email manages SMTP configuration, per-event message templates, and a
// thin send-log. Actual dispatch uses net/smtp; integration with downstream
// services (invoice issued, password reset, etc.) is wired by the calling
// service via Service.SendTemplated.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const (
	DoctypeSMTP     = "smtp_config"
	DoctypeTemplate = "email_template"
)

// EventCatalog enumerates the events the rest of the system can fire. Surfaced
// to the UI so admins know which keys to bind templates for.
var EventCatalog = []EventDef{
	{Key: "invoice.issued",         Label: "Sales invoice issued",       Description: "Customer is notified when a sales invoice is submitted."},
	{Key: "invoice.payment_received", Label: "Payment received",         Description: "Customer is thanked once a payment entry clears."},
	{Key: "invoice.overdue",        Label: "AR overdue dunning",         Description: "Outstanding invoice has aged past terms."},
	{Key: "po.sent",                Label: "PO sent to supplier",        Description: "Supplier receives a copy of a purchase order."},
	{Key: "user.password_reset",    Label: "Password reset",             Description: "Magic-link / OTP for password recovery."},
	{Key: "user.invited",           Label: "Team invitation",            Description: "New user is invited to the workspace."},
	{Key: "test",                   Label: "Test email",                 Description: "Used by the SMTP test-send button."},
}

type EventDef struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ---- SMTP config ----

// SMTPConfig is the API shape. Password is omitted on every read.
// CompanyID empty = workspace-wide fallback config.
type SMTPConfig struct {
	CompanyID    string    `json:"company_id,omitempty"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	Username     string    `json:"username,omitempty"`
	HasPassword  bool      `json:"has_password"`
	UseTLS       bool      `json:"use_tls"`
	FromEmail    string    `json:"from_email"`
	FromName     string    `json:"from_name,omitempty"`
	ReplyToEmail string    `json:"reply_to_email,omitempty"`
	IsEnabled    bool      `json:"is_enabled"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SMTPSaveInput struct {
	CompanyID    string  `json:"company_id,omitempty" doc:"omit for workspace-wide fallback config"`
	Host         string  `json:"host"`
	Port         int     `json:"port,omitempty"   doc:"defaults to 587"`
	Username     string  `json:"username,omitempty"`
	Password     *string `json:"password,omitempty" doc:"omit to leave unchanged; empty string to clear"`
	UseTLS       *bool   `json:"use_tls,omitempty"`
	FromEmail    string  `json:"from_email"`
	FromName     string  `json:"from_name,omitempty"`
	ReplyToEmail string  `json:"reply_to_email,omitempty"`
	IsEnabled    *bool   `json:"is_enabled,omitempty"`
}

// ---- Templates ----

type Template struct {
	ID         string    `json:"id"`
	EventKey   string    `json:"event_key"`
	CompanyID  string    `json:"company_id,omitempty"`
	Subject    string    `json:"subject"`
	BodyHTML   string    `json:"body_html"`
	IsEnabled  bool      `json:"is_enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TemplateSaveInput struct {
	EventKey  string `json:"event_key"`
	CompanyID string `json:"company_id,omitempty"`
	Subject   string `json:"subject"`
	BodyHTML  string `json:"body_html"`
	IsEnabled *bool  `json:"is_enabled,omitempty"`
}

// ---- Log ----

type LogEntry struct {
	ID           string    `json:"id"`
	ToAddr       string    `json:"to_addr"`
	Subject      string    `json:"subject"`
	EventKey     string    `json:"event_key,omitempty"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	SentAt       time.Time `json:"sent_at"`
}

// ---- Service ----

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// GetConfig returns the most-specific config for `companyID`. If a company-
// scoped row exists it's returned; otherwise the workspace-wide (NULL) row;
// otherwise an empty placeholder so the UI can render the form.
func (s *Service) GetConfig(ctx context.Context, companyID string) (*SMTPConfig, error) {
	var c SMTPConfig
	var passwordSet bool
	err := s.db.QueryRow(ctx, `
		SELECT coalesce(company_id,''), host, port, username, (password <> ''), use_tls,
		       from_email, from_name, reply_to_email, is_enabled, updated_at
		FROM smtp_config
		WHERE company_id = $1 OR company_id IS NULL
		ORDER BY (company_id IS NULL) ASC
		LIMIT 1`, companyID).
		Scan(&c.CompanyID, &c.Host, &c.Port, &c.Username, &passwordSet, &c.UseTLS,
			&c.FromEmail, &c.FromName, &c.ReplyToEmail, &c.IsEnabled, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Return an empty config so the UI can show the form.
		return &SMTPConfig{CompanyID: companyID, Port: 587, UseTLS: true}, nil
	}
	if err != nil {
		return nil, err
	}
	c.HasPassword = passwordSet
	return &c, nil
}

func (s *Service) SaveConfig(ctx context.Context, in SMTPSaveInput) (*SMTPConfig, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("smtp: unauthenticated")
	}
	in.Host = strings.TrimSpace(in.Host)
	in.FromEmail = strings.TrimSpace(in.FromEmail)
	if in.Host == "" {
		return nil, errors.New("smtp.host: required")
	}
	if in.FromEmail == "" {
		return nil, errors.New("smtp.from_email: required")
	}
	if in.Port == 0 {
		in.Port = 587
	}
	useTLS := true
	if in.UseTLS != nil {
		useTLS = *in.UseTLS
	}
	enabled := false
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}

	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Existing row for this scope? Pull id + password to support partial
		// updates (omit password = keep current).
		var (
			existingID   string
			currentPass  string
		)
		_ = tx.QueryRow(ctx, `
			SELECT id, password FROM smtp_config
			WHERE coalesce(company_id,'') = $1`, in.CompanyID).Scan(&existingID, &currentPass)
		pass := currentPass
		if in.Password != nil {
			pass = *in.Password
		}

		if existingID != "" {
			_, err := tx.Exec(ctx, `
				UPDATE smtp_config SET host = $2, port = $3, username = $4, password = $5,
				  use_tls = $6, from_email = $7, from_name = $8, reply_to_email = $9,
				  is_enabled = $10, updated_at = now(), updated_by = $11
				WHERE id = $1`,
				existingID, in.Host, in.Port, in.Username, pass, useTLS,
				in.FromEmail, in.FromName, in.ReplyToEmail, enabled, p.UserID)
			return err
		}

		newID := "smtp_singleton"
		if in.CompanyID != "" {
			newID = dbx.NewIDWithPrefix("smtp")
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO smtp_config (id, company_id, host, port, username, password, use_tls,
			                         from_email, from_name, reply_to_email, is_enabled, updated_at, updated_by)
			VALUES ($1, NULLIF($2,''), $3,$4,$5,$6,$7,$8,$9,$10,$11, now(), $12)`,
			newID, in.CompanyID, in.Host, in.Port, in.Username, pass, useTLS,
			in.FromEmail, in.FromName, in.ReplyToEmail, enabled, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetConfig(ctx, in.CompanyID)
}

// SendTest sends a fixed body to the given address using the saved config
// for the active company (or workspace-wide fallback). Not logged.
func (s *Service) SendTest(ctx context.Context, to string) error {
	companyID := auth.CompanyFromContext(ctx)
	cfg, err := s.GetConfig(ctx, companyID)
	if err != nil {
		return err
	}
	if cfg.Host == "" {
		return errors.New("smtp: not configured")
	}
	pass, err := s.fetchPassword(ctx, cfg.CompanyID)
	if err != nil {
		return err
	}
	return s.deliver(cfg, pass, to,
		"Logica ERP — SMTP test",
		`<p>Hello,</p><p>This is a test email from Logica ERP confirming that your SMTP configuration is working.</p><p>If you received this, you're all set.</p>`)
}

// SendTemplated renders the template for an event and delivers it to `to`,
// persisting an entry in email_log. Returns the log id.
//
// The company under which to look up SMTP config is taken from the vars
// payload's "company_id" key (the dispatcher fills it from event payloads).
// Falls back to the workspace-wide config if no company-specific one exists.
func (s *Service) SendTemplated(ctx context.Context, eventKey, to string, vars map[string]any) (string, error) {
	companyID, _ := vars["company_id"].(string)
	cfg, err := s.GetConfig(ctx, companyID)
	if err != nil {
		return "", err
	}
	if cfg.Host == "" || !cfg.IsEnabled {
		return "", errors.New("smtp: not configured or disabled")
	}
	tpl, err := s.findTemplate(ctx, eventKey)
	if err != nil {
		return "", err
	}
	subject, body, err := renderTemplate(tpl, vars)
	if err != nil {
		return "", err
	}
	pass, err := s.fetchPassword(ctx, cfg.CompanyID)
	if err != nil {
		return "", err
	}

	status := "sent"
	errMsg := ""
	if err := s.deliver(cfg, pass, to, subject, body); err != nil {
		status = "failed"
		errMsg = err.Error()
	}
	logID := dbx.NewIDWithPrefix("eml")
	if _, dberr := s.db.Exec(ctx, `
		INSERT INTO email_log (id, to_addr, subject, event_key, status, error_message)
		VALUES ($1,$2,$3,$4,$5,$6)`, logID, to, subject, eventKey, status, errMsg); dberr != nil {
		return "", dberr
	}
	if status == "failed" {
		return logID, errors.New(errMsg)
	}
	return logID, nil
}

func (s *Service) fetchPassword(ctx context.Context, companyID string) (string, error) {
	var p string
	err := s.db.QueryRow(ctx, `
		SELECT password FROM smtp_config
		WHERE company_id = $1 OR company_id IS NULL
		ORDER BY (company_id IS NULL) ASC
		LIMIT 1`, companyID).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return p, err
}

func (s *Service) deliver(cfg *SMTPConfig, password, to, subject, htmlBody string) error {
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	from := cfg.FromEmail
	headers := map[string]string{
		"From":         formatAddress(cfg.FromName, cfg.FromEmail),
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": `text/html; charset="utf-8"`,
	}
	if cfg.ReplyToEmail != "" {
		headers["Reply-To"] = cfg.ReplyToEmail
	}
	var msg bytes.Buffer
	for k, v := range headers {
		msg.WriteString(k)
		msg.WriteString(": ")
		msg.WriteString(v)
		msg.WriteString("\r\n")
	}
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	var auther smtp.Auth
	if cfg.Username != "" {
		auther = smtp.PlainAuth("", cfg.Username, password, cfg.Host)
	}

	if cfg.UseTLS && cfg.Port == 465 {
		// implicit TLS (SMTPS)
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return fmt.Errorf("smtps dial: %w", err)
		}
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("smtps client: %w", err)
		}
		defer client.Close()
		if auther != nil {
			if err := client.Auth(auther); err != nil {
				return fmt.Errorf("smtps auth: %w", err)
			}
		}
		if err := client.Mail(from); err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(msg.Bytes()); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return client.Quit()
	}

	// STARTTLS / plain path
	return smtp.SendMail(addr, auther, from, []string{to}, msg.Bytes())
}

// ---- Template helpers ----

func (s *Service) findTemplate(ctx context.Context, eventKey string) (*Template, error) {
	co := auth.CompanyFromContext(ctx)
	row := s.db.QueryRow(ctx, `
		SELECT id, event_key, coalesce(company_id,''), subject, body_html, is_enabled, created_at, updated_at
		FROM email_template
		WHERE event_key = $1 AND (company_id = $2 OR company_id IS NULL)
		ORDER BY (company_id IS NULL) ASC
		LIMIT 1`, eventKey, co)
	var t Template
	err := row.Scan(&t.ID, &t.EventKey, &t.CompanyID, &t.Subject, &t.BodyHTML, &t.IsEnabled, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("email_template: none for event %q", eventKey)
	}
	if err != nil {
		return nil, err
	}
	if !t.IsEnabled {
		return nil, fmt.Errorf("email_template: %q disabled", eventKey)
	}
	return &t, nil
}

func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_key, coalesce(company_id,''), subject, body_html, is_enabled, created_at, updated_at
		FROM email_template ORDER BY event_key, company_id NULLS FIRST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Template, 0)
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.EventKey, &t.CompanyID, &t.Subject, &t.BodyHTML, &t.IsEnabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) UpsertTemplate(ctx context.Context, in TemplateSaveInput) (*Template, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("email_template: unauthenticated")
	}
	in.EventKey = strings.TrimSpace(in.EventKey)
	if in.EventKey == "" {
		return nil, errors.New("email_template.event_key: required")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("email_template.subject: required")
	}
	enabled := true
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}
	id := dbx.NewIDWithPrefix("emt")
	var t Template
	err := s.db.QueryRow(ctx, `
		INSERT INTO email_template (id, event_key, company_id, subject, body_html, is_enabled, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
		ON CONFLICT (event_key, company_id) DO UPDATE SET
		  subject = EXCLUDED.subject, body_html = EXCLUDED.body_html, is_enabled = EXCLUDED.is_enabled,
		  updated_at = now(), updated_by = EXCLUDED.updated_by
		RETURNING id, event_key, coalesce(company_id,''), subject, body_html, is_enabled, created_at, updated_at`,
		id, in.EventKey, nullable(in.CompanyID), in.Subject, in.BodyHTML, enabled, p.UserID).
		Scan(&t.ID, &t.EventKey, &t.CompanyID, &t.Subject, &t.BodyHTML, &t.IsEnabled, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM email_template WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("email_template %s: not found", id)
	}
	return nil
}

func (s *Service) RecentLog(ctx context.Context, limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, to_addr, subject, coalesce(event_key,''), status, error_message, sent_at
		FROM email_log ORDER BY sent_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LogEntry, 0)
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.ToAddr, &e.Subject, &e.EventKey, &e.Status, &e.ErrorMessage, &e.SentAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-email-events", Method: http.MethodGet,
		Path: "/admin/email/events", Summary: "List the events templates can be bound to",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, _ *struct{}) (*evListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &evListOut{Body: evListBody{Items: EventCatalog}}, nil
	})

	// SMTP config
	huma.Register(api, huma.Operation{
		OperationID: "get-smtp-config", Method: http.MethodGet,
		Path: "/admin/smtp", Summary: "Get SMTP configuration (optionally for a company)",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, in *smtpGetIn) (*smtpItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeSMTP, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.GetConfig(ctx, in.CompanyID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &smtpItemOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "save-smtp-config", Method: http.MethodPut,
		Path: "/admin/smtp", Summary: "Save SMTP configuration",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, in *smtpSaveIn) (*smtpItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeSMTP, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.SaveConfig(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &smtpItemOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "test-smtp", Method: http.MethodPost,
		Path: "/admin/smtp/test", Summary: "Send a test email",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, in *smtpTestIn) (*struct{ Body smtpTestOut }, error) {
		if err := h.Perm.Check(ctx, DoctypeSMTP, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.SendTest(ctx, in.Body.To); err != nil {
			return &struct{ Body smtpTestOut }{Body: smtpTestOut{OK: false, Error: err.Error()}}, nil
		}
		return &struct{ Body smtpTestOut }{Body: smtpTestOut{OK: true}}, nil
	})

	// Templates
	huma.Register(api, huma.Operation{
		OperationID: "list-email-templates", Method: http.MethodGet,
		Path: "/admin/email-templates", Summary: "List email templates",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, _ *struct{}) (*tplListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.ListTemplates(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &tplListOut{Body: tplListBody{Items: ts}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "upsert-email-template", Method: http.MethodPut,
		Path: "/admin/email-templates", Summary: "Create or update an email template",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, in *tplUpsertIn) (*tplItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.UpsertTemplate(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &tplItemOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-email-template", Method: http.MethodDelete,
		Path: "/admin/email-templates/{id}", Summary: "Delete an email template",
		Tags: []string{"Admin / Email"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *tplByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteTemplate(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// Log
	huma.Register(api, huma.Operation{
		OperationID: "list-email-log", Method: http.MethodGet,
		Path: "/admin/email-log", Summary: "Recent send attempts",
		Tags: []string{"Admin / Email"},
	}, func(ctx context.Context, in *logListIn) (*logListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeTemplate, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		es, err := h.Service.RecentLog(ctx, in.Limit)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &logListOut{Body: logListBody{Items: es}}, nil
	})
}

type (
	smtpItemOut struct{ Body SMTPConfig }
	smtpGetIn   struct {
		CompanyID string `query:"company_id"`
	}
	smtpSaveIn  struct{ Body SMTPSaveInput }
	smtpTestIn  struct {
		Body struct {
			To string `json:"to"`
		}
	}
	smtpTestOut struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}

	tplItemOut struct{ Body Template }
	tplUpsertIn struct{ Body TemplateSaveInput }
	tplByID    struct {
		ID string `path:"id"`
	}
	tplListOut  struct{ Body tplListBody }
	tplListBody struct {
		Items []Template `json:"items"`
	}

	evListOut  struct{ Body evListBody }
	evListBody struct {
		Items []EventDef `json:"items"`
	}

	logListIn struct {
		Limit int `query:"limit"`
	}
	logListOut  struct{ Body logListBody }
	logListBody struct {
		Items []LogEntry `json:"items"`
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func formatAddress(name, email string) string {
	if name == "" {
		return email
	}
	return fmt.Sprintf("%q <%s>", name, email)
}

func renderTemplate(t *Template, vars map[string]any) (subject, body string, err error) {
	subjTpl, err := template.New("s").Parse(t.Subject)
	if err != nil {
		return "", "", fmt.Errorf("template subject: %w", err)
	}
	bodyTpl, err := template.New("b").Parse(t.BodyHTML)
	if err != nil {
		return "", "", fmt.Errorf("template body: %w", err)
	}
	var sb, bb bytes.Buffer
	if err := subjTpl.Execute(&sb, vars); err != nil {
		return "", "", err
	}
	if err := bodyTpl.Execute(&bb, vars); err != nil {
		return "", "", err
	}
	return sb.String(), bb.String(), nil
}
