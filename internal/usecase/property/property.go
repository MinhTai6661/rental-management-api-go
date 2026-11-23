// Package property holds the use cases of a landlord's buildings, rooms, charges and pictures. It owns the whole
// inheritance of a room from its building, so a building edit and the rooms it reaches change in one transaction.
// It declares the ports it needs (ports.go) and imports no framework, driver, storage or image code.
package property

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil ports get their no-op defaults.
type Deps struct {
	Tx        TxManager
	Buildings BuildingRepository
	Rooms     RoomRepository
	Charges   ChargeRepository
	Media     MediaRepository
	Files     FileStore
	Images    ImageProcessor
	Plans     PlanLimits
	Guards    RoomGuards
	Observer  RoomObserver
	Clock     Clock
	Log       *slog.Logger
}

// Options are the settings of the use cases.
type Options struct {
	// MaxMediaPerOwner is the number of pictures per room or building when the plan sets no limit.
	MaxMediaPerOwner int
	// MaxUploadBytes is the size limit of one picture upload (reported when an upload is too large).
	MaxUploadBytes int
	// MaxDimension is the longest side of a picture in pixels (reported when an upload is too large).
	MaxDimension int
}

// core is what the use case groups share: the ports, the settings and the helpers every group needs.
type core struct {
	tx        TxManager
	buildings BuildingRepository
	rooms     RoomRepository
	charges   ChargeRepository
	media     MediaRepository
	files     FileStore
	images    ImageProcessor
	plans     PlanLimits
	guards    RoomGuards
	observer  RoomObserver
	clock     Clock
	log       *slog.Logger
	opts      Options
}

func newCore(d Deps, o Options) *core {
	c := &core{
		tx: d.Tx, buildings: d.Buildings, rooms: d.Rooms, charges: d.Charges, media: d.Media, files: d.Files,
		images: d.Images, plans: d.Plans, guards: d.Guards, observer: d.Observer, clock: d.Clock, log: d.Log, opts: o,
	}
	if c.plans == nil {
		c.plans = UnlimitedPlans{}
	}
	if c.guards == nil {
		c.guards = NoGuards{}
	}
	if c.observer == nil {
		c.observer = NoObserver{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	return c
}

// notFound turns a repository's "no such row" into the domain's not found.
func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// deleteFiles removes stored files after the transaction that dropped their rows committed. A failure is logged:
// the rows are gone, so a leftover file is only wasted space.
func (c *core) deleteFiles(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	// The request may be over already (the client left); the cleanup must still happen.
	if err := c.files.Delete(context.WithoutCancel(ctx), keys...); err != nil {
		c.log.Warn("delete stored files", "error", err, "count", len(keys))
	}
}

// contentChanged tells the observer which rooms changed in a way a listing shows (each room once).
func (c *core) contentChanged(ctx context.Context, ids []uuid.UUID) error {
	ids = distinct(ids)
	if len(ids) == 0 {
		return nil
	}
	return c.observer.ContentChanged(ctx, ids)
}

// distinct drops repeated ids, keeping the first occurrence.
func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// checkWard refuses a ward that does not exist (no code is fine).
func (c *core) checkWard(ctx context.Context, code *int32) error {
	if code == nil {
		return nil
	}
	ok, err := c.buildings.WardExists(ctx, *code)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrWardNotFound
	}
	return nil
}

// checkBuildingLimit refuses a new building past the plan's limit (the caller holds the landlord lock).
func (c *core) checkBuildingLimit(ctx context.Context, landlordID uuid.UUID) error {
	limit, err := c.plans.MaxBuildings(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	count, err := c.buildings.CountLive(ctx, landlordID)
	if err != nil {
		return err
	}
	if domain.LimitReached(limit, count, 1) {
		return &domain.BuildingLimitError{Limit: *limit}
	}
	return nil
}

// checkRoomLimit refuses adding n rooms past the plan's limit (the caller holds the landlord lock).
func (c *core) checkRoomLimit(ctx context.Context, landlordID uuid.UUID, adding int) error {
	limit, err := c.plans.MaxRooms(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	count, err := c.rooms.CountLive(ctx, landlordID)
	if err != nil {
		return err
	}
	if domain.LimitReached(limit, count, adding) {
		return &domain.RoomLimitError{Limit: *limit}
	}
	return nil
}

// BulkFailure is an item a bulk operation refused; Err is the domain error that says why.
type BulkFailure struct {
	ID   uuid.UUID
	Name string
	Err  error
}

// BulkResult tells which items a bulk operation did and which it refused.
type BulkResult struct {
	Done   []uuid.UUID
	Failed []BulkFailure
}
