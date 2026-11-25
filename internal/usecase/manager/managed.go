package manager

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/manager"
	"rental-management/internal/domain/shared"
)

// Managed is the manager's side: the rooms handed to them (read only: a manager never gives a room up) and what they
// were given on each.
type Managed struct{ *core }

// NewManaged builds the manager's use cases.
func NewManaged(d Deps, o Options) *Managed { return &Managed{newCore(d, o)} }

// Rooms returns a page of the rooms handed to the manager, newest first; a nil landlordID is every landlord.
func (s *Managed) Rooms(ctx context.Context, managerID uuid.UUID, landlordID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error) {
	return s.Assignments.ManagedList(ctx, managerID, landlordID, limit, offset)
}

// Groups returns a page of the landlords that handed rooms to the manager, with how many rooms each.
func (s *Managed) Groups(ctx context.Context, managerID uuid.UUID, limit, offset int) ([]ManagedGroup, int, error) {
	return s.Assignments.ManagedGroups(ctx, managerID, limit, offset)
}

// Access answers what an account was given on a room: the lookup the other contexts use (through the composition
// root) to decide what a manager may do there. It reads only; the caller has decided that the account acts as a
// manager, which is what makes the answer count (an account in tenant mode has no rights from a hand-over).
type Access struct{ *core }

// NewAccess builds the lookup.
func NewAccess(d Deps, o Options) *Access { return &Access{newCore(d, o)} }

// Of returns the manager's active hand-over of the room; nil when there is none.
func (s *Access) Of(ctx context.Context, managerID, roomID uuid.UUID) (*domain.Assignment, error) {
	a, err := s.Assignments.Of(ctx, managerID, roomID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil //nolint:nilnil // "no hand-over" is a normal answer
		}
		return nil, err
	}
	return &a, nil
}

// RoomIDs lists the rooms the manager has an active hand-over of.
func (s *Access) RoomIDs(ctx context.Context, managerID uuid.UUID) ([]uuid.UUID, error) {
	list, err := s.Assignments.ActiveOf(ctx, managerID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, len(list))
	for i, a := range list {
		out[i] = a.RoomID
	}
	return out, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound) || errors.Is(err, shared.ErrNotFound)
}
