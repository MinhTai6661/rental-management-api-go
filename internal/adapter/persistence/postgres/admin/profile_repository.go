package adminpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/admin"
)

// ProfileRepository stores the landlords' business profiles and measures what a landlord has.
type ProfileRepository struct{ pool *pgxpool.Pool }

// NewProfileRepository returns a repository on the pool.
func NewProfileRepository(pool *pgxpool.Pool) *ProfileRepository {
	return &ProfileRepository{pool: pool}
}

// Get returns the profile of a landlord, or shared.ErrNotFound.
func (r *ProfileRepository) Get(ctx context.Context, userID uuid.UUID) (domain.Profile, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminGetLandlordProfile(ctx, userID)
	if err != nil {
		return domain.Profile{}, postgres.Translate(err)
	}
	return domain.Profile{
		UserID: v.UserID, BusinessName: v.BusinessName, Phone: v.Phone, WardCode: v.WardCode,
		AddressDetail: v.AddressDetail, Description: v.Description, UpdatedAt: v.UpdatedAt,
	}, nil
}

// Upsert writes the profile, creating it when there is none.
func (r *ProfileRepository) Upsert(ctx context.Context, userID uuid.UUID, in domain.ProfileInput, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminUpsertLandlordProfile(ctx, sqlcgen.AdminUpsertLandlordProfileParams{
		UserID: userID, BusinessName: in.BusinessName, Phone: in.Phone, WardCode: in.WardCode,
		AddressDetail: in.AddressDetail, Description: in.Description, Now: now,
	}))
}

// InsertIfAbsent creates the profile unless the landlord has one.
func (r *ProfileRepository) InsertIfAbsent(ctx context.Context, userID uuid.UUID, in domain.ProfileInput, now time.Time) error {
	_, err := postgres.Queries(ctx, r.pool).AdminInsertLandlordProfileIfAbsent(ctx, sqlcgen.AdminInsertLandlordProfileIfAbsentParams{
		UserID: userID, BusinessName: in.BusinessName, Phone: in.Phone, WardCode: in.WardCode, AddressDetail: in.AddressDetail, Now: now,
	})
	return postgres.Translate(err)
}

// Impact counts what the landlord has: rooms, running contracts, registrations waiting and unpaid bills.
func (r *ProfileRepository) Impact(ctx context.Context, landlordID uuid.UUID) (domain.Impact, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminLandlordImpact(ctx, landlordID)
	return domain.Impact{
		Rooms: int(v.RoomCount), ActiveContracts: int(v.ActiveContracts),
		PendingRegistrations: int(v.PendingRegistrations), UnpaidInvoices: int(v.UnpaidInvoices),
	}, postgres.Translate(err)
}
