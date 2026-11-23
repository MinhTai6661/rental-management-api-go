package property

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ports to what later phases own. Each has a default that changes nothing, so the module works on its own and a
// later module is wired in internal/app without touching this one.

// PlanLimits says how much a landlord's plan allows; nil means unlimited. The plan module implements it.
type PlanLimits interface {
	MaxBuildings(ctx context.Context, landlordID uuid.UUID) (*int, error)
	MaxRooms(ctx context.Context, landlordID uuid.UUID) (*int, error)
	// MaxMediaPerOwner is the number of pictures one room or building may have.
	MaxMediaPerOwner(ctx context.Context, landlordID uuid.UUID) (*int, error)
}

// UnlimitedPlans is the default: no plan limits anything.
type UnlimitedPlans struct{}

func (UnlimitedPlans) MaxBuildings(context.Context, uuid.UUID) (*int, error)     { return nil, nil }
func (UnlimitedPlans) MaxRooms(context.Context, uuid.UUID) (*int, error)         { return nil, nil }
func (UnlimitedPlans) MaxMediaPerOwner(context.Context, uuid.UUID) (*int, error) { return nil, nil }

// RoomGuards tells what stops rooms from being changed or deleted (a running contract, an open listing). It
// receives the open transaction so the answer is read in the same snapshot as the change.
type RoomGuards interface {
	Blockers(ctx context.Context, tx pgx.Tx, roomIDs []uuid.UUID) (map[uuid.UUID]Blocker, error)
}

// NoGuards is the default: nothing blocks a room.
type NoGuards struct{}

func (NoGuards) Blockers(context.Context, pgx.Tx, []uuid.UUID) (map[uuid.UUID]Blocker, error) {
	return nil, nil
}

// StatusChange is a room that moved from one status to another; From is "" for a new room.
type StatusChange struct {
	RoomID uuid.UUID
	From   Status
	To     Status
}

// RoomObserver is told about changes of rooms, in the transaction that makes them, so a later module can react
// atomically (open or close a blank contract, send a listing back to draft).
type RoomObserver interface {
	StatusChanged(ctx context.Context, tx pgx.Tx, changes []StatusChange) error
	ContentChanged(ctx context.Context, tx pgx.Tx, roomIDs []uuid.UUID) error
}

// NoObserver is the default: nobody listens.
type NoObserver struct{}

func (NoObserver) StatusChanged(context.Context, pgx.Tx, []StatusChange) error { return nil }
func (NoObserver) ContentChanged(context.Context, pgx.Tx, []uuid.UUID) error   { return nil }
