package identitypg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/identity"
)

// OAuthAccountRepository stores which accounts of other services (Google, Zalo) open which accounts here.
type OAuthAccountRepository struct{ pool *pgxpool.Pool }

// NewOAuthAccountRepository returns a repository on the pool.
func NewOAuthAccountRepository(pool *pgxpool.Pool) *OAuthAccountRepository {
	return &OAuthAccountRepository{pool: pool}
}

// FindByProvider returns the row of a provider account (linked or not), or shared.ErrNotFound.
func (r *OAuthAccountRepository) FindByProvider(ctx context.Context, provider, providerUserID string) (domain.OAuthAccount, error) {
	row, err := postgres.Queries(ctx, r.pool).GetOAuthAccountByProvider(ctx, sqlcgen.GetOAuthAccountByProviderParams{
		Provider: provider, ProviderUserID: providerUserID,
	})
	if err != nil {
		return domain.OAuthAccount{}, postgres.Translate(err)
	}
	return oauthAccountOf(row.UserID, row.Provider, row.ProviderUserID, row.LinkedAt, row.UnlinkedAt), nil
}

// ListOfUser returns every provider account of the account, linked or not.
func (r *OAuthAccountRepository) ListOfUser(ctx context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListOAuthAccountsOfUser(ctx, userID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.OAuthAccount, len(rows))
	for i, row := range rows {
		out[i] = oauthAccountOf(row.UserID, row.Provider, row.ProviderUserID, row.LinkedAt, row.UnlinkedAt)
	}
	return out, nil
}

// ListOfUserForUpdate is ListOfUser that locks the rows until the transaction ends.
func (r *OAuthAccountRepository) ListOfUserForUpdate(ctx context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListOAuthAccountsOfUserForUpdate(ctx, userID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.OAuthAccount, len(rows))
	for i, row := range rows {
		out[i] = oauthAccountOf(row.UserID, row.Provider, row.ProviderUserID, row.LinkedAt, row.UnlinkedAt)
	}
	return out, nil
}

// Create adds a linked row. A provider account (or a provider of the account) already on record is a unique ConstraintError.
func (r *OAuthAccountRepository) Create(ctx context.Context, a domain.OAuthAccount, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).CreateOAuthAccount(ctx, sqlcgen.CreateOAuthAccountParams{
		UserID: a.UserID, Provider: a.Provider, ProviderUserID: a.ProviderUserID, Now: now,
	}))
}

// Relink switches an unlinked row back on.
func (r *OAuthAccountRepository) Relink(ctx context.Context, userID uuid.UUID, provider string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).RelinkOAuthAccount(ctx, sqlcgen.RelinkOAuthAccountParams{
		UserID: userID, Provider: provider, Now: now,
	}))
}

// Replace points an unlinked row at another provider account and links it. A provider account already linked to
// another account is a unique ConstraintError.
func (r *OAuthAccountRepository) Replace(ctx context.Context, userID uuid.UUID, provider, providerUserID string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).ReplaceOAuthAccount(ctx, sqlcgen.ReplaceOAuthAccountParams{
		UserID: userID, Provider: provider, ProviderUserID: providerUserID, Now: now,
	}))
}

// Unlink marks the row unlinked; the row stays.
func (r *OAuthAccountRepository) Unlink(ctx context.Context, userID uuid.UUID, provider string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UnlinkOAuthAccount(ctx, sqlcgen.UnlinkOAuthAccountParams{
		UserID: userID, Provider: provider, Now: now,
	}))
}

func oauthAccountOf(userID uuid.UUID, provider, providerUserID string, linkedAt time.Time, unlinkedAt *time.Time) domain.OAuthAccount {
	return domain.OAuthAccount{
		UserID: userID, Provider: provider, ProviderUserID: providerUserID, LinkedAt: linkedAt, UnlinkedAt: unlinkedAt,
	}
}
