package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// TimelineEntry is a unified record across the three feeds shown on a doc's
// form: events (writes), views (reads), and comments. Frontend renders them
// by `kind`.
type TimelineEntry struct {
	Kind       string          `json:"kind"` // "event" | "view" | "comment"
	ID         string          `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	UserID     string          `json:"user_id"`
	UserEmail  string          `json:"user_email,omitempty"`
	UserName   string          `json:"user_name,omitempty"`
	Action     string          `json:"action,omitempty"`  // event only
	Diff       json.RawMessage `json:"diff,omitempty"`    // event only
	Body       string          `json:"body,omitempty"`    // comment only
}

type TimelineService struct{ db *dbx.DB }

func NewTimelineService(db *dbx.DB) *TimelineService { return &TimelineService{db: db} }

// Fetch returns the merged event+view+comment feed for one document, newest
// first. Limit caps the per-feed slice (so a chatty viewer can't drown out
// the events you actually want to see).
func (s *TimelineService) Fetch(ctx context.Context, doctype, documentID string, limit int) ([]TimelineEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Each feed is queried independently — three small, partition-pruned
	// lookups — then merged in memory. This is much friendlier to the
	// planner than a UNION ALL across three differently-shaped tables.
	out := make([]TimelineEntry, 0, limit*3)

	evRows, err := s.db.Query(ctx, `
		SELECT e.id, e.occurred_at, e.action, e.diff,
		       e.changed_by, coalesce(u.email,''), coalesce(u.full_name,'')
		FROM doc_event e LEFT JOIN users u ON u.id = e.changed_by
		WHERE e.doctype = $1 AND e.document_id = $2
		ORDER BY e.occurred_at DESC LIMIT $3`, doctype, documentID, limit)
	if err != nil {
		return nil, err
	}
	for evRows.Next() {
		var e TimelineEntry
		e.Kind = "event"
		if err := evRows.Scan(&e.ID, &e.OccurredAt, &e.Action, &e.Diff, &e.UserID, &e.UserEmail, &e.UserName); err != nil {
			evRows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	evRows.Close()

	vwRows, err := s.db.Query(ctx, `
		SELECT v.id, v.occurred_at, v.viewed_by, coalesce(u.email,''), coalesce(u.full_name,'')
		FROM doc_view v LEFT JOIN users u ON u.id = v.viewed_by
		WHERE v.doctype = $1 AND v.document_id = $2
		ORDER BY v.occurred_at DESC LIMIT $3`, doctype, documentID, limit)
	if err != nil {
		return nil, err
	}
	for vwRows.Next() {
		var e TimelineEntry
		e.Kind = "view"
		if err := vwRows.Scan(&e.ID, &e.OccurredAt, &e.UserID, &e.UserEmail, &e.UserName); err != nil {
			vwRows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	vwRows.Close()

	cmRows, err := s.db.Query(ctx, `
		SELECT c.id, c.created_at, c.body, c.created_by, coalesce(u.email,''), coalesce(u.full_name,'')
		FROM document_comment c LEFT JOIN users u ON u.id = c.created_by
		WHERE c.doctype = $1 AND c.document_id = $2
		ORDER BY c.created_at DESC LIMIT $3`, doctype, documentID, limit)
	if err != nil {
		return nil, err
	}
	for cmRows.Next() {
		var e TimelineEntry
		e.Kind = "comment"
		if err := cmRows.Scan(&e.ID, &e.OccurredAt, &e.Body, &e.UserID, &e.UserEmail, &e.UserName); err != nil {
			cmRows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	cmRows.Close()

	// Merge sort by occurred_at DESC. Slice is small (<= 3*limit) — no need
	// for heap merge.
	sortByOccurredAtDesc(out)
	return out, nil
}

func sortByOccurredAtDesc(xs []TimelineEntry) {
	// Insertion sort is fine for small n; avoids importing sort just for this.
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].OccurredAt.After(xs[j-1].OccurredAt); j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// ---- HTTP ----

type TimelineHandler struct {
	Service *TimelineService
	Perm    *permission.Engine
}

func RegisterTimeline(api huma.API, h *TimelineHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "doc-timeline",
		Method:      http.MethodGet,
		Path:        "/platform/timeline",
		Summary:     "Merged event + view + comment feed for one document",
		Tags:        []string{"Platform / Timeline"},
	}, func(ctx context.Context, in *timelineIn) (*timelineOut, error) {
		if in.Doctype == "" || in.DocumentID == "" {
			return nil, huma.NewError(http.StatusBadRequest, "doctype and document_id are required")
		}
		// Reuse the target doctype's read permission — if you can see the
		// document you can see its timeline.
		if err := h.Perm.Check(ctx, in.Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		entries, err := h.Service.Fetch(ctx, in.Doctype, in.DocumentID, in.Limit)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &timelineOut{Body: timelineBody{Items: entries}}, nil
	})
}

type (
	timelineIn struct {
		Doctype    string `query:"doctype"     required:"true"`
		DocumentID string `query:"document_id" required:"true"`
		Limit      int    `query:"limit"`
	}
	timelineOut  struct{ Body timelineBody }
	timelineBody struct {
		Items []TimelineEntry `json:"items"`
	}
)
