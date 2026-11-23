// Package repository is the only place that talks to the database (through the generated sqlc package) and to
// Redis. Repositories take and return model types, never sqlc rows.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/model"
	sqlcgen "rental-management/internal/repository/sqlc"
	"rental-management/pkg/db"
)

// txKey is the context key of the transaction WithinTx opens.
type txKey struct{}

// TxManager runs a function in one database transaction. The transaction travels in the context, so every
// repository call made with that context joins it without the service knowing about pgx.
type TxManager struct{ pool *pgxpool.Pool }

// NewTxManager returns a manager on the pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// WithinTx runs fn in a transaction: it commits when fn returns nil and rolls back on an error or a panic. When
// ctx already carries a transaction, fn simply runs in it (no nesting, the outer call decides).
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	err := db.WithTx(ctx, m.pool, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
	return translate(err)
}

// queries returns the generated queries bound to the transaction of ctx, or to the pool when there is none. Every
// repository method gets its queries here, which is what makes it join the caller's transaction.
func queries(ctx context.Context, pool *pgxpool.Pool) *sqlcgen.Queries {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return sqlcgen.New(tx)
	}
	return sqlcgen.New(pool)
}

// Postgres error codes of a constraint that refused a row.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// translate turns a driver error into a domain error: no rows becomes model.ErrNotFound, a unique or foreign key
// violation a *model.ConstraintError. Any other error is returned as it is.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			return &model.ConstraintError{Kind: model.Unique, Constraint: pgErr.ConstraintName, Err: err}
		case sqlStateForeignKeyViolation:
			return &model.ConstraintError{Kind: model.ForeignKey, Constraint: pgErr.ConstraintName, Err: err}
		}
	}
	return err
}
