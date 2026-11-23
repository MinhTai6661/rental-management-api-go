// Package property is the buildings, rooms, charges and pictures of a landlord. It owns the whole inheritance of a
// room from its building, so a building edit and the rooms it reaches change in one transaction.
package property

import (
	"log/slog"
	"time"

	"rental-management/pkg/imaging"
	"rental-management/pkg/storage"
)

// Deps are the collaborators of the service. Nil ports get their no-op defaults.
type Deps struct {
	Store    *Store
	Storage  storage.Storage
	Images   *imaging.Processor
	Plans    PlanLimits
	Guards   RoomGuards
	Observer RoomObserver
	Log      *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Options are the settings of the service.
type Options struct {
	// MaxMediaPerOwner is the number of pictures per room or building when the plan sets no limit.
	MaxMediaPerOwner int
	// MaxUploadBytes is the size limit of one picture upload.
	MaxUploadBytes int
	// MaxDimension is the longest side of a picture in pixels (reported when an upload is too large).
	MaxDimension int
}

// Service is the use cases of buildings and rooms.
type Service struct {
	store    *Store
	storage  storage.Storage
	images   *imaging.Processor
	plans    PlanLimits
	guards   RoomGuards
	observer RoomObserver
	log      *slog.Logger
	now      func() time.Time
	opts     Options
}

// NewService builds the service.
func NewService(d Deps, o Options) *Service {
	s := &Service{
		store: d.Store, storage: d.Storage, images: d.Images, plans: d.Plans, guards: d.Guards, observer: d.Observer,
		log: d.Log, now: d.Now, opts: o,
	}
	if s.plans == nil {
		s.plans = UnlimitedPlans{}
	}
	if s.guards == nil {
		s.guards = NoGuards{}
	}
	if s.observer == nil {
		s.observer = NoObserver{}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}
