package audit

import (
	"context"
	"log/slog"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// dedupHours is the window during which repeated views of the same document
// by the same user collapse to a single doc_view row. Confirmed with the user
// on 2026-05-27 as 24h.
const dedupHours = 24

// ViewRecorder writes deduped read events to doc_view. Fire-and-forget: the
// caller doesn't see DB errors (we log them); a failed view-record never
// blocks the actual page render.
type ViewRecorder struct {
	db *dbx.DB
}

func NewViewRecorder(db *dbx.DB) *ViewRecorder { return &ViewRecorder{db: db} }

// Record asynchronously inserts a doc_view row for (userID, doctype, documentID)
// if and only if no row exists in the last 24h. Returns immediately.
func (r *ViewRecorder) Record(ctx context.Context, doctype, documentID, userID string) {
	if doctype == "" || documentID == "" || userID == "" {
		return
	}
	go r.recordSync(context.WithoutCancel(ctx), doctype, documentID, userID)
}

func (r *ViewRecorder) recordSync(ctx context.Context, doctype, documentID, userID string) {
	// Single conditional INSERT; the dedup index makes the EXISTS subquery a
	// cheap partition-local lookup. Note: NOT EXISTS races are tolerated —
	// at worst two concurrent reads from the same user yield two rows, which
	// is acceptable (and rare since dedupHours is wide).
	_, err := r.db.Exec(ctx, `
		INSERT INTO doc_view (id, doctype, document_id, viewed_by)
		SELECT $1, $2, $3, $4
		WHERE NOT EXISTS (
		  SELECT 1 FROM doc_view
		  WHERE doctype = $2 AND document_id = $3 AND viewed_by = $4
		    AND occurred_at > now() - make_interval(hours => $5)
		)`,
		dbx.NewIDWithPrefix("vw"), doctype, documentID, userID, dedupHours)
	if err != nil {
		slog.Warn("audit: record view failed",
			"doctype", doctype, "document_id", documentID, "user", userID, "err", err)
	}
}
