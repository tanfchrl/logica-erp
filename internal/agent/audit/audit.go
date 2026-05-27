// Package audit writes append-only events to the partitioned agent_audit_log
// table — the trust foundation per docs/agent-build-prompt.md §8.
//
// Every prompt, tool call, tool result, proposal, human approval, human
// rejection, policy block, and orchestration error lands here. The log is
// exposed to admins under Settings → AI Audit Log; it is never deletable
// from the UI.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type EventType string

const (
	EventPrompt         EventType = "prompt"
	EventToolCall       EventType = "tool_call"
	EventToolResult     EventType = "tool_result"
	EventProposal       EventType = "proposal"
	EventHumanApproved  EventType = "human_approved"
	EventHumanRejected  EventType = "human_rejected"
	EventPolicyBlocked  EventType = "policy_blocked"
	EventError          EventType = "error"
)

// Recorder writes to agent_audit_log. Safe for concurrent use.
type Recorder struct {
	db *dbx.DB
}

func New(db *dbx.DB) *Recorder { return &Recorder{db: db} }

// Event is one audit entry. Required fields: SessionID, UserID, Type, Payload.
type Event struct {
	SessionID  string
	UserID     string
	CompanyID  string
	Turn       int
	Type       EventType
	Payload    any
	Model      string
	TokensIn   int
	TokensOut  int
	LatencyMS  int
}

// Record writes a single event. Errors are logged but never returned to the
// orchestration loop — losing an audit row is bad but should not cascade
// into a failed user interaction. Use slog to surface failures for ops to
// investigate.
func (r *Recorder) Record(ctx context.Context, e Event) {
	if e.SessionID == "" || e.UserID == "" || e.Type == "" {
		slog.Warn("agent audit: dropping event with missing required fields",
			"session_id", e.SessionID, "user_id", e.UserID, "type", e.Type)
		return
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		slog.Warn("agent audit: payload marshal failed", "err", err, "type", e.Type)
		payload = []byte("{}")
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO agent_audit_log
		  (id, session_id, user_id, company_id, turn, event_type, payload,
		   model, tokens_in, tokens_out, latency_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		dbx.NewIDWithPrefix("aev"),
		e.SessionID, e.UserID, nullStr(e.CompanyID), e.Turn, string(e.Type), payload,
		e.Model, e.TokensIn, e.TokensOut, e.LatencyMS)
	if err != nil {
		slog.Warn("agent audit: insert failed", "err", err, "type", e.Type)
	}
}

// ErrMissing is returned by Lookup when an event id can't be found. Lookup is
// only used by the admin UI; the agent orchestration never reads back.
var ErrMissing = errors.New("agent audit: event not found")

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
