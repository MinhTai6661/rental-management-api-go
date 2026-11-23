// Package db opens the Postgres pool and holds the helpers every module shares.
package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is what Open needs. The timeouts bound every query and lock wait.
type Config struct {
	URL                        string
	MaxConns                   int32
	StatementTimeoutMS         int
	LockTimeoutMS              int
	IdleInTransactionTimeoutMS int
}

// Open creates the connection pool and checks that the database answers.
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pc.MaxConns = cfg.MaxConns
	pc.ConnConfig.RuntimeParams["application_name"] = "rental-management"
	pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(cfg.StatementTimeoutMS)
	pc.ConnConfig.RuntimeParams["lock_timeout"] = strconv.Itoa(cfg.LockTimeoutMS)
	pc.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.Itoa(cfg.IdleInTransactionTimeoutMS)

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// WithTx runs fn in a transaction: it commits when fn returns nil and rolls back on an error or a panic.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, fn)
}

// Postgres error codes of a query that ran out of time or waited too long for a lock.
const (
	sqlStateQueryCanceled = "57014" // statement_timeout (or a cancelled request)
	sqlStateLockTimeout   = "55P03" // lock_timeout
)

// IsBusy reports whether err means the database is overloaded: the API answers 503 "try again", not 500.
func IsBusy(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == sqlStateQueryCanceled || pgErr.Code == sqlStateLockTimeout)
}

// Postgres error codes of a constraint that refused a row.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// IsUniqueViolation reports whether err is a unique constraint refusing a duplicate row.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateUniqueViolation
}

// IsForeignKeyViolation reports whether err is a foreign key pointing at a row that does not exist.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation
}

// IsNotFound reports whether err is "no rows" from a query that expected one.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
