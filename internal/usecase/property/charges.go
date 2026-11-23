package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// syncCharges makes the stored charges of one owner equal to the submitted list (see domain.PlanChargeSync); add
// stores the new ones. It reports whether anything actually changed.
func (c *core) syncCharges(ctx context.Context, stored []domain.Charge, submitted []domain.ChargeInput,
	add func([]domain.ChargeCreate) error) (bool, error) {
	rows := make([]domain.ChargeRow, len(stored))
	byID := make(map[uuid.UUID]domain.Charge, len(stored))
	for i, ch := range stored {
		rows[i] = ch.ChargeRow
		byID[ch.ID] = ch
	}
	updates, creates, deletes := domain.PlanChargeSync(rows, submitted)
	changed := len(creates) > 0 || len(deletes) > 0
	for _, u := range updates {
		if !byID[u.ID].SameAs(u.Input, u.Order) {
			changed = true
			if err := c.charges.Update(ctx, u.ID, u.Input, u.Order); err != nil {
				return false, err
			}
		}
	}
	if len(deletes) > 0 {
		if err := c.charges.Delete(ctx, deletes); err != nil {
			return false, err
		}
	}
	return changed, add(creates)
}

// syncBuildingCharges makes the building's charges equal to the submitted list; it reports whether they changed.
func (c *core) syncBuildingCharges(ctx context.Context, buildingID uuid.UUID, submitted []domain.ChargeInput) (bool, error) {
	stored, err := c.charges.OfBuilding(ctx, buildingID)
	if err != nil {
		return false, err
	}
	return c.syncCharges(ctx, stored, submitted, func(items []domain.ChargeCreate) error {
		return c.charges.AddToBuilding(ctx, buildingID, items)
	})
}

// syncRoomCharges makes the room's charges equal to the submitted list; it reports whether they changed.
func (c *core) syncRoomCharges(ctx context.Context, roomID uuid.UUID, submitted []domain.ChargeInput) (bool, error) {
	stored, err := c.charges.OfRoom(ctx, roomID)
	if err != nil {
		return false, err
	}
	return c.syncCharges(ctx, stored, submitted, func(items []domain.ChargeCreate) error {
		return c.charges.AddToRoom(ctx, roomID, items)
	})
}

// follow makes the rooms carry exactly the live charges of the building (nothing to do for no rooms).
func (c *core) follow(ctx context.Context, roomIDs []uuid.UUID, buildingID uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	return c.charges.Follow(ctx, roomIDs, buildingID)
}

// chargesToCreate lists a whole submitted list as creations (a new owner has nothing to update).
func chargesToCreate(items []domain.ChargeInput) []domain.ChargeCreate {
	out := make([]domain.ChargeCreate, len(items))
	for i, item := range items {
		item.ID = nil
		out[i] = domain.ChargeCreate{Input: item, Order: i + 1}
	}
	return out
}
