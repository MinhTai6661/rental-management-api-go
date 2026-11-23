package contract

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// Register is a tenant asking to rent a room: the contract starts pending and the landlord sees it at once (there is
// no confirmation by email). Spam is held back by one open registration per tenant and room, the plan's cap on
// waiting registrations and the rate limit of the route.
func (s *Contracts) Register(ctx context.Context, tenantID uuid.UUID, in RegisterInput) (domain.Detail, error) {
	if in.OccupantCount == 0 {
		in.OccupantCount = 1
	}
	if in.OccupantCount < 1 || in.OccupantCount > domain.MaxOccupants {
		return domain.Detail{}, domain.ErrOccupantsInvalid
	}
	if in.StartsOn.Before(s.today()) {
		return domain.Detail{}, domain.ErrStartInPast
	}
	if err := domain.CheckDates(in.StartsOn, in.EndsOn); err != nil {
		return domain.Detail{}, err
	}
	var id uuid.UUID
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		room, err := s.rooms.Lock(ctx, in.RoomID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return domain.ErrRoomNotFound
			}
			return err
		}
		if !room.Live {
			return domain.ErrRoomNotFound
		}
		if room.LandlordID == tenantID {
			return domain.ErrOwnRoom
		}
		occ, err := s.contracts.Occupancy(ctx, room.ID)
		if err != nil {
			return err
		}
		if !domain.RoomVacant(room, occ) {
			return domain.ErrRoomNotVacant
		}
		// A room is on offer once an admin approved its listing; nobody registers for one that is not.
		if !room.Listed {
			return domain.ErrRoomNotListed
		}
		k := domain.Contract{
			TenantID: &tenantID, Status: domain.StatusPending, PriceVND: room.PriceVND, OccupantCount: in.OccupantCount,
			StartsOn: in.StartsOn, EndsOn: in.EndsOn, Notes: in.Notes,
		}
		stored, err := s.admit(ctx, room, k, admission{
			actorID: tenantID, action: AuditRegistered,
			meta: map[string]any{"starts_on": in.StartsOn.Format(domain.DateLayout), "ends_on": dateMeta(in.EndsOn)},
			before: func(ctx context.Context) error {
				return s.checkRegistrationLimit(ctx, tenantID)
			},
		})
		id = stored.ID
		return err
	})
	if err != nil {
		return domain.Detail{}, admitError(err, domain.StatusPending)
	}
	d, err := s.detail(ctx, domain.Viewer{UserID: tenantID}, id)
	if err != nil {
		return domain.Detail{}, err
	}
	s.notify(ctx, d, NotifyRegistrationConfirmed, true, nil)
	return d, nil
}

// checkRegistrationLimit refuses a new registration past the tenant's plan limit.
func (c *core) checkRegistrationLimit(ctx context.Context, tenantID uuid.UUID) error {
	limit, err := c.plans.MaxPendingRegistrations(ctx, tenantID)
	if err != nil || limit == nil {
		return err
	}
	count, err := c.contracts.CountPending(ctx, tenantID)
	if err != nil {
		return err
	}
	if domain.LimitReached(limit, count) {
		return &domain.RegistrationLimitError{Limit: *limit}
	}
	return nil
}
