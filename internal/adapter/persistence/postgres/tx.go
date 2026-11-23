// Package postgres is the only place that talks to the database (through the generated sqlc package). Its
// repositories take and return domain types, never sqlc rows.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
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
