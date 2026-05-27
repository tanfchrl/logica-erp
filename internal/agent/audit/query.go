package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

// AuditEntry is the API-facing row shape — agent_audit_log rows joined with the
// user table for display.
type AuditEntry struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	UserID     string          `json:"user_id"`
	UserEmail  string          `json:"user_email,omitempty"`
	UserName   string          `json:"user_name,omitempty"`
	CompanyID  string          `json:"company_id,omitempty"`
	Turn       int             `json:"turn"`
	EventType  string          `json:"event_type"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Model      string          `json:"model,omitempty"`
	TokensIn   int             `json:"tokens_in"`
	TokensOut  int             `json:"tokens_out"`
	LatencyMS  int             `json:"latency_ms"`
	CreatedAt  time.Time       `json:"created_at"`
}

// QueryParams scope the agent_audit_log read. Empty fields are unconstrained.
type QueryParams struct {
	UserID    string
	SessionID string
	EventType string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// Query is the read service. Always require system access at the handler
// layer — the data here is cross-user and not safe to expose otherwise.
type Query struct{ db *dbx.DB }

func NewQuery(db *dbx.DB) *Query { return &Query{db: db} }

func (q *Query) Fetch(ctx context.Context, p QueryParams) ([]AuditEntry, error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 100
	}
	const sql = `
		SELECT a.id, a.session_id, a.user_id,
		       coalesce(u.email,''), coalesce(u.full_name,''),
		       coalesce(a.company_id,''), a.turn, a.event_type,
		       a.payload, a.model, a.tokens_in, a.tokens_out, a.latency_ms,
		       a.created_at
		FROM agent_audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = '' OR a.user_id    = $1)
		  AND ($2 = '' OR a.session_id = $2)
		  AND ($3 = '' OR a.event_type = $3)
		  AND ($4::timestamptz IS NULL OR a.created_at >= $4)
		  AND ($5::timestamptz IS NULL OR a.created_at <  $5)
		ORDER BY a.created_at DESC
		LIMIT $6`
	var since, until any
	if !p.Since.IsZero() {
		since = p.Since
	}
	if !p.Until.IsZero() {
		until = p.Until
	}
	rows, err := q.db.Query(ctx, sql, p.UserID, p.SessionID, p.EventType, since, until, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditEntry, 0)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.SessionID, &e.UserID, &e.UserEmail, &e.UserName,
			&e.CompanyID, &e.Turn, &e.EventType, &e.Payload,
			&e.Model, &e.TokensIn, &e.TokensOut, &e.LatencyMS, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- HTTP ----

// RegisterAdmin wires GET /admin/audit-log onto the agent service's huma API.
// System-only — non-system users get 403 even if they somehow construct a
// valid query string. The data is cross-user (cost analytics, accuracy
// review) so this gate is non-negotiable per spec §8.
func RegisterAdmin(api huma.API, q *Query) {
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-audit-log",
		Method:      http.MethodGet,
		Path:        "/admin/audit-log",
		Summary:     "Query the agent_audit_log (system administrators only)",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, in *queryIn) (*queryOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		if !p.IsSystem {
			return nil, huma.NewError(http.StatusForbidden, "system administrators only")
		}

		var since, until time.Time
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "since: "+err.Error())
			}
			since = t
		}
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "until: "+err.Error())
			}
			until = t
		}
		es, err := q.Fetch(ctx, QueryParams{
			UserID:    in.UserID,
			SessionID: in.SessionID,
			EventType: strings.ToLower(in.EventType),
			Since:     since,
			Until:     until,
			Limit:     in.Limit,
		})
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &queryOut{Body: queryBody{Items: es}}, nil
	})
}

// ErrUnauthorized matches the typed sentinel callers can compare against.
var ErrUnauthorized = errors.New("agent audit: not authorised")

type (
	queryIn struct {
		UserID    string `query:"user_id"`
		SessionID string `query:"session_id"`
		EventType string `query:"event_type"`
		Since     string `query:"since" doc:"RFC3339"`
		Until     string `query:"until" doc:"RFC3339"`
		Limit     int    `query:"limit"`
	}
	queryOut  struct{ Body queryBody }
	queryBody struct {
		Items []AuditEntry `json:"items"`
	}
)
