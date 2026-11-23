package contract

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// Update changes a contract of the landlord: its terms, and/or its status along the life cycle (pending to active or
// rejected, active to ended; a contract that ended or was rejected can be edited and reopened). The error says what
// refuses it: the room already has a running contract, the dates break the room's minimum rental period...
func (s *Contracts) Update(ctx context.Context, landlordID, id uuid.UUID, in UpdateInput) (domain.Detail, error) {
	before, err := s.applyUpdate(ctx, landlordID, id, in)
	if err != nil {
		return domain.Detail{}, err
	}
	d, err := s.detail(ctx, domain.Viewer{UserID: landlordID, AsLandlord: true}, id)
	if err != nil {
		return domain.Detail{}, err
	}
	s.afterUpdate(ctx, before, d)
	return d, nil
}

// applyUpdate stores the change with its audit record and returns the status the contract had before.
func (s *core) applyUpdate(ctx context.Context, landlordID, id uuid.UUID, in UpdateInput) (domain.Status, error) {
	var before domain.Status
	var target domain.Status
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.contracts.Get(ctx, domain.Viewer{UserID: landlordID, AsLandlord: true}, id)
		if err != nil {
			return notFound(err)
		}
		room, err := s.lockRoom(ctx, d.RoomID)
		if err != nil {
			return err
		}
		c, err := s.contracts.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		from := c.Status
		if in.requireFrom != "" && from != in.requireFrom {
			return domain.ErrInvalidTransition
		}
		before, target = from, from
		if in.Status != nil {
			target = *in.Status
		}
		if target != from && !domain.CanMove(from, target) {
			return domain.ErrInvalidTransition
		}
		if err := applyFields(&c, in); err != nil {
			return err
		}
		activating := target == domain.StatusActive && from != domain.StatusActive
		if activating {
			if err := s.assertRoomFree(ctx, c.RoomID); err != nil {
				return err
			}
			if err := s.checkContractLimit(ctx, room.LandlordID); err != nil {
				return err
			}
		}
		// The dates are checked again only when they change, and not when the contract is being ended or rejected.
		datesChanged := in.StartsOn != nil || in.EndsOn.Set
		if datesChanged {
			if err := domain.CheckDates(c.StartsOn, c.EndsOn); err != nil {
				return err
			}
			if target != domain.StatusEnded && target != domain.StatusRejected {
				if err := domain.CheckMinimumPeriod(c.StartsOn, c.EndsOn, room.MinPeriod, room.PeriodUnit); err != nil {
					return err
				}
			}
		}
		if target != from {
			c.MoveTo(target, s.today(), in.EndsOn.Set && in.EndsOn.Value != nil)
		}
		if err := s.contracts.Save(ctx, c); err != nil {
			return err
		}
		if activating {
			// Somebody got the room: the other waiting registrations can no longer be accepted.
			if _, err := s.contracts.RejectOtherPending(ctx, c.RoomID, c.ID); err != nil {
				return err
			}
		}
		if err := s.syncRoom(ctx, room); err != nil {
			return err
		}
		return s.record(ctx, AuditUpdated, landlordID, c, map[string]any{"from": string(from), "to": string(target)})
	})
	if err != nil {
		if shared.IsUnique(err) {
			if target == domain.StatusActive {
				return "", domain.ErrRoomOccupied
			}
			return "", domain.ErrAlreadyRegistered
		}
		return "", err
	}
	return before, nil
}

// applyFields copies what the update carries into the contract. The tenant's name and phone are only for a contract
// nobody holds.
func applyFields(c *domain.Contract, in UpdateInput) error {
	if in.PriceVND != nil {
		c.PriceVND = *in.PriceVND
	}
	if in.DepositVND.Set {
		c.DepositVND = in.DepositVND.Value
	}
	if in.OccupantCount != nil {
		if *in.OccupantCount < 1 || *in.OccupantCount > domain.MaxOccupants {
			return domain.ErrOccupantsInvalid
		}
		c.OccupantCount = *in.OccupantCount
	}
	if in.StartsOn != nil {
		c.StartsOn = *in.StartsOn
	}
	if in.EndsOn.Set {
		c.EndsOn = in.EndsOn.Value
	}
	if in.Notes.Set {
		c.Notes = in.Notes.Value
	}
	if c.Unassigned() {
		if in.TenantName.Set {
			c.TenantName = in.TenantName.Value
		}
		if in.TenantPhone.Set {
			c.TenantPhone = in.TenantPhone.Value
		}
	}
	return nil
}

// assertRoomFree makes room for a contract that is about to run: a contract nobody holds gives way to one somebody
// does, then no other contract may run on the room.
func (c *core) assertRoomFree(ctx context.Context, roomID uuid.UUID) error {
	if err := c.closeBlank(ctx, []uuid.UUID{roomID}, "displaced"); err != nil {
		return err
	}
	occ, err := c.contracts.Occupancy(ctx, roomID)
	if err != nil {
		return err
	}
	if occ.Running > 0 {
		return domain.ErrRoomOccupied
	}
	return nil
}

// afterUpdate tells the people an update concerns, once it is stored.
func (c *core) afterUpdate(ctx context.Context, before domain.Status, d domain.Detail) {
	switch {
	case before == domain.StatusActive && d.Status == domain.StatusEnded:
		// The landlord ended a running contract on their own: both sides are told.
		c.notifyEnded(ctx, d, nil)
	case before == domain.StatusPending && d.Status == domain.StatusActive:
		c.notify(ctx, d, NotifyRegistrationApproved, false, nil)
	case before == domain.StatusPending && d.Status == domain.StatusRejected:
		c.notify(ctx, d, NotifyRegistrationRejected, false, nil)
	}
}

// BulkAnswer approves or rejects several waiting registrations. Each contract goes through its own rules and its
// own transaction; the ones that cannot are reported as failed while the others still go through. An id that is not
// a contract of the landlord is domain.ErrItemsNotFound and nothing is done.
func (s *Contracts) BulkAnswer(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, answer Answer) (BulkResult, error) {
	viewer := domain.Viewer{UserID: landlordID, AsLandlord: true}
	rows := make([]domain.Detail, len(ids))
	for i, id := range ids {
		d, err := s.contracts.Get(ctx, viewer, id)
		if err != nil {
			if errorsIsNotFound(err) {
				return BulkResult{}, domain.ErrItemsNotFound
			}
			return BulkResult{}, err
		}
		rows[i] = d
	}
	target := domain.StatusActive
	if answer == AnswerReject {
		target = domain.StatusRejected
	}
	result := BulkResult{Done: []uuid.UUID{}}
	for _, d := range rows {
		fail := func(err error) {
			result.Failed = append(result.Failed, BulkFailure{ID: d.ID, Name: d.Room.Name, Err: err})
		}
		if d.Status != domain.StatusPending {
			fail(domain.ErrInvalidTransition)
			continue
		}
		before, err := s.applyUpdate(ctx, landlordID, d.ID, UpdateInput{Status: &target, requireFrom: domain.StatusPending})
		if err != nil {
			if !domain.IsRefusal(err) {
				return BulkResult{}, err
			}
			fail(err)
			continue
		}
		updated, err := s.detail(ctx, viewer, d.ID)
		if err != nil {
			return BulkResult{}, err
		}
		s.afterUpdate(ctx, before, updated)
		result.Done = append(result.Done, d.ID)
	}
	return result, nil
}
