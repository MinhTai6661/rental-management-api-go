package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Reorder applies a drag and drop: ids are the buildings that moved, in their new relative order.
func (b *Buildings) Reorder(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return b.tx.WithinTx(ctx, func(ctx context.Context) error {
		current, err := b.buildings.Order(ctx, landlordID)
		if err != nil {
			return err
		}
		if err := checkAllListed(current, ids); err != nil {
			return err
		}
		moves := domain.Reorder(current, ids)
		if len(moves) == 0 {
			return nil
		}
		return b.buildings.SetOrder(ctx, landlordID, moves)
	})
}

// checkAllListed refuses ids that are not among the stored rows (domain.ErrItemsNotFound).
func checkAllListed(current []domain.OrderedID, ids []uuid.UUID) error {
	have := make(map[uuid.UUID]bool, len(current))
	for _, r := range current {
		have[r.ID] = true
	}
	for _, id := range ids {
		if !have[id] {
			return domain.ErrItemsNotFound
		}
	}
	return nil
}
