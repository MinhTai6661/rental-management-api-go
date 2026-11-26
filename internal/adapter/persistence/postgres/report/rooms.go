package reportpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/report"
)

// RoomReader is the read-only view of the rooms tenants rented that the report rules need.
type RoomReader struct{ pool *pgxpool.Pool }

// NewRoomReader returns a reader on the pool.
func NewRoomReader(pool *pgxpool.Pool) *RoomReader { return &RoomReader{pool: pool} }

// RentedBy returns the room when the tenant rents it or rented it, else shared.ErrNotFound.
func (r *RoomReader) RentedBy(ctx context.Context, tenantID, roomID uuid.UUID) (domain.RoomRef, error) {
	row, err := postgres.Queries(ctx, r.pool).GetRoomRentedByTenant(ctx, sqlcgen.GetRoomRentedByTenantParams{RoomID: roomID, TenantID: &tenantID})
	if err != nil {
		return domain.RoomRef{}, postgres.Translate(err)
	}
	return domain.RoomRef{ID: row.ID, Name: row.Name, LandlordID: row.LandlordID}, nil
}

// FirstRentedFrom returns a room of the landlord the tenant rents or rented, else shared.ErrNotFound.
func (r *RoomReader) FirstRentedFrom(ctx context.Context, tenantID, landlordID uuid.UUID) (domain.RoomRef, error) {
	row, err := postgres.Queries(ctx, r.pool).GetFirstRoomRentedFromLandlord(ctx, sqlcgen.GetFirstRoomRentedFromLandlordParams{LandlordID: landlordID, TenantID: &tenantID})
	if err != nil {
		return domain.RoomRef{}, postgres.Translate(err)
	}
	return domain.RoomRef{ID: row.ID, Name: row.Name, LandlordID: row.LandlordID}, nil
}
