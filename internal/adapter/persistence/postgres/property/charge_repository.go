package propertypg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/property"
)

// ChargeRepository reads and writes the charges of buildings and rooms.
type ChargeRepository struct{ pool *pgxpool.Pool }

// NewChargeRepository returns a repository on the pool.
func NewChargeRepository(pool *pgxpool.Pool) *ChargeRepository { return &ChargeRepository{pool: pool} }

// OfBuilding lists the live charges of a building in order.
func (r *ChargeRepository) OfBuilding(ctx context.Context, buildingID uuid.UUID) ([]domain.Charge, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListChargesOfBuildings(ctx, []uuid.UUID{buildingID})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Charge, len(rows))
	for i, c := range rows {
		out[i] = domain.Charge{
			ChargeRow: domain.ChargeRow{ID: c.ID, TemplateID: c.BuildingChargeID}, Name: c.Name, Kind: c.Kind,
			AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note, Order: int(c.DisplayOrder),
		}
	}
	return out, nil
}

// OfRoom lists the live charges of a room in order.
func (r *ChargeRepository) OfRoom(ctx context.Context, roomID uuid.UUID) ([]domain.Charge, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListChargesOfRooms(ctx, []uuid.UUID{roomID})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Charge, len(rows))
	for i, c := range rows {
		out[i] = domain.Charge{
			ChargeRow: domain.ChargeRow{ID: c.ID, TemplateID: c.BuildingChargeID}, Name: c.Name, Kind: c.Kind,
			AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note, Order: int(c.DisplayOrder),
		}
	}
	return out, nil
}

// AddToBuilding adds charges to a building in the order given.
func (r *ChargeRepository) AddToBuilding(ctx context.Context, buildingID uuid.UUID, items []domain.ChargeCreate) error {
	q := postgres.Queries(ctx, r.pool)
	for _, c := range items {
		err := q.InsertBuildingCharge(ctx, sqlcgen.InsertBuildingChargeParams{
			BuildingID: &buildingID, Name: c.Input.Name, Kind: c.Input.Kind, AmountVnd: c.Input.AmountVND,
			Unit: c.Input.Unit, Note: c.Input.Note, DisplayOrder: int16(c.Order), //nolint:gosec // at most 30 items
		})
		if err != nil {
			return postgres.Translate(err)
		}
	}
	return nil
}

// AddToRoom adds own charges to a room in the order given.
func (r *ChargeRepository) AddToRoom(ctx context.Context, roomID uuid.UUID, items []domain.ChargeCreate) error {
	q := postgres.Queries(ctx, r.pool)
	for _, c := range items {
		err := q.InsertRoomCharge(ctx, sqlcgen.InsertRoomChargeParams{
			RoomID: &roomID, Name: c.Input.Name, Kind: c.Input.Kind, AmountVnd: c.Input.AmountVND,
			Unit: c.Input.Unit, Note: c.Input.Note, DisplayOrder: int16(c.Order), //nolint:gosec // at most 30 items
		})
		if err != nil {
			return postgres.Translate(err)
		}
	}
	return nil
}

// AddToRooms gives every room the same own charges.
func (r *ChargeRepository) AddToRooms(ctx context.Context, roomIDs []uuid.UUID, items []domain.ChargeInput) error {
	if len(roomIDs) == 0 || len(items) == 0 {
		return nil
	}
	p := sqlcgen.InsertChargesForRoomsParams{RoomIds: roomIDs}
	for i, c := range items {
		p.Names = append(p.Names, c.Name)
		p.Kinds = append(p.Kinds, c.Kind)
		p.Amounts = append(p.Amounts, c.AmountVND)
		p.Units = append(p.Units, c.Unit)
		note := ""
		if c.Note != nil {
			note = *c.Note
		}
		p.Notes = append(p.Notes, note)
		p.Orders = append(p.Orders, int16(i+1)) //nolint:gosec // at most 30 items
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).InsertChargesForRooms(ctx, p))
}

// Update replaces the values and position of a charge.
func (r *ChargeRepository) Update(ctx context.Context, id uuid.UUID, in domain.ChargeInput, order int) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateCharge(ctx, sqlcgen.UpdateChargeParams{
		ID: id, Name: in.Name, Kind: in.Kind, AmountVnd: in.AmountVND, Unit: in.Unit, Note: in.Note,
		DisplayOrder: int16(order), //nolint:gosec // at most 30 items
	}))
}

// Delete marks charges deleted.
func (r *ChargeRepository) Delete(ctx context.Context, ids []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SoftDeleteCharges(ctx, ids))
}

// DeleteOfBuildings marks every charge of the buildings deleted.
func (r *ChargeRepository) DeleteOfBuildings(ctx context.Context, buildingIDs []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SoftDeleteChargesOfBuildings(ctx, buildingIDs))
}

// Follow makes the rooms carry exactly the live charges of the building: copies of its templates, updated in
// place when a template changed, dropped when it is gone, added when it is new; a room's own charges go (three set
// statements, so any number of rooms costs three queries).
func (r *ChargeRepository) Follow(ctx context.Context, roomIDs []uuid.UUID, buildingID uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	q := postgres.Queries(ctx, r.pool)
	if err := q.UpdateChargeCopiesFromTemplates(ctx, sqlcgen.UpdateChargeCopiesFromTemplatesParams{RoomIds: roomIDs, BuildingID: &buildingID}); err != nil {
		return postgres.Translate(err)
	}
	if err := q.DeleteChargeCopiesWithoutTemplate(ctx, sqlcgen.DeleteChargeCopiesWithoutTemplateParams{RoomIds: roomIDs, BuildingID: &buildingID}); err != nil {
		return postgres.Translate(err)
	}
	return postgres.Translate(q.InsertMissingChargeCopies(ctx, sqlcgen.InsertMissingChargeCopiesParams{RoomIds: roomIDs, BuildingID: &buildingID}))
}

// Detach turns the copies a room holds into charges of its own.
func (r *ChargeRepository) Detach(ctx context.Context, roomIDs []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).DetachRoomCharges(ctx, roomIDs))
}

// CopyOwn gives the new rooms copies of the own charges of the source rooms.
func (r *ChargeRepository) CopyOwn(ctx context.Context, newIDs, sourceIDs []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).CopyOwnCharges(ctx, sqlcgen.CopyOwnChargesParams{NewIds: newIDs, SourceIds: sourceIDs}))
}
