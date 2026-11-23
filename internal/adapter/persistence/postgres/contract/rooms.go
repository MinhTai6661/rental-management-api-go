package contractpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/contract"
)

// RoomReader is the read-only view of rooms that contracts need: a few facts about a room, taken with the row lock.
type RoomReader struct{ pool *pgxpool.Pool }

// NewRoomReader returns a reader on the pool.
func NewRoomReader(pool *pgxpool.Pool) *RoomReader { return &RoomReader{pool: pool} }

// Lock returns the room, a deleted one included, and locks it until the transaction ends.
func (r *RoomReader) Lock(ctx context.Context, id uuid.UUID) (domain.RoomFacts, error) {
	row, err := postgres.Queries(ctx, r.pool).LockRoomForContract(ctx, id)
	if err != nil {
		return domain.RoomFacts{}, postgres.Translate(err)
	}
	return roomFactsOf(row.ID, row.LandlordID, row.Name, row.Status, row.PriceVnd, row.MinimumRentalPeriod, row.RentalPeriodUnit, row.DeletedAt != nil, row.Listed), nil
}

// FindByName returns a live room of the landlord by name (any case), without locking it.
func (r *RoomReader) FindByName(ctx context.Context, landlordID uuid.UUID, name string) (domain.RoomFacts, error) {
	row, err := postgres.Queries(ctx, r.pool).FindRoomByNameForContract(ctx, sqlcgen.FindRoomByNameForContractParams{LandlordID: landlordID, Name: name})
	if err != nil {
		return domain.RoomFacts{}, postgres.Translate(err)
	}
	return roomFactsOf(row.ID, row.LandlordID, row.Name, row.Status, row.PriceVnd, row.MinimumRentalPeriod, row.RentalPeriodUnit, row.DeletedAt != nil, row.Listed), nil
}
