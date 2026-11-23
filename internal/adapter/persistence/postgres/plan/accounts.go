package planpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/plan"
)

// Accounts is the read-only view of the users that the plan rules need (the identity context owns the table).
type Accounts struct{ pool *pgxpool.Pool }

// NewAccounts returns the view on the pool.
func NewAccounts(pool *pgxpool.Pool) *Accounts { return &Accounts{pool: pool} }

// Lock returns the account with the roles it holds and locks it until the transaction ends.
func (a *Accounts) Lock(ctx context.Context, id uuid.UUID) (domain.Account, error) {
	q := postgres.Queries(ctx, a.pool)
	row, err := q.LockAccountForPlan(ctx, id)
	if err != nil {
		return domain.Account{}, postgres.Translate(err)
	}
	roles, err := q.ListRoleCodesOfUser(ctx, id)
	if err != nil {
		return domain.Account{}, postgres.Translate(err)
	}
	return domain.Account{ID: row.ID, Name: row.FullName, Roles: roles}, nil
}

// Usage counts what an account has used of its plan's limits: a read-only view of the rooms, buildings and contracts
// the other contexts own.
type Usage struct{ pool *pgxpool.Pool }

// NewUsage returns the view on the pool.
func NewUsage(pool *pgxpool.Pool) *Usage { return &Usage{pool: pool} }

// Landlord counts the landlord's live rooms and buildings and running contracts with a tenant.
func (u *Usage) Landlord(ctx context.Context, landlordID uuid.UUID) (domain.LandlordUsage, error) {
	q := postgres.Queries(ctx, u.pool)
	rooms, err := q.CountRoomsOfLandlordForPlan(ctx, landlordID)
	if err != nil {
		return domain.LandlordUsage{}, postgres.Translate(err)
	}
	buildings, err := q.CountBuildingsOfLandlordForPlan(ctx, landlordID)
	if err != nil {
		return domain.LandlordUsage{}, postgres.Translate(err)
	}
	contracts, err := q.CountRunningContractsOfLandlordForPlan(ctx, landlordID)
	if err != nil {
		return domain.LandlordUsage{}, postgres.Translate(err)
	}
	return domain.LandlordUsage{Rooms: int(rooms), Buildings: int(buildings), ActiveContracts: int(contracts)}, nil
}

// PendingRegistrations counts the tenant's registrations waiting for an answer.
func (u *Usage) PendingRegistrations(ctx context.Context, tenantID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, u.pool).CountPendingRegistrationsOfTenantForPlan(ctx, &tenantID)
	return int(n), postgres.Translate(err)
}
