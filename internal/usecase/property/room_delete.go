package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Delete deletes a room; the error says what blocks it (a running contract, an open listing).
func (r *Rooms) Delete(ctx context.Context, landlordID, id uuid.UUID) error {
	result, err := r.deleteRooms(ctx, landlordID, []uuid.UUID{id}, true)
	if err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		return result.Failed[0].Err
	}
	return nil
}

// BulkDelete deletes the rooms among ids that nothing blocks; the others are reported as failed. An id that is not
// the landlord's room at all is domain.ErrItemsNotFound.
func (r *Rooms) BulkDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) (BulkResult, error) {
	return r.deleteRooms(ctx, landlordID, ids, false)
}

// deleteRooms deletes what nothing blocks; single says the ids are one path id (not found is then
// domain.ErrNotFound, otherwise domain.ErrItemsNotFound).
func (r *Rooms) deleteRooms(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, single bool) (BulkResult, error) {
	var result BulkResult
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		rows, err := r.rooms.ForUpdateMany(ctx, landlordID, ids)
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return missingRooms(single)
		}
		blockers, err := r.guards.Blockers(ctx, ids)
		if err != nil {
			return err
		}
		var deletable []uuid.UUID
		for _, row := range rows {
			if err := domain.CheckDelete(blockers[row.ID]); err != nil {
				result.Failed = append(result.Failed, BulkFailure{ID: row.ID, Name: row.Name, Err: err})
				continue
			}
			deletable = append(deletable, row.ID)
		}
		result.Done = deletable
		if len(deletable) == 0 {
			return nil
		}
		return r.rooms.SoftDelete(ctx, landlordID, deletable)
	})
	if err != nil {
		return BulkResult{}, err
	}
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}

// missingRooms is the answer for ids that are not all the landlord's: not found for a path id, ids not found for a list.
func missingRooms(single bool) error {
	if single {
		return domain.ErrNotFound
	}
	return domain.ErrItemsNotFound
}

// BulkSetStatus moves the rooms among ids to a status; rooms a running contract keeps rented are reported as
// failed, rooms already in that status count as done.
func (r *Rooms) BulkSetStatus(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, to domain.Status) (BulkResult, error) {
	var result BulkResult
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		rows, err := r.rooms.ForUpdateMany(ctx, landlordID, ids)
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return missingRooms(false)
		}
		blockers, err := r.guards.Blockers(ctx, ids)
		if err != nil {
			return err
		}
		var moving []uuid.UUID
		var changes []domain.StatusChange
		for _, row := range rows {
			if row.Status == to {
				result.Done = append(result.Done, row.ID)
				continue
			}
			if err := domain.CheckStatusChange(to, blockers[row.ID]); err != nil {
				result.Failed = append(result.Failed, BulkFailure{ID: row.ID, Name: row.Name, Err: err})
				continue
			}
			moving = append(moving, row.ID)
			changes = append(changes, domain.StatusChange{RoomID: row.ID, From: row.Status, To: to})
		}
		if len(moving) == 0 {
			return nil
		}
		result.Done = append(result.Done, moving...)
		if err := r.rooms.SetStatus(ctx, landlordID, moving, to); err != nil {
			return err
		}
		return r.observer.StatusChanged(ctx, changes)
	})
	if err != nil {
		return BulkResult{}, err
	}
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}

// Reorder applies a drag and drop: ids are the rooms that moved, in their new relative order.
func (r *Rooms) Reorder(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return r.tx.WithinTx(ctx, func(ctx context.Context) error {
		current, err := r.rooms.Order(ctx, landlordID)
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
		return r.rooms.SetOrder(ctx, landlordID, moves)
	})
}
