// Package approvals manages the agent's draft-review queue. Tier-1 drafts
// produced by the copilot land in agent_approval_queue with status=pending;
// a human reviews and either opens the document to submit (approved) or
// flags it as rejected. This is distinct from the workflow approval_request
// table — that one gates submitted documents, this one gates whose drafts
// the human will look at first.
package approvals

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Entry struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	UserID       string    `json:"user_id"`
	CompanyID    string    `json:"company_id,omitempty"`
	Doctype      string    `json:"doctype"`
	DocumentID   string    `json:"document_id"`
	DocumentName string    `json:"document_name"`
	Prompt       string    `json:"prompt"`
	Status       string    `json:"status"`
	ResolvedBy   string    `json:"resolved_by,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Store struct{ db *dbx.DB }

func New(db *dbx.DB) *Store { return &Store{db: db} }

// Enqueue records one Tier-1 draft as awaiting human review. Idempotent on
// (doctype, document_id) — if the same draft is re-proposed, the original
// row stands.
func (s *Store) Enqueue(ctx context.Context, e Entry) (*Entry, error) {
	if e.ID == "" {
		e.ID = dbx.NewIDWithPrefix("aprq")
	}
	if e.SessionID == "" || e.UserID == "" || e.Doctype == "" || e.DocumentID == "" {
		return nil, errors.New("approvals: session/user/doctype/document_id required")
	}
	if e.Status == "" {
		e.Status = "pending"
	}
	var out Entry
	err := s.db.QueryRow(ctx, `
		INSERT INTO agent_approval_queue
		  (id, session_id, user_id, company_id, doctype, document_id, document_name, prompt, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING
		RETURNING id, session_id, user_id, coalesce(company_id,''), doctype, document_id, document_name, prompt, status, coalesce(resolved_by,''), resolved_at, created_at`,
		e.ID, e.SessionID, e.UserID, nullStr(e.CompanyID), e.Doctype, e.DocumentID, e.DocumentName, e.Prompt, e.Status).
		Scan(&out.ID, &out.SessionID, &out.UserID, &out.CompanyID, &out.Doctype, &out.DocumentID, &out.DocumentName, &out.Prompt, &out.Status, &out.ResolvedBy, &out.ResolvedAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already enqueued — fetch and return the original.
		return s.Get(ctx, e.UserID, e.Doctype, e.DocumentID)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Get fetches the queue entry for (userID, doctype, documentID).
func (s *Store) Get(ctx context.Context, userID, doctype, documentID string) (*Entry, error) {
	var e Entry
	err := s.db.QueryRow(ctx, `
		SELECT id, session_id, user_id, coalesce(company_id,''), doctype, document_id, document_name, prompt, status, coalesce(resolved_by,''), resolved_at, created_at
		FROM agent_approval_queue
		WHERE user_id = $1 AND doctype = $2 AND document_id = $3
		ORDER BY created_at DESC LIMIT 1`, userID, doctype, documentID).
		Scan(&e.ID, &e.SessionID, &e.UserID, &e.CompanyID, &e.Doctype, &e.DocumentID, &e.DocumentName, &e.Prompt, &e.Status, &e.ResolvedBy, &e.ResolvedAt, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListPending returns the caller's pending drafts, newest first. Powers the
// "AI Drafts menunggu review" surface on the home dashboard.
func (s *Store) ListPending(ctx context.Context, userID string) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, session_id, user_id, coalesce(company_id,''), doctype, document_id, document_name, prompt, status, coalesce(resolved_by,''), resolved_at, created_at
		FROM agent_approval_queue
		WHERE user_id = $1 AND status = 'pending'
		ORDER BY created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.SessionID, &e.UserID, &e.CompanyID, &e.Doctype, &e.DocumentID, &e.DocumentName, &e.Prompt, &e.Status, &e.ResolvedBy, &e.ResolvedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Resolve transitions an entry to approved or rejected by `resolverID`. The
// resolver must be the same user the entry belongs to — cross-user resolves
// fail. Used by the FE when the human opens the draft (approved) or
// dismisses it (rejected).
func (s *Store) Resolve(ctx context.Context, resolverID, id, newStatus string) error {
	if newStatus != "approved" && newStatus != "rejected" {
		return errors.New("approvals: status must be approved or rejected")
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE agent_approval_queue
		SET status = $1, resolved_by = $2, resolved_at = now()
		WHERE id = $3 AND user_id = $2 AND status = 'pending'`,
		newStatus, resolverID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var ErrNotFound = errors.New("approvals: entry not found or not yours")

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
