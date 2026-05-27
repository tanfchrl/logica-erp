package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// TimelineEntry is a unified record across four feeds: events (writes),
// views (reads), comments, and workflow approvals. Frontend renders by `kind`.
//
// Diff payloads are populated only for system users (Principal.IsSystem) —
// regular users see the high-level "Alice modified fields" line but not the
// before/after JSON, which often contains sensitive financial deltas.
type TimelineEntry struct {
	Kind       string          `json:"kind"` // "event" | "view" | "comment" | "approval"
	ID         string          `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	UserID     string          `json:"user_id"`
	UserEmail  string          `json:"user_email,omitempty"`
	UserName   string          `json:"user_name,omitempty"`
	Action     string          `json:"action,omitempty"` // event: create/update/submit/... | approval: requested/approved/rejected
	Diff       json.RawMessage `json:"diff,omitempty"`   // event only, system users only
	Body       string          `json:"body,omitempty"`   // comment only, or approval decision_note
	// Approval-only fields:
	RuleName string `json:"rule_name,omitempty"`
}

type TimelineService struct{ db *dbx.DB }

func NewTimelineService(db *dbx.DB) *TimelineService { return &TimelineService{db: db} }

// Fetch returns the merged event+view+comment+approval feed for one document.
// Diff payloads are stripped for non-system callers (caller controls via the
// includeDetails flag — the handler infers it from auth.Principal.IsSystem).
func (s *TimelineService) Fetch(ctx context.Context, doctype, documentID string, limit int, includeDetails bool) ([]TimelineEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Each feed is queried independently — small partition-pruned lookups
	// then merged in memory. Friendlier to the planner than a UNION ALL.
	out := make([]TimelineEntry, 0, limit*4)

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
		if !includeDetails {
			e.Diff = nil
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

	// Approval feed: each approval_request row yields up to two timeline
	// entries — "requested" (always) and "approved"/"rejected" (if decided).
	apRows, err := s.db.Query(ctx, `
		SELECT a.id, a.status, a.decision_note,
		       a.requested_by, a.requested_at, coalesce(ur.email,''), coalesce(ur.full_name,''),
		       a.decided_by,   a.decided_at,   coalesce(ud.email,''), coalesce(ud.full_name,''),
		       coalesce(r.name, '')
		FROM approval_request a
		LEFT JOIN users         ur ON ur.id = a.requested_by
		LEFT JOIN users         ud ON ud.id = a.decided_by
		LEFT JOIN approval_rule r  ON r.id  = a.rule_id
		WHERE a.doctype = $1 AND a.document_id = $2
		ORDER BY a.requested_at DESC LIMIT $3`, doctype, documentID, limit)
	if err != nil {
		return nil, err
	}
	for apRows.Next() {
		var (
			id, status, note         string
			reqBy, reqEmail, reqName string
			decEmail, decName        string
			ruleName                 string
			reqAt                    time.Time
			decAt                    *time.Time
			decByPtr                 *string
		)
		if err := apRows.Scan(&id, &status, &note,
			&reqBy, &reqAt, &reqEmail, &reqName,
			&decByPtr, &decAt, &decEmail, &decName,
			&ruleName); err != nil {
			apRows.Close()
			return nil, err
		}
		// "Requested" is always emitted.
		out = append(out, TimelineEntry{
			Kind: "approval", ID: id + ":req",
			OccurredAt: reqAt,
			UserID:     reqBy, UserEmail: reqEmail, UserName: reqName,
			Action:   "requested",
			RuleName: ruleName,
		})
		// "Approved"/"Rejected" emitted only if decided.
		if decAt != nil && status != "pending" && decByPtr != nil {
			body := note
			out = append(out, TimelineEntry{
				Kind: "approval", ID: id + ":dec",
				OccurredAt: *decAt,
				UserID:     *decByPtr, UserEmail: decEmail, UserName: decName,
				Action:   status, // "approved" or "rejected"
				RuleName: ruleName,
				Body:     body,
			})
		}
	}
	apRows.Close()

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
		// Diff payloads (which can carry sensitive financial deltas) are
		// only returned to system users. Regular users get the high-level
		// timeline with no JSON.
		p := auth.FromContext(ctx)
		includeDetails := p != nil && p.IsSystem
		entries, err := h.Service.Fetch(ctx, in.Doctype, in.DocumentID, in.Limit, includeDetails)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &timelineOut{Body: timelineBody{Items: entries, CanViewDetails: includeDetails}}, nil
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
		Items          []TimelineEntry `json:"items"`
		CanViewDetails bool            `json:"can_view_details"`
	}
)
