package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// Retention settings — confirmed with the user on 2026-05-27.
const (
	eventRetentionMonths = 36  // doc_event: 36 months back
	viewRetentionDays    = 180 // doc_view:  180 days back
	eventForwardMonths   = 2   // create this many months ahead so a deploy can ride out a maintenance outage
	viewForwardDays      = 7
)

// PartitionManager keeps doc_event and doc_view's monthly/daily range
// partitions aligned with the retention window:
//   - Forward: ensure partitions exist for the upcoming horizon so inserts
//     never hit a gap.
//   - Backward: detach + drop partitions older than the retention window.
//
// Designed to be safe to call repeatedly — every operation is idempotent.
type PartitionManager struct {
	db *dbx.DB
}

func NewPartitionManager(db *dbx.DB) *PartitionManager { return &PartitionManager{db: db} }

// Run starts a daily ticker. Call once from main; cancel via ctx to stop.
func (m *PartitionManager) Run(ctx context.Context) {
	// Run once immediately so a fresh install gets caught up.
	if err := m.Sweep(ctx); err != nil {
		slog.Warn("partman: initial sweep failed", "err", err)
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Sweep(ctx); err != nil {
				slog.Warn("partman: sweep failed", "err", err)
			}
		}
	}
}

// Sweep does a single forward-ensure + retention-prune pass over both tables.
func (m *PartitionManager) Sweep(ctx context.Context) error {
	if err := m.ensureMonthly(ctx, "doc_event", eventForwardMonths); err != nil {
		return fmt.Errorf("ensure doc_event partitions: %w", err)
	}
	if err := m.ensureDaily(ctx, "doc_view", viewForwardDays); err != nil {
		return fmt.Errorf("ensure doc_view partitions: %w", err)
	}
	if err := m.dropMonthlyBefore(ctx, "doc_event", time.Now().AddDate(0, -eventRetentionMonths, 0)); err != nil {
		return fmt.Errorf("drop doc_event partitions: %w", err)
	}
	if err := m.dropDailyBefore(ctx, "doc_view", time.Now().AddDate(0, 0, -viewRetentionDays)); err != nil {
		return fmt.Errorf("drop doc_view partitions: %w", err)
	}
	return nil
}

func (m *PartitionManager) ensureMonthly(ctx context.Context, parent string, forward int) error {
	// Create [thisMonth ... thisMonth+forward] inclusive.
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= forward; i++ {
		lo := thisMonth.AddDate(0, i, 0)
		hi := thisMonth.AddDate(0, i+1, 0)
		suffix := lo.Format("2006_01")
		if err := m.createPartitionIfMissing(ctx, parent, suffix, lo, hi, true); err != nil {
			return err
		}
	}
	return nil
}

func (m *PartitionManager) ensureDaily(ctx context.Context, parent string, forward int) error {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i <= forward; i++ {
		lo := today.AddDate(0, 0, i)
		hi := today.AddDate(0, 0, i+1)
		suffix := lo.Format("2006_01_02")
		if err := m.createPartitionIfMissing(ctx, parent, suffix, lo, hi, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *PartitionManager) createPartitionIfMissing(
	ctx context.Context, parent, suffix string, lo, hi time.Time, monthly bool,
) error {
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
	// Doc lookup index — identical on both tables.
	if _, err := m.db.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX %s_doc_idx ON %s (doctype, document_id, occurred_at DESC)",
		tableName, tableName,
	)); err != nil {
		return err
	}
	// Per-table secondary indexes:
	if parent == "doc_event" {
		if _, err := m.db.Exec(ctx, fmt.Sprintf(
			"CREATE INDEX %s_user_idx ON %s (changed_by, occurred_at DESC)",
			tableName, tableName,
		)); err != nil {
			return err
		}
	} else if parent == "doc_view" {
		if _, err := m.db.Exec(ctx, fmt.Sprintf(
			"CREATE INDEX %s_dedup_idx ON %s (viewed_by, doctype, document_id, occurred_at DESC)",
			tableName, tableName,
		)); err != nil {
			return err
		}
	}
	slog.Info("partman: created partition", "table", tableName, "from", lo, "to", hi)
	return nil
}

func (m *PartitionManager) dropMonthlyBefore(ctx context.Context, parent string, cutoff time.Time) error {
	return m.dropBefore(ctx, parent, cutoff, "2006_01", 7) // YYYY_MM = 7 chars
}

func (m *PartitionManager) dropDailyBefore(ctx context.Context, parent string, cutoff time.Time) error {
	return m.dropBefore(ctx, parent, cutoff, "2006_01_02", 10)
}

// dropBefore enumerates partition tables for `parent` and drops any whose
// suffix-encoded date is strictly before `cutoff`. We DROP rather than
// DETACH because Logica doesn't have an archival tier yet — when one is
// added, swap this for DETACH and move them to an archive schema.
func (m *PartitionManager) dropBefore(ctx context.Context, parent string, cutoff time.Time, layout string, suffixLen int) error {
	prefix := parent + "_"
	rows, err := m.db.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE tablename LIKE $1 ORDER BY tablename`,
		prefix+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	var victims []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		suffix := name[len(prefix):]
		if len(suffix) != suffixLen {
			continue
		}
		t, err := time.Parse(layout, suffix)
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
		slog.Info("partman: dropped retired partition", "table", v, "cutoff", cutoff)
	}
	return nil
}
