package listing

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// The interfaces below are what the listing use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.
// Repository lookups return shared.ErrNotFound when there is no such row; writes refused by a constraint come back
// as *shared.ConstraintError.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it, and so do the
// other ports implemented over the same database.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ListingRepository stores listings. The lookups that take a landlord id are scoped to that landlord (nil: every
// landlord's, for the admins).
type ListingRepository interface {
	// Search returns a page of listings with their rooms and the people they name, and how many match in all.
	Search(ctx context.Context, q domain.Query) ([]domain.Detail, int, error)
	// Counts counts the listings per status.
	Counts(ctx context.Context, landlordID *uuid.UUID, excludeDrafts bool) (domain.StatusCounts, error)
	// Get returns one listing as a list shows it (shared.ErrNotFound when it is not in the landlord's scope).
	Get(ctx context.Context, landlordID *uuid.UUID, id uuid.UUID) (domain.Detail, error)
	// ByIDs returns the listings among ids (fewer when some do not exist).
	ByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.Listing, error)
	// ForUpdate locks a listing until the transaction ends. Lock its room first, to keep one lock order.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Listing, error)
	Insert(ctx context.Context, l domain.Listing) (uuid.UUID, error)
	// Save writes the fields of a listing that can change.
	Save(ctx context.Context, l domain.Listing) error
	Delete(ctx context.Context, id uuid.UUID) error
	// OpenOfRoom says whether the room has a waiting or live listing other than except.
	OpenOfRoom(ctx context.Context, roomID uuid.UUID, except *uuid.UUID) (bool, error)
	// OpenRooms returns the rooms among roomIDs that have a waiting or live listing.
	OpenRooms(ctx context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error)
	// LockOpenOfRooms locks and returns the waiting and live listings of the rooms.
	LockOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]domain.Listing, error)
	// CountOpen counts the landlord's waiting and live listings.
	CountOpen(ctx context.Context, landlordID uuid.UUID) (int, error)
	// LockLandlord serialises the transactions that count a landlord's open listings against a plan limit.
	LockLandlord(ctx context.Context, landlordID uuid.UUID) error
}

// RoomReader tells the facts about rooms that listings need (a read-only view of what the property context owns).
type RoomReader interface {
	// Lock returns the room, a deleted one included (Live tells), and locks it until the transaction ends
	// (shared.ErrNotFound when there is no such room).
	Lock(ctx context.Context, id uuid.UUID) (domain.RoomFacts, error)
	// Extras returns the costs and the pictures of a room (its building's pictures while it follows them).
	Extras(ctx context.Context, roomID uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error)
}

// OfferRepository is the read-only search of find rooms: the rooms that are vacant and have an approved listing.
type OfferRepository interface {
	// Search returns a page of offered rooms and how many match in all.
	Search(ctx context.Context, q domain.OfferQuery) ([]domain.Offer, int, error)
	// Landlords returns the landlords with at least one room matching the filters of q (paging and order ignored).
	Landlords(ctx context.Context, q domain.OfferQuery) ([]uuid.UUID, error)
	// Get returns an offered room (shared.ErrNotFound when it is not offered).
	Get(ctx context.Context, id uuid.UUID) (domain.Offer, error)
	// Landlord returns an active landlord as a guest may see them (shared.ErrNotFound for anybody else).
	Landlord(ctx context.Context, id uuid.UUID) (domain.LandlordCard, error)
}

// SeedRooms lists the available rooms of a landlord that have no listing yet, in display order.
type SeedRooms interface {
	WithoutListing(ctx context.Context, landlordID uuid.UUID) ([]domain.SeedRoom, error)
}

// Ports to what other contexts own. Each has a default that changes nothing, so the context works on its own and
// the composition root (cmd/rental-management) wires the real one without this package knowing it.

// PlanLimits says how many open listings a landlord's plan allows; nil means unlimited. No plan sets this yet.
type PlanLimits interface {
	MaxActiveListings(ctx context.Context, landlordID uuid.UUID) (*int, error)
}

// UnlimitedPlans is the default: no plan limits anything.
type UnlimitedPlans struct{}

// MaxActiveListings is unlimited.
func (UnlimitedPlans) MaxActiveListings(context.Context, uuid.UUID) (*int, error) { return nil, nil }

// SearchPriority tells how high the plan of each landlord ranks their rooms in find rooms (greater first). A
// landlord left out of the answer counts as 0.
type SearchPriority interface {
	SearchPriorities(ctx context.Context, landlordIDs []uuid.UUID) (map[uuid.UUID]int, error)
}

// NeutralPriority is the default: every landlord ranks the same, so the search is newest first.
type NeutralPriority struct{}

// SearchPriorities ranks nobody.
func (NeutralPriority) SearchPriorities(context.Context, []uuid.UUID) (map[uuid.UUID]int, error) {
	return nil, nil
}

// Notification is a message about a listing: for one user, or for every admin.
type Notification struct {
	// UserID is the recipient; uuid.Nil when ToAdmins.
	UserID uuid.UUID
	// ToAdmins: every admin (not the super admins) gets it.
	ToAdmins bool
	// Kind is one of the Notify* constants; the web translates it with Params.
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Notifier tells users about something that happened.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoNotifier is the default: nobody is told.
type NoNotifier struct{}

// Notify does nothing.
func (NoNotifier) Notify(context.Context, Notification) error { return nil }

// AuditEvent is something worth keeping in the audit trail.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action string
	// ActorID is who did it (uuid.Nil for the system).
	ActorID   uuid.UUID
	ListingID uuid.UUID
	RoomID    uuid.UUID
	// OwnerID is the landlord whose data it is.
	OwnerID uuid.UUID
	Meta    map[string]any
}

// AuditRecorder writes the audit trail; it runs in the transaction of ctx.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
