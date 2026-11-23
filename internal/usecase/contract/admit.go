package contract

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// admission says who admits a contract, what the audit trail records about it and what else must hold before it is
// stored.
type admission struct {
	actorID uuid.UUID
	// action and meta are the audit event of the new contract.
	action string
	meta   map[string]any
	// before, when set, runs after the common checks and before the contract is stored (the registration limit of a
	// tenant's own request is one: an invitation does not count against it).
	before func(ctx context.Context) error
}

// admit stores a contract a tenant holds on a room the caller has locked, under the rules every such contract
// follows, and returns it with its id. It does not tell anybody. The rules:
//   - nobody rents their own room;
//   - a running contract gives way to nothing but a blank one, and stays within the landlord's plan limit; the other
//     waiting registrations of the room are rejected and the room becomes rented;
//   - a waiting one is the tenant's only open contract on the room;
//   - the dates respect the room's minimum rental period.
//
// Any refusal leaves the caller's transaction to roll back. A unique violation (two requests racing) is mapped by
// admitError.
func (c *core) admit(ctx context.Context, room domain.RoomFacts, k domain.Contract, a admission) (domain.Contract, error) {
	if k.TenantID != nil && *k.TenantID == room.LandlordID {
		return domain.Contract{}, domain.ErrOwnRoom
	}
	switch k.Status {
	case domain.StatusActive:
		if err := c.assertRoomFree(ctx, room.ID); err != nil {
			return domain.Contract{}, err
		}
		if err := c.checkContractLimit(ctx, room.LandlordID); err != nil {
			return domain.Contract{}, err
		}
	case domain.StatusPending:
		open, err := c.contracts.RegistrationOpen(ctx, room.ID, *k.TenantID)
		if err != nil {
			return domain.Contract{}, err
		}
		if open {
			return domain.Contract{}, domain.ErrAlreadyRegistered
		}
	default:
		return domain.Contract{}, domain.ErrInvalidTransition
	}
	if err := domain.CheckMinimumPeriod(k.StartsOn, k.EndsOn, room.MinPeriod, room.PeriodUnit); err != nil {
		return domain.Contract{}, err
	}
	if a.before != nil {
		if err := a.before(ctx); err != nil {
			return domain.Contract{}, err
		}
	}
	order, err := c.contracts.TopOrder(ctx, room.LandlordID)
	if err != nil {
		return domain.Contract{}, err
	}
	now := c.clock.Now()
	k.RoomID, k.LandlordID, k.ConfirmedAt, k.DisplayOrder = room.ID, room.LandlordID, &now, order
	if k.ID, err = c.contracts.Insert(ctx, k); err != nil {
		return domain.Contract{}, err
	}
	if k.Status == domain.StatusActive {
		// Somebody got the room: the other waiting registrations can no longer be accepted.
		if _, err := c.contracts.RejectOtherPending(ctx, room.ID, k.ID); err != nil {
			return domain.Contract{}, err
		}
		if err := c.syncRoom(ctx, room); err != nil {
			return domain.Contract{}, err
		}
	}
	return k, c.record(ctx, a.action, a.actorID, k, a.meta)
}

// admitError maps the unique violation of two requests racing for the same room (or the same tenant and room) to the
// domain's answer; any other error passes through.
func admitError(err error, status domain.Status) error {
	if !shared.IsUnique(err) {
		return err
	}
	if status == domain.StatusActive {
		return domain.ErrRoomOccupied
	}
	return domain.ErrAlreadyRegistered
}
