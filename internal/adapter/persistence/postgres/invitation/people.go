// Package invitationpg is the database side of the invitation use cases: a read-only view of the accounts an
// invitation can name and the list of addresses that opted out of invitation emails. It returns domain types and
// translates driver errors (see postgres.Translate).
package invitationpg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/invitation"
	"rental-management/pkg/authz"
)

// PeopleRepository reads accounts; the users table belongs to the identity context.
type PeopleRepository struct{ pool *pgxpool.Pool }

// NewPeopleRepository returns a repository on the pool.
func NewPeopleRepository(pool *pgxpool.Pool) *PeopleRepository { return &PeopleRepository{pool: pool} }

// ByID returns the account, or shared.ErrNotFound.
func (r *PeopleRepository) ByID(ctx context.Context, id uuid.UUID) (domain.Person, error) {
	row, err := postgres.Queries(ctx, r.pool).GetInviteeByID(ctx, id)
	if err != nil {
		return domain.Person{}, postgres.Translate(err)
	}
	return domain.Person{
		ID: row.ID, FullName: row.FullName, Email: row.Email, EmailVerified: row.EmailVerifiedAt != nil,
		Active: row.Status == authz.StatusActive, AcceptsInvitations: row.AcceptsInvitations, Roles: row.Roles,
	}, nil
}

// ByEmail returns the account with the normalized address, or shared.ErrNotFound.
func (r *PeopleRepository) ByEmail(ctx context.Context, normalized string) (domain.Person, error) {
	row, err := postgres.Queries(ctx, r.pool).GetInviteeByEmail(ctx, normalized)
	if err != nil {
		return domain.Person{}, postgres.Translate(err)
	}
	return domain.Person{
		ID: row.ID, FullName: row.FullName, Email: row.Email, EmailVerified: row.EmailVerifiedAt != nil,
		Active: row.Status == authz.StatusActive, AcceptsInvitations: row.AcceptsInvitations, Roles: row.Roles,
	}, nil
}

// OptedOut says whether an account with the address switched invitations off.
func (r *PeopleRepository) OptedOut(ctx context.Context, normalized string) (bool, error) {
	out, err := postgres.Queries(ctx, r.pool).InviteeEmailOptedOut(ctx, normalized)
	return out, postgres.Translate(err)
}

// EmailBlockRepository remembers the addresses that must not be mailed, by hash.
type EmailBlockRepository struct{ pool *pgxpool.Pool }

// NewEmailBlockRepository returns a repository on the pool.
func NewEmailBlockRepository(pool *pgxpool.Pool) *EmailBlockRepository {
	return &EmailBlockRepository{pool: pool}
}

// Add stores the hash; storing it again changes nothing.
func (r *EmailBlockRepository) Add(ctx context.Context, hash string) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AddInvitationEmailBlock(ctx, hash))
}

// Exists says whether the hash is stored.
func (r *EmailBlockRepository) Exists(ctx context.Context, hash string) (bool, error) {
	ok, err := postgres.Queries(ctx, r.pool).InvitationEmailBlockExists(ctx, hash)
	return ok, postgres.Translate(err)
}
