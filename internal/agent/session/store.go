// Package session persists agent conversations to Postgres. One row in
// agent_session per Copilot thread or Migration Wizard run; chronological
// messages in agent_conversation linked by session_id.
//
// Conversations are scoped to the user. A user resuming an open session
// must be the same user who created it — enforced server-side.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Kind string

const (
	KindCopilot   Kind = "copilot"
	KindMigration Kind = "migration"
)

type Session struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	CompanyID string         `json:"company_id,omitempty"`
	Kind      Kind           `json:"kind"`
	Title     string         `json:"title"`
	State     map[string]any `json:"state,omitempty"`
	ClosedAt  *time.Time     `json:"closed_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Message struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Turn       int             `json:"turn"`
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Store struct{ db *dbx.DB }

func New(db *dbx.DB) *Store { return &Store{db: db} }

// Create starts a new session for `userID`. The kind determines which UI
// surface owns it. Title is a short label the FE can render in a session list.
func (s *Store) Create(ctx context.Context, userID, companyID, title string, kind Kind) (*Session, error) {
	if userID == "" {
		return nil, errors.New("session: user_id required")
	}
	id := dbx.NewIDWithPrefix("ses")
	var ss Session
	err := s.db.QueryRow(ctx, `
		INSERT INTO agent_session (id, user_id, company_id, kind, title)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, user_id, coalesce(company_id,''), kind, title, state, closed_at, created_at, updated_at`,
		id, userID, nullStr(companyID), string(kind), title).
		Scan(&ss.ID, &ss.UserID, &ss.CompanyID, &ss.Kind, &ss.Title, &rawJSON{&ss.State}, &ss.ClosedAt, &ss.CreatedAt, &ss.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &ss, nil
}

// Get loads a session for the caller. Cross-user reads return ErrForbidden.
func (s *Store) Get(ctx context.Context, userID, sessionID string) (*Session, error) {
	var ss Session
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, coalesce(company_id,''), kind, title, state, closed_at, created_at, updated_at
		FROM agent_session WHERE id = $1`, sessionID).
		Scan(&ss.ID, &ss.UserID, &ss.CompanyID, &ss.Kind, &ss.Title, &rawJSON{&ss.State}, &ss.ClosedAt, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ss.UserID != userID {
		return nil, ErrForbidden
	}
	return &ss, nil
}

// ListOpenForUser returns up to 50 non-closed sessions for the user, newest
// first. Powers the "recent agent sessions" surface in the ⌘K palette.
func (s *Store) ListOpenForUser(ctx context.Context, userID string, kind Kind) ([]Session, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, coalesce(company_id,''), kind, title, state, closed_at, created_at, updated_at
		FROM agent_session
		WHERE user_id = $1 AND ($2 = '' OR kind = $2) AND closed_at IS NULL
		ORDER BY updated_at DESC LIMIT 50`, userID, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Session, 0)
	for rows.Next() {
		var ss Session
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.CompanyID, &ss.Kind, &ss.Title, &rawJSON{&ss.State}, &ss.ClosedAt, &ss.CreatedAt, &ss.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// SetState replaces the session state JSON. Used by the Migration Wizard to
// persist its step + accumulated SetupProfile across page reloads.
func (s *Store) SetState(ctx context.Context, sessionID string, state map[string]any) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE agent_session SET state = $1, updated_at = now() WHERE id = $2`, payload, sessionID)
	return err
}

// Close marks the session done. Closed sessions remain readable for audit,
// just hidden from the "recent" list. Reopening is intentionally not supported.
func (s *Store) Close(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `UPDATE agent_session SET closed_at = now(), updated_at = now() WHERE id = $1 AND closed_at IS NULL`, sessionID)
	return err
}

// AppendMessage writes one message to agent_conversation. The caller must
// supply a monotonically-increasing turn; the orchestrator computes the
// next turn from len(history)+1 to avoid round-trip lookups.
func (s *Store) AppendMessage(ctx context.Context, m Message) error {
	if m.SessionID == "" || m.Role == "" {
		return errors.New("session: message session_id + role required")
	}
	if m.ID == "" {
		m.ID = dbx.NewIDWithPrefix("amsg")
	}
	var toolCallsArg any = nil
	if len(m.ToolCalls) > 0 {
		toolCallsArg = []byte(m.ToolCalls)
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO agent_conversation
		  (id, session_id, turn, role, content, tool_calls, tool_call_id, tool_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		m.ID, m.SessionID, m.Turn, m.Role, m.Content,
		toolCallsArg, nullStr(m.ToolCallID), nullStr(m.ToolName))
	return err
}

// History returns the full chronological message list for a session. The
// orchestrator injects this verbatim into each chat-completions request.
func (s *Store) History(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, session_id, turn, role, content,
		       coalesce(tool_calls, 'null'::jsonb),
		       coalesce(tool_call_id, ''),
		       coalesce(tool_name, ''),
		       created_at
		FROM agent_conversation WHERE session_id = $1 ORDER BY turn`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		var tc []byte
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Turn, &m.Role, &m.Content, &tc, &m.ToolCallID, &m.ToolName, &m.CreatedAt); err != nil {
			return nil, err
		}
		if len(tc) > 0 && string(tc) != "null" {
			m.ToolCalls = json.RawMessage(tc)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Standard errors callers handle.
var (
	ErrNotFound  = errors.New("session: not found")
	ErrForbidden = errors.New("session: not yours")
)

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rawJSON helps Scan() decode a jsonb column into a map[string]any without
// allocating an intermediate []byte at the call site.
type rawJSON struct{ dst *map[string]any }

func (r *rawJSON) Scan(src any) error {
	if src == nil {
		*r.dst = nil
		return nil
	}
	b, ok := src.([]byte)
	if !ok {
		s, _ := src.(string)
		b = []byte(s)
	}
	if len(b) == 0 || string(b) == "null" {
		*r.dst = nil
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("session: state json: %w", err)
	}
	*r.dst = out
	return nil
}
