package managerpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/manager"
)

// RoomReader is the read-only view of rooms that the manager rules need.
type RoomReader struct{ pool *pgxpool.Pool }

// NewRoomReader returns a reader on the pool.
func NewRoomReader(pool *pgxpool.Pool) *RoomReader { return &RoomReader{pool: pool} }

// Lock returns the landlord's live rooms among the ids and locks them until the transaction ends.
func (r *RoomReader) Lock(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.RoomInfo, error) {
	rows, err := postgres.Queries(ctx, r.pool).LockLandlordRoomsForManagers(ctx, sqlcgen.LockLandlordRoomsForManagersParams{LandlordID: landlordID, RoomIds: ids})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomInfo, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomInfo{ID: row.ID, Name: row.Name}
	}
	return out, nil
}

// Names returns the rooms with these ids, deleted ones too.
func (r *RoomReader) Names(ctx context.Context, ids []uuid.UUID) ([]domain.RoomInfo, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRoomNamesByIDs(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomInfo, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomInfo{ID: row.ID, Name: row.Name}
	}
	return out, nil
}

// Roles gives and takes the manager role of an account.
type Roles struct{ pool *pgxpool.Pool }

// NewRoles returns the role adapter on the pool.
func NewRoles(pool *pgxpool.Pool) *Roles { return &Roles{pool: pool} }

// Grant adds the manager role.
func (r *Roles) Grant(ctx context.Context, userID uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).GrantManagerRole(ctx, userID))
}

// Revoke removes the manager role and clears it as the role last used.
func (r *Roles) Revoke(ctx context.Context, userID uuid.UUID) error {
	q := postgres.Queries(ctx, r.pool)
	if err := q.RemoveManagerRole(ctx, userID); err != nil {
		return postgres.Translate(err)
	}
	return postgres.Translate(q.ClearLastManagerRole(ctx, userID))
}
