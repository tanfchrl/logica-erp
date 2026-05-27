// Command worker runs background jobs (River). Phase 0 ships an idle worker —
// jobs are registered as later phases need them.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tandigital/logica-erp/internal/config"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
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

	logger.Info("worker: idle (no jobs registered yet)")
	<-ctx.Done()
	logger.Info("worker: shutting down")
}
