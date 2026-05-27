// Command logica is the operator CLI: migrate, seed, backup, restore, user-add.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/tandigital/logica-erp/internal/config"
)

// Migrations live under ./migrations (mounted into the container).
// We intentionally do NOT embed them so they can be inspected/edited
// without rebuilding the binary in dev.

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "migrate":
		runMigrate(args)
	case "seed":
		runSeed(args)
	case "user-add":
		runUserAdd(args)
	case "backup":
		runBackup(args)
	case "restore":
		runRestore(args)
	case "version":
		fmt.Println("logica 0.1.0")
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Logica ERP — operator CLI

Usage:
  logica migrate up|down|status|reset|version
  logica seed                          bootstrap admin user, demo company, COA
  logica user-add <email> <password>   create an enabled user with system role
  logica backup  <output.sql.gz>       pg_dump → gzipped SQL
  logica restore <input.sql.gz>        gunzip + psql replay
  logica version`)
}

// ---- migrate ----

func runMigrate(args []string) {
	cfg := mustConfig()
	if len(args) == 0 {
		args = []string{"up"}
	}
	sub := args[0]

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	must(err)
	defer db.Close()

	goose.SetBaseFS(os.DirFS("migrations"))
	must(goose.SetDialect("postgres"))

	switch sub {
	case "up":
		must(goose.Up(db, "."))
	case "down":
		must(goose.Down(db, "."))
	case "status":
		must(goose.Status(db, "."))
	case "reset":
		must(goose.Reset(db, "."))
	case "version":
		v, err := goose.GetDBVersion(db)
		must(err)
		fmt.Println(v)
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate subcommand %q\n", sub)
		os.Exit(2)
	}
}

// ---- seed ----

func runSeed(_ []string) {
	cfg := mustConfig()
	ctx := context.Background()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	must(err)
	defer db.Close()

	if err := bootstrap(ctx, db, cfg); err != nil {
		slog.Error("seed", "err", err)
		os.Exit(1)
	}
	fmt.Println("seed complete")
}

// ---- user-add ----

func runUserAdd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: logica user-add <email> <password>")
		os.Exit(2)
	}
	email := strings.ToLower(strings.TrimSpace(args[0]))
	password := args[1]

	cfg := mustConfig()
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	must(err)
	defer db.Close()

	id, err := createUser(context.Background(), db, email, password, true)
	must(err)
	fmt.Println(id)
}

func mustConfig() config.Config {
	c, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	return c
}

func must(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "migration directory not found (expected ./migrations)")
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
