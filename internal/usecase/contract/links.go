package contract

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// RoomLinks is what the property context needs to know and ask of contracts when a room changes. The composition
// root adapts it to the property ports, so the two contexts never import each other.
type RoomLinks struct{ *core }

// NewRoomLinks builds the use cases the room bridges call.
func NewRoomLinks(d Deps, o Options) *RoomLinks { return &RoomLinks{newCore(d, o)} }

// Rented opens the blank contract of a room that was marked rented while no contract runs on it: nobody with an
// account is behind it, so the room can be billed like any other. Nothing happens when the room is not rented, was
// deleted, or already has a running contract.
func (l *RoomLinks) Rented(ctx context.Context, roomID uuid.UUID) error {
	room, err := l.rooms.Lock(ctx, roomID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil // no such room: nothing to open
		}
		return err
	}
	if !room.Live || room.Status != domain.RoomRented {
		return nil
	}
	occ, err := l.contracts.Occupancy(ctx, roomID)
	if err != nil || occ.Running > 0 {
		return err
	}
	order, err := l.contracts.TopOrder(ctx, room.LandlordID)
	if err != nil {
		return err
	}
	now := l.clock.Now()
	c := domain.Contract{
		RoomID: room.ID, LandlordID: room.LandlordID, Status: domain.StatusActive, PriceVND: room.PriceVND,
		OccupantCount: 1, StartsOn: l.today(), ConfirmedAt: &now, DisplayOrder: order,
	}
	id, err := l.contracts.Insert(ctx, c)
	if err != nil {
		return err
	}
	c.ID = id
	return l.record(ctx, AuditCreated, uuid.Nil, c, map[string]any{"status": string(domain.StatusActive), "price_vnd": c.PriceVND, "unassigned": true})
}

// Released ends the blank contracts of rooms that stopped being rented or were deleted.
func (l *RoomLinks) Released(ctx context.Context, roomIDs []uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	return l.closeBlank(ctx, roomIDs, "room_released")
}

// Deleted is what happens to the contracts of rooms that were deleted: their blank contracts end, and the offers still
// waiting are withdrawn (nobody can accept an offer of a room that is gone). It runs in the transaction of the deletion,
// which holds the rooms' locks, so the order of locks is the room's, then the invitation's.
func (l *RoomLinks) Deleted(ctx context.Context, roomIDs []uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	if err := l.closeBlank(ctx, roomIDs, "room_released"); err != nil {
		return err
	}
	revoked, err := l.invitations.RevokeOpenOfRooms(ctx, roomIDs, l.clock.Now())
	if err != nil {
		return err
	}
	for _, inv := range revoked {
		if err := l.recordInvitation(ctx, AuditInvitationRevoked, uuid.Nil, inv, map[string]any{"reason": "room_deleted"}); err != nil {
			return err
		}
	}
	return nil
}

// Occupied tells which of the rooms a tenant holds a running contract on: those cannot be deleted or taken out of
// the rented status. A blank contract does not lock a room.
func (l *RoomLinks) Occupied(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	ids, err := l.contracts.RunningTenantRooms(ctx, roomIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
