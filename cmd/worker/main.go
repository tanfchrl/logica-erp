// Command worker runs background jobs via River (Postgres-backed, no Redis).
// It owns all recurring background work — currently the agent-data retention
// sweep. New deferred jobs are added by registering a Worker in
// internal/platform/jobs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tandigital/logica-erp/internal/config"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/jobs"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := dbx.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("db", "err", err)
		os.Exit(2)
	}
	defer db.Close()

	if err := jobs.Migrate(ctx, db); err != nil {
		logger.Error("worker: river migrate", "err", err)
		os.Exit(2)
	}

	client, err := jobs.NewClient(db, logger, jobs.DefaultRetentionInterval)
	if err != nil {
		logger.Error("worker: river client", "err", err)
		os.Exit(2)
	}

	if err := client.Start(ctx); err != nil {
		logger.Error("worker: river start", "err", err)
		os.Exit(2)
	}
	logger.Info("worker: started; retention sweep scheduled", "interval", jobs.DefaultRetentionInterval.String())

	<-ctx.Done()
	logger.Info("worker: shutting down")
	// Give in-flight jobs a chance to finish on a fresh, non-cancelled context.
	if err := client.Stop(context.Background()); err != nil {
		logger.Warn("worker: river stop", "err", err)
	}
}
