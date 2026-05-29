// Package jobs wires River (Postgres-backed background jobs) for cmd/worker.
//
// The worker is the single owner of recurring background work. Today that is
// one periodic job — the agent-data retention sweep — but the client is set up
// so future deferred work (scheduled depreciation posting, tax-invoice number
// reservation, etc.) only needs a new Worker registered here.
//
// River keeps its own schema in the same Postgres database. Migrate() applies
// it idempotently at worker boot, so River tables are managed by River's own
// migrator rather than the project's goose migrations — the two never touch
// the same tables.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/tandigital/logica-erp/internal/agent/retention"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// DefaultRetentionInterval is how often the retention sweep runs. The sweep is
// cheap and idempotent, so a daily cadence is plenty.
const DefaultRetentionInterval = 24 * time.Hour

// RetentionArgs is the periodic agent-data retention sweep: it ages out
// agent sessions, audit-log partitions, resolved approvals, and dismissed
// nudges past their configured windows. See internal/agent/retention.
type RetentionArgs struct{}

// Kind is River's stable job identifier — persisted on every row, so it must
// not change once jobs exist in the queue.
func (RetentionArgs) Kind() string { return "agent_retention_sweep" }

type retentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	db *dbx.DB
}

func (w *retentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	return retention.New(w.db).Sweep(ctx)
}

// Migrate applies River's own schema to the database. Idempotent — safe to run
// on every worker boot.
func Migrate(ctx context.Context, db *dbx.DB) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(db.Pool), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

// NewClient builds the worker-side River client with every job registered and
// the recurring periodic jobs scheduled. Call Start on the returned client to
// begin working the queue.
func NewClient(db *dbx.DB, logger *slog.Logger, retentionInterval time.Duration) (*river.Client[pgx.Tx], error) {
	if retentionInterval <= 0 {
		retentionInterval = DefaultRetentionInterval
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &retentionWorker{db: db})

	return river.NewClient(riverpgxv5.New(db.Pool), &river.Config{
		Logger:  logger,
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 2},
		},
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(retentionInterval),
				func() (river.JobArgs, *river.InsertOpts) {
					return RetentionArgs{}, nil
				},
				// RunOnStart catches up a fresh deploy immediately, matching the
				// old inline ticker's startup behaviour.
				&river.PeriodicJobOpts{ID: "agent_retention_sweep", RunOnStart: true},
			),
		},
	})
}
