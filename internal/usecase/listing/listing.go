// Package listing holds the use cases of listings: a landlord writes a post that offers one of their rooms and sends it
// to the admins, an admin approves, refuses or takes it down, and renters (and guests) search the rooms that are
// vacant and have an approved listing. It declares the ports it needs (ports.go) and imports no framework, driver or
// other context: the plan limit and priority, notifications and audit are ports too, and the property context tells
// it about rooms through the composition root.
package listing

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx       TxManager
	Listings ListingRepository
	Rooms    RoomReader
	Offers   OfferRepository
	Seed     SeedRooms
	Plans    PlanLimits
	Priority SearchPriority
	Notifier Notifier
	Audit    AuditRecorder
	Clock    Clock
	Log      *slog.Logger
}

// core is what the use case groups share: the ports and the helpers every group needs.
type core struct {
	tx       TxManager
	listings ListingRepository
	rooms    RoomReader
	offers   OfferRepository
	seed     SeedRooms
	plans    PlanLimits
	priority SearchPriority
	notifier Notifier
	audit    AuditRecorder
	clock    Clock
	log      *slog.Logger
}

func newCore(d Deps) *core {
	c := &core{
		tx: d.Tx, listings: d.Listings, rooms: d.Rooms, offers: d.Offers, seed: d.Seed, plans: d.Plans,
		priority: d.Priority, notifier: d.Notifier, audit: d.Audit, clock: d.Clock, log: d.Log,
	}
	if c.plans == nil {
		c.plans = UnlimitedPlans{}
	}
	if c.priority == nil {
		c.priority = NeutralPriority{}
	}
	if c.notifier == nil {
		c.notifier = NoNotifier{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
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

// detail is the listing as the scope sees it; domain.ErrNotFound when it is not in it.
func (c *core) detail(ctx context.Context, landlordID *uuid.UUID, id uuid.UUID) (domain.Detail, error) {
	d, err := c.listings.Get(ctx, landlordID, id)
	return d, notFound(err)
}

// withExtras adds the costs and pictures of the room to a listing's detail.
func (c *core) withExtras(ctx context.Context, d domain.Detail) (domain.Detail, error) {
	var err error
	d.Charges, d.Pictures, err = c.rooms.Extras(ctx, d.RoomID)
	return d, err
}

// record writes an audit event for the listing, in the transaction of ctx.
func (c *core) record(ctx context.Context, action string, actor uuid.UUID, l domain.Listing, meta map[string]any) error {
	if meta == nil {
		meta = map[string]any{}
	}
	meta["listing_id"] = l.ID.String()
	return c.audit.Record(ctx, AuditEvent{Action: action, ActorID: actor, ListingID: l.ID, RoomID: l.RoomID, OwnerID: l.LandlordID, Meta: meta})
}

// tell sends a notification after the change is stored: a failure is logged and never undoes the change.
func (c *core) tell(ctx context.Context, n Notification) {
	if err := c.notifier.Notify(ctx, n); err != nil {
		c.log.Warn("notify about a listing", "error", err, "kind", n.Kind)
	}
}

// tellAdmins tells the admins a listing waits for review.
func (c *core) tellAdmins(ctx context.Context, d domain.Detail) {
	c.tell(ctx, Notification{
		ToAdmins: true, Kind: NotifySubmitted, URL: "/admin/listings/" + d.ID.String(),
		Params: map[string]string{"landlord": d.Landlord.FullName, "title": d.Title, "room": d.Room.Name},
	})
}

// tellLandlord tells the landlord what the admins decided about their listing.
func (c *core) tellLandlord(ctx context.Context, kind string, d domain.Detail) {
	reason := ""
	if d.ReviewNote != nil {
		reason = *d.ReviewNote
	}
	c.tell(ctx, Notification{
		UserID: d.LandlordID, Kind: kind, URL: "/my-listings", Audience: AudienceLandlord,
		Params: map[string]string{"title": d.Title, "room": d.Room.Name, "reason": reason},
	})
}
