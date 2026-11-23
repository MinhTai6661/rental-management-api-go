package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Delete deletes an empty building; *domain.BuildingHasRoomsError while it holds rooms.
func (b *Buildings) Delete(ctx context.Context, landlordID, id uuid.UUID) error {
	result, err := b.deleteBuildings(ctx, landlordID, []uuid.UUID{id}, true)
	if err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		return result.Failed[0].Err
	}
	return nil
}

// BulkDelete deletes the empty buildings among ids; the others are reported as failed. An id that is not the
// landlord's building at all is domain.ErrItemsNotFound.
func (b *Buildings) BulkDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) (BulkResult, error) {
	return b.deleteBuildings(ctx, landlordID, ids, false)
}

// deleteBuildings deletes the empty ones; single says the ids are one path id (not found is then
// domain.ErrNotFound, otherwise domain.ErrItemsNotFound).
func (b *Buildings) deleteBuildings(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, single bool) (BulkResult, error) {
	var result BulkResult
	var keys []string
	err := b.tx.WithinTx(ctx, func(ctx context.Context) error {
		rows, err := b.buildings.ToDelete(ctx, landlordID, ids)
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			if single {
				return domain.ErrNotFound
			}
			return domain.ErrItemsNotFound
		}
		var deletable []uuid.UUID
		for _, r := range rows {
			if r.RoomsCount > 0 {
				result.Failed = append(result.Failed, BulkFailure{ID: r.ID, Name: r.Name, Err: &domain.BuildingHasRoomsError{Count: r.RoomsCount}})
				continue
			}
			deletable = append(deletable, r.ID)
		}
		result.Done = deletable
		if len(deletable) == 0 {
			return nil
		}
		if err := b.buildings.SoftDelete(ctx, landlordID, deletable); err != nil {
			return err
		}
		if err := b.charges.DeleteOfBuildings(ctx, deletable); err != nil {
			return err
		}
		keys, err = b.media.DeleteOfBuildings(ctx, deletable)
		return err
	})
	if err != nil {
		return BulkResult{}, err
	}
	b.deleteFiles(ctx, keys)
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}
