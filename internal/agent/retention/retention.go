// Package retention is the agent service's daily housekeeping ticker.
//
// Four tables get aged out at independent windows:
//
//	agent_session         closed (or stale-open) > N days   default 90
//	agent_audit_log       monthly partitions > N months     default 12
//	                      (forward partitions also created)
//	agent_approval_queue  resolved (approved/rejected/expired) > N days  default 90
//	agent_nudge           dismissed > N days                default 30
//
// All four cap at the most-permissive interpretation of spec §11. The audit
// retention is intentionally longer than the conversation retention — token
// cost reporting (#33) needs a wider window than the chat history does.
//
// Each tunable is an env var so an operator can keep things longer for
// compliance without recompiling.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// defaults match what spec §11 calls out + what feels right for the cost
// dashboard's reporting window.
const (
	defaultSessionDays      = 90
	defaultAuditMonths      = 12
	defaultApprovalDays     = 90
	defaultNudgeDays        = 30
	defaultForwardMonths    = 2 // partitions to keep created ahead of "now"
)

// Manager runs the daily sweep. Construct once in main, kick off with Run.
type Manager struct {
	db *dbx.DB

	sessionDays   int
	auditMonths   int
	approvalDays  int
	nudgeDays     int
	forwardMonths int
}

func New(db *dbx.DB) *Manager {
	return &Manager{
		db:            db,
		sessionDays:   envInt("AGENT_RETENTION_SESSION_DAYS",   defaultSessionDays),
		auditMonths:   envInt("AGENT_RETENTION_AUDIT_MONTHS",   defaultAuditMonths),
		approvalDays:  envInt("AGENT_RETENTION_APPROVAL_DAYS",  defaultApprovalDays),
		nudgeDays:     envInt("AGENT_RETENTION_NUDGE_DAYS",     defaultNudgeDays),
		forwardMonths: envInt("AGENT_RETENTION_FORWARD_MONTHS", defaultForwardMonths),
	}
}

// Run starts the daily ticker. Cancel ctx to stop. Runs one sweep immediately
// at startup so a fresh deploy catches up on whatever sat across the gap.
func (m *Manager) Run(ctx context.Context) {
	if err := m.Sweep(ctx); err != nil {
		slog.Warn("agent retention: initial sweep failed", "err", err)
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Sweep(ctx); err != nil {
				slog.Warn("agent retention: sweep failed", "err", err)
			}
		}
	}
}

// Sweep is one pass across all four tables. Errors per table are logged but
// don't stop other tables from being processed — partial success is fine.
func (m *Manager) Sweep(ctx context.Context) error {
	if n, err := m.purgeSessions(ctx); err != nil {
		slog.Warn("agent retention: purgeSessions", "err", err)
	} else if n > 0 {
		slog.Info("agent retention: purged sessions", "count", n, "older_than_days", m.sessionDays)
	}

	if err := m.maintainAuditPartitions(ctx); err != nil {
		slog.Warn("agent retention: audit partitions", "err", err)
	}

	if n, err := m.purgeApprovals(ctx); err != nil {
		slog.Warn("agent retention: purgeApprovals", "err", err)
	} else if n > 0 {
		slog.Info("agent retention: purged resolved approvals", "count", n, "older_than_days", m.approvalDays)
	}

	if n, err := m.purgeNudges(ctx); err != nil {
		slog.Warn("agent retention: purgeNudges", "err", err)
	} else if n > 0 {
		slog.Info("agent retention: purged dismissed nudges", "count", n, "older_than_days", m.nudgeDays)
	}
	return nil
}

// purgeSessions removes agent_session rows older than `sessionDays`.
// A session counts as "old" if it's been closed for the window OR — for
// abandoned sessions that never got Close()d — its last update is older
// than the window. Conversation rows cascade via FK.
func (m *Manager) purgeSessions(ctx context.Context) (int64, error) {
	cmd, err := m.db.Exec(ctx, `
		DELETE FROM agent_session
		WHERE
		  (closed_at IS NOT NULL AND closed_at < now() - make_interval(days => $1))
		  OR
		  (closed_at IS NULL     AND updated_at < now() - make_interval(days => $1))`,
		m.sessionDays)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// purgeApprovals drops resolved/expired queue entries past the window.
// `pending` rows are never auto-purged — a pending draft is the human's
// open work item; leaving it alive forever is correct.
func (m *Manager) purgeApprovals(ctx context.Context) (int64, error) {
	cmd, err := m.db.Exec(ctx, `
		DELETE FROM agent_approval_queue
		WHERE status <> 'pending'
		  AND resolved_at IS NOT NULL
		  AND resolved_at < now() - make_interval(days => $1)`,
		m.approvalDays)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// purgeNudges drops dismissed nudges past the window. Active (undismissed)
// nudges are left alone — they're still surfaced in the UI.
func (m *Manager) purgeNudges(ctx context.Context) (int64, error) {
	cmd, err := m.db.Exec(ctx, `
		DELETE FROM agent_nudge
		WHERE dismissed_at IS NOT NULL
		  AND dismissed_at < now() - make_interval(days => $1)`,
		m.nudgeDays)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// maintainAuditPartitions: same pattern as platform/audit.PartitionManager
// but scoped to agent_audit_log. Creates forward partitions, drops
// past-retention ones. Idempotent.
func (m *Manager) maintainAuditPartitions(ctx context.Context) error {
	if err := m.ensureMonthly(ctx, "agent_audit_log", m.forwardMonths); err != nil {
		return fmt.Errorf("ensure agent_audit_log partitions: %w", err)
	}
	cutoff := time.Now().UTC().AddDate(0, -m.auditMonths, 0)
	if err := m.dropMonthlyBefore(ctx, "agent_audit_log", cutoff); err != nil {
		return fmt.Errorf("drop agent_audit_log partitions: %w", err)
	}
	return nil
}

func (m *Manager) ensureMonthly(ctx context.Context, parent string, forward int) error {
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= forward; i++ {
		lo := thisMonth.AddDate(0, i, 0)
		hi := thisMonth.AddDate(0, i+1, 0)
		suffix := lo.Format("2006_01")
		if err := m.createPartitionIfMissing(ctx, parent, suffix, lo, hi); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) createPartitionIfMissing(ctx context.Context, parent, suffix string, lo, hi time.Time) error {
	tableName := parent + "_" + suffix
	var exists bool
	if err := m.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename = $1)`, tableName,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := m.db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
		tableName, parent, lo.Format(time.RFC3339), hi.Format(time.RFC3339),
	)); err != nil {
		return err
	}
	// Same indexes the original migration creates per partition.
	if _, err := m.db.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX %s_session_idx ON %s (session_id, turn, created_at)",
		tableName, tableName,
	)); err != nil {
		return err
	}
	if _, err := m.db.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX %s_user_idx ON %s (user_id, created_at DESC)",
		tableName, tableName,
	)); err != nil {
		return err
	}
	slog.Info("agent retention: created partition", "table", tableName, "from", lo, "to", hi)
	return nil
}

// dropMonthlyBefore enumerates `parent`'s YYYY_MM partitions and drops the
// ones whose date is strictly before cutoff. Mirrors the doc_event partman
// pattern; consolidating these into a single shared helper is on the
// follow-up backlog.
func (m *Manager) dropMonthlyBefore(ctx context.Context, parent string, cutoff time.Time) error {
	rows, err := m.db.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE tablename LIKE $1 ORDER BY tablename`,
		parent+"_%")
	if err != nil {
		return err
	}
	defer rows.Close()
	var victims []string
	prefix := parent + "_"
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		suffix := name[len(prefix):]
		if len(suffix) != 7 { // YYYY_MM
			continue
		}
		t, err := time.Parse("2006_01", suffix)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			victims = append(victims, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range victims {
		if _, err := m.db.Exec(ctx, "DROP TABLE "+v); err != nil {
			return fmt.Errorf("drop %s: %w", v, err)
		}
		slog.Info("agent retention: dropped retired partition", "table", v, "cutoff", cutoff)
	}
	return nil
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
