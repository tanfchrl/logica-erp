// Package dbx wraps pgxpool with transaction helpers and the ID generator.
package dbx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	*pgxpool.Pool
}

func Open(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("dbx: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("dbx: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Tx runs fn inside a single transaction; commits on success, rolls back on error.
func (d *DB) Tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("dbx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbx: commit: %w", err)
	}
	return nil
}

// IsUniqueViolation reports whether err is a Postgres unique_violation (23505).
func IsUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

// IsForeignKeyViolation reports whether err is a Postgres foreign_key_violation (23503).
func IsForeignKeyViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23503"
}
