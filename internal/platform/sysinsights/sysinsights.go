// Package sysinsights aggregates "things ops should know" from the various
// admin tables: failed webhook deliveries, failed emails, stuck approval
// requests, import errors. Replaces a generic background-jobs dashboard for
// now — surfaces real failures we already record instead of a synthetic
// queue view.
package sysinsights

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "audit_log" // reuse — anyone who can read the audit log can read system insights

type Summary struct {
	WebhookFailures24h  int             `json:"webhook_failures_24h"`
	EmailFailures24h    int             `json:"email_failures_24h"`
	ApprovalPendingOver24h int          `json:"approval_pending_over_24h"`
	ImportErrors24h     int             `json:"import_errors_24h"`
	RecentFailures      []FailureRow    `json:"recent_failures"`
}

type FailureRow struct {
	Source    string    `json:"source"`    // webhook | email | approval | import
	Subject   string    `json:"subject"`
	Detail    string    `json:"detail,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Summary(ctx context.Context) (*Summary, error) {
	var sum Summary
	cutoff := time.Now().UTC().Add(-24 * time.Hour)

	// Webhook failures
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM webhook_delivery WHERE status = 'failed' AND created_at > $1`, cutoff).
		Scan(&sum.WebhookFailures24h); err != nil {
		return nil, err
	}
	// Email failures
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM email_log WHERE status = 'failed' AND sent_at > $1`, cutoff).
		Scan(&sum.EmailFailures24h); err != nil {
		return nil, err
	}
	// Approvals stuck > 24h
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM approval_request WHERE status = 'pending' AND requested_at < $1`, cutoff).
		Scan(&sum.ApprovalPendingOver24h); err != nil {
		return nil, err
	}
	// Import errors (sum of error_rows across jobs in last 24h)
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(error_rows), 0) FROM import_job WHERE created_at > $1`, cutoff).
		Scan(&sum.ImportErrors24h); err != nil {
		return nil, err
	}

	// Recent failures, all sources, time-ordered. UNION 4 small queries.
	const limit = 25
	rows, err := s.db.Query(ctx, `
		WITH unioned AS (
		  SELECT 'webhook' AS source,
		         event || ' → HTTP ' || coalesce(response_code::text,'?') AS subject,
		         error_message AS detail, created_at AS occurred_at
		  FROM webhook_delivery WHERE status = 'failed'
		  UNION ALL
		  SELECT 'email', subject, error_message, sent_at
		  FROM email_log WHERE status = 'failed'
		  UNION ALL
		  SELECT 'approval', document_name, 'pending ' || age(now(), requested_at)::text, requested_at
		  FROM approval_request WHERE status = 'pending' AND requested_at < $1
		  UNION ALL
		  SELECT 'import', doctype || ' (' || error_rows || ' bad rows)', '', created_at
		  FROM import_job WHERE error_rows > 0
		)
		SELECT source, subject, coalesce(detail,''), occurred_at
		FROM unioned ORDER BY occurred_at DESC LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sum.RecentFailures = make([]FailureRow, 0)
	for rows.Next() {
		var r FailureRow
		if err := rows.Scan(&r.Source, &r.Subject, &r.Detail, &r.OccurredAt); err != nil {
			return nil, err
		}
		sum.RecentFailures = append(sum.RecentFailures, r)
	}
	return &sum, rows.Err()
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "sysinsights-summary",
		Method:      http.MethodGet,
		Path:        "/admin/system/health",
		Summary:     "System health summary",
		Tags:        []string{"Admin / System"},
	}, func(ctx context.Context, _ *struct{}) (*siOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		sum, err := h.Service.Summary(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &siOut{Body: *sum}, nil
	})
}

type siOut struct{ Body Summary }
