package listing

import (
	"context"

	"github.com/google/uuid"
)

// RoomLinks is what the property context needs from listings, reached through the composition root: which rooms are
// held by a waiting or live listing (they cannot be deleted) and the reaction to a room whose content changed.
type RoomLinks struct{ *core }

// NewRoomLinks builds the room links.
func NewRoomLinks(d Deps) *RoomLinks { return &RoomLinks{newCore(d)} }

// Open returns the rooms among roomIDs that have a waiting or live listing. It reads in the transaction of ctx.
func (s *RoomLinks) Open(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(roomIDs) == 0 {
		return nil, nil
	}
	open, err := s.listings.OpenRooms(ctx, roomIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(open))
	for _, id := range open {
		out[id] = true
	}
	return out, nil
}

// ReturnToDraft sends the waiting or live listings of the rooms back to drafts: what the admins saw (or approved)
// is not what renters would now see, so the listing leaves find rooms and the landlord sends it again when ready.
// Nobody is told (the admins are not bothered by a draft). It runs in the transaction of the room change.
func (s *RoomLinks) ReturnToDraft(ctx context.Context, roomIDs []uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	open, err := s.listings.LockOpenOfRooms(ctx, roomIDs)
	if err != nil {
		return err
	}
	for _, l := range open {
		from := l.Status
		if err := l.ReturnToDraft(); err != nil {
			return err
		}
		if err := s.listings.Save(ctx, l); err != nil {
			return err
		}
		if err := s.record(ctx, AuditUpdated, uuid.Nil, l, map[string]any{
			"back_to_draft": true, "from": string(from), "because": "room_changed",
		}); err != nil {
			return err
		}
	}
	return nil
}
