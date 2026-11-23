package listingpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/listing"
)

// RoomReader is the read-only view of rooms that listings need: a few facts about a room, taken with the row lock,
// and the costs and pictures a listing shows. It also lists the rooms the demo seed writes listings for.
type RoomReader struct{ pool *pgxpool.Pool }

// NewRoomReader returns a reader on the pool.
func NewRoomReader(pool *pgxpool.Pool) *RoomReader { return &RoomReader{pool: pool} }

// Lock returns the room, a deleted one included, and locks it until the transaction ends.
func (r *RoomReader) Lock(ctx context.Context, id uuid.UUID) (domain.RoomFacts, error) {
	row, err := postgres.Queries(ctx, r.pool).LockRoomForListing(ctx, id)
	if err != nil {
		return domain.RoomFacts{}, postgres.Translate(err)
	}
	return domain.RoomFacts{ID: row.ID, LandlordID: row.LandlordID, Name: row.Name, Live: row.DeletedAt == nil}, nil
}

// Extras returns the costs and the pictures of a room (its building's while it follows them).
func (r *RoomReader) Extras(ctx context.Context, roomID uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error) {
	q := postgres.Queries(ctx, r.pool)
	charges, err := q.ListChargesOfRoomForListing(ctx, &roomID)
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	pictures, err := q.ListPicturesOfRoomForListing(ctx, roomID)
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	cl := make([]domain.ChargeLine, len(charges))
	for i, c := range charges {
		cl[i] = domain.ChargeLine{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note}
	}
	pl := make([]domain.Picture, len(pictures))
	for i, p := range pictures {
		pl[i] = domain.Picture{ID: p.ID, Kind: p.Kind, Key: p.FileKey, ThumbKey: p.ThumbKey, Width: int(p.Width), Height: int(p.Height)}
	}
	return cl, pl, nil
}

// WithoutListing lists the available rooms of the landlord that have no listing yet.
func (r *RoomReader) WithoutListing(ctx context.Context, landlordID uuid.UUID) ([]domain.SeedRoom, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRoomsWithoutListing(ctx, landlordID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.SeedRoom, len(rows))
	for i, row := range rows {
		out[i] = domain.SeedRoom{ID: row.ID, Name: row.Name, Description: row.Description}
	}
	return out, nil
}
