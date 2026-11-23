package property

import (
	"context"
	"slices"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Update replaces the fields of a room.
func (r *Rooms) Update(ctx context.Context, landlordID, id uuid.UUID, in RoomInput) (domain.RoomDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return domain.RoomDetail{}, err
	}
	var staleFiles []string
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		cur, err := r.rooms.ForUpdate(ctx, landlordID, id)
		if err != nil {
			return notFound(err)
		}
		var building *domain.BuildingDefaults
		if in.BuildingID != nil {
			d, err := r.loadBuilding(ctx, landlordID, *in.BuildingID)
			if err != nil {
				return err
			}
			building = &d
		}
		fields := in.fields(charges)
		state := cur.State()
		groups := domain.ResolveInheritance(in.Inherit, domain.CarriedGroups(fields, &state), building != nil)
		values, violations := domain.EffectiveValues(building, groups, fields)
		if len(violations) > 0 {
			return domain.Invalid(violations...)
		}
		if err := r.checkWard(ctx, values.WardCode); err != nil {
			return err
		}
		status := statusOrDefault(in.Status, cur.Status)
		if status != cur.Status {
			blockers, err := r.guards.Blockers(ctx, []uuid.UUID{id})
			if err != nil {
				return err
			}
			if err := domain.CheckStatusChange(status, blockers[id]); err != nil {
				return err
			}
		}
		if err := r.checkRoomName(ctx, landlordID, in.BuildingID, in.Name, &id); err != nil {
			return err
		}
		next := domain.RoomData{BuildingID: in.BuildingID, Name: in.Name, RoomValues: values, Status: status, Inherits: groups}
		if err := r.rooms.Update(ctx, landlordID, id, next); err != nil {
			return err
		}
		buildingMoved := !domain.EqualPtr(cur.BuildingID, in.BuildingID)
		changed := cur.ContentDiffers(next)

		follows := slices.Contains(groups, domain.GroupCharges)
		chargesChanged, err := r.updateRoomCharges(ctx, id, cur, follows, buildingMoved, in, charges)
		if err != nil {
			return err
		}
		mediaChanged, files, err := r.updateRoomMedia(ctx, id, cur, slices.Contains(groups, domain.GroupMedia), buildingMoved)
		if err != nil {
			return err
		}
		staleFiles = files

		if status != cur.Status {
			if err := r.observer.StatusChanged(ctx, []domain.StatusChange{{RoomID: id, From: cur.Status, To: status}}); err != nil {
				return err
			}
		}
		if changed || chargesChanged || mediaChanged {
			return r.contentChanged(ctx, []uuid.UUID{id})
		}
		return nil
	})
	if err != nil {
		return domain.RoomDetail{}, roomNameTaken(err)
	}
	r.deleteFiles(ctx, staleFiles)
	return r.Get(ctx, landlordID, id)
}

// updateRoomCharges brings the charges of an updated room in line with its new inheritance: a room that follows
// its building takes the building's, one that stopped following keeps the copies as its own, and a list in the
// request replaces the own charges. It reports whether the charges a listing shows changed.
func (r *Rooms) updateRoomCharges(ctx context.Context, id uuid.UUID, cur domain.Room, follows, buildingMoved bool,
	in RoomInput, charges []domain.ChargeInput) (bool, error) {
	wasFollowing := cur.Follows(domain.GroupCharges)
	if follows {
		if err := r.follow(ctx, []uuid.UUID{id}, *in.BuildingID); err != nil {
			return false, err
		}
		return !wasFollowing || buildingMoved, nil
	}
	changed := false
	if wasFollowing {
		if err := r.charges.Detach(ctx, []uuid.UUID{id}); err != nil {
			return false, err
		}
		changed = true
	}
	if in.Charges != nil {
		replaced, err := r.syncRoomCharges(ctx, id, charges)
		if err != nil {
			return false, err
		}
		changed = changed || replaced
	}
	return changed, nil
}

// updateRoomMedia keeps the cover of an updated room in line with whether it follows its building's pictures: a
// room that starts following drops its own pictures (their files are returned, to delete after commit) and shows the
// building's cover; one that stops following shows its own cover (none when it has no pictures). It reports whether
// the pictures shown changed.
func (r *Rooms) updateRoomMedia(ctx context.Context, id uuid.UUID, cur domain.Room, follows, buildingMoved bool) (bool, []string, error) {
	ids := []uuid.UUID{id}
	wasFollowing := cur.Follows(domain.GroupMedia)
	switch {
	case follows && !wasFollowing:
		files, err := r.media.DeleteOfRooms(ctx, ids)
		if err != nil {
			return false, nil, err
		}
		return true, files, r.media.CoverRoomsFromBuilding(ctx, ids)
	case follows && buildingMoved:
		return true, nil, r.media.CoverRoomsFromBuilding(ctx, ids)
	case !follows && wasFollowing:
		_, err := r.refreshCover(ctx, domain.RoomMedia(id))
		return true, nil, err
	}
	return false, nil, nil
}
