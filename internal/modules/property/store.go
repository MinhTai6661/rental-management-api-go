package property

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/modules/property/sqlcgen"
	"rental-management/internal/platform/db"
)

// Store gives the service its queries, plain or inside one transaction.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

// NewStore returns a store on the pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlcgen.New(pool)} }

// Queries are the plain (non-transactional) queries.
func (s *Store) Queries() *sqlcgen.Queries { return s.q }

// InTx runs fn with queries bound to one transaction: it commits when fn returns nil, rolls back otherwise. The
// transaction itself is handed over too, for the ports that act in it.
func (s *Store) InTx(ctx context.Context, fn func(tx pgx.Tx, q *sqlcgen.Queries) error) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(tx, s.q.WithTx(tx)) })
}

// lockLandlord serialises the transactions that count a landlord's things against a plan limit.
func lockLandlord(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID) error {
	return q.LockLandlord(ctx, landlordID.String())
}
