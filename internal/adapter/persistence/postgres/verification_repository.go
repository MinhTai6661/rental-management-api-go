package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/identity"
)

// VerificationRepository stores email verification tokens (hashes only).
type VerificationRepository struct{ pool *pgxpool.Pool }

// NewVerificationRepository returns a repository on the pool.
func NewVerificationRepository(pool *pgxpool.Pool) *VerificationRepository {
	return &VerificationRepository{pool: pool}
}

// Insert stores a token.
func (r *VerificationRepository) Insert(ctx context.Context, t domain.NewVerificationToken, now time.Time) error {
	return translate(queries(ctx, r.pool).InsertVerificationToken(ctx, sqlcgen.InsertVerificationTokenParams{
		ID: t.ID, UserID: t.UserID, TokenHash: t.Hash, ExpiresAt: t.ExpiresAt, Now: now,
	}))
}

// InvalidateForUser marks every unused token of the user as used, so older links stop working.
func (r *VerificationRepository) InvalidateForUser(ctx context.Context, userID uuid.UUID, now time.Time) error {
	return translate(queries(ctx, r.pool).InvalidateVerificationTokens(ctx, sqlcgen.InvalidateVerificationTokensParams{UserID: userID, Now: now}))
}

// ForUpdate returns the token and locks its row until the transaction ends; shared.ErrNotFound when unknown.
func (r *VerificationRepository) ForUpdate(ctx context.Context, hash []byte) (domain.VerificationToken, error) {
	row, err := queries(ctx, r.pool).GetVerificationTokenForUpdate(ctx, hash)
	if err != nil {
		return domain.VerificationToken{}, translate(err)
	}
	return domain.VerificationToken{ID: row.ID, UserID: row.UserID, ExpiresAt: row.ExpiresAt, UsedAt: row.UsedAt}, nil
}

// MarkUsed records that the token was used.
func (r *VerificationRepository) MarkUsed(ctx context.Context, id uuid.UUID, now time.Time) error {
	return translate(queries(ctx, r.pool).MarkVerificationTokenUsed(ctx, sqlcgen.MarkVerificationTokenUsedParams{ID: id, Now: now}))
}

// LatestCreatedAt returns when the latest token of the user was made; shared.ErrNotFound when there is none.
func (r *VerificationRepository) LatestCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	at, err := queries(ctx, r.pool).LatestVerificationTokenAt(ctx, userID)
	return at, translate(err)
}
