package main

import (
	"context"

	"github.com/google/uuid"

	mailadapter "rental-management/internal/adapter/mail"
	plandomain "rental-management/internal/domain/plan"
	propertydomain "rental-management/internal/domain/property"
	contractuc "rental-management/internal/usecase/contract"
	invitationuc "rental-management/internal/usecase/invitation"
	invoiceuc "rental-management/internal/usecase/invoice"
	listinguc "rental-management/internal/usecase/listing"
	planuc "rental-management/internal/usecase/plan"
	propertyuc "rental-management/internal/usecase/property"
)

// The bridges between the property, contract, listing, plan and invoice contexts. None imports another (the linter enforces it): this
// composition root adapts one's use cases to the other's ports.

// contractGuards tells property which rooms a contract locks: a room a tenant holds a running contract on cannot be
// deleted or leave the rented status (a blank contract does not lock it).
type contractGuards struct{ links *contractuc.RoomLinks }

// Blockers implements propertyuc.RoomGuards.
func (g contractGuards) Blockers(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]propertydomain.Blocker, error) {
	occupied, err := g.links.Occupied(ctx, roomIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]propertydomain.Blocker, len(occupied))
	for id := range occupied {
		out[id] = propertydomain.Blocker{ActiveContract: true}
	}
	return out, nil
}

// contractObserver tells contracts about rooms: a room marked rented gets a blank contract when nobody holds it, and a
// room that stops being rented (or is deleted) loses it. It runs in the transaction of the room change.
type contractObserver struct{ links *contractuc.RoomLinks }

// StatusChanged implements propertyuc.RoomObserver.
func (o contractObserver) StatusChanged(ctx context.Context, changes []propertydomain.StatusChange) error {
	var released []uuid.UUID
	for _, c := range changes {
		switch {
		case c.To == propertydomain.StatusRented && c.From != propertydomain.StatusRented:
			if err := o.links.Rented(ctx, c.RoomID); err != nil {
				return err
			}
		case c.From == propertydomain.StatusRented && c.To != propertydomain.StatusRented:
			released = append(released, c.RoomID)
		}
	}
	return o.links.Released(ctx, released)
}

// ContentChanged implements propertyuc.RoomObserver: contracts do not care how a room reads.
func (contractObserver) ContentChanged(context.Context, []uuid.UUID) error { return nil }

// Deleted implements propertyuc.RoomObserver.
func (o contractObserver) Deleted(ctx context.Context, roomIDs []uuid.UUID) error {
	return o.links.Deleted(ctx, roomIDs)
}

// roomStatus lets contracts rent and free rooms through property's use case, so its rules and its observer apply.
// The property use cases are built after the contract ones (they take the guards and the observer above), so the room
// use case is set once they exist.
type roomStatus struct{ rooms *propertyuc.Rooms }

// SetStatus implements contractuc.RoomStatusSetter.
func (s *roomStatus) SetStatus(ctx context.Context, landlordID, roomID uuid.UUID, status string) error {
	result, err := s.rooms.BulkSetStatus(ctx, landlordID, []uuid.UUID{roomID}, propertydomain.Status(status))
	if err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		return result.Failed[0].Err
	}
	return nil
}

// planLimits tells property and contract what the plan of an account allows, from the plan use cases. It asks the
// database on each call and caches nothing, and a call made with the ctx of a transaction sees that transaction, so
// the count a use case takes right after (under its lock) and the limit agree. A limit that is nil is unlimited.
type planLimits struct{ limits *planuc.Limits }

func (p planLimits) landlord(ctx context.Context, id uuid.UUID) (plandomain.Limits, error) {
	return p.limits.For(ctx, id, plandomain.AudienceLandlord)
}

// MaxBuildings implements propertyuc.PlanLimits.
func (p planLimits) MaxBuildings(ctx context.Context, landlordID uuid.UUID) (*int, error) {
	l, err := p.landlord(ctx, landlordID)
	return l.MaxBuildings, err
}

// MaxRooms implements propertyuc.PlanLimits.
func (p planLimits) MaxRooms(ctx context.Context, landlordID uuid.UUID) (*int, error) {
	l, err := p.landlord(ctx, landlordID)
	return l.MaxRooms, err
}

// MaxMediaPerOwner implements propertyuc.PlanLimits: the plan's pictures per room also cap a building.
func (p planLimits) MaxMediaPerOwner(ctx context.Context, landlordID uuid.UUID) (*int, error) {
	l, err := p.landlord(ctx, landlordID)
	return l.MaxImagesPerRoom, err
}

// MaxActiveContracts implements contractuc.PlanLimits: a landlord's running contracts.
func (p planLimits) MaxActiveContracts(ctx context.Context, landlordID uuid.UUID) (*int, error) {
	l, err := p.landlord(ctx, landlordID)
	return l.MaxActiveContracts, err
}

// MaxPendingRegistrations implements contractuc.PlanLimits: a tenant's registrations waiting for an answer.
func (p planLimits) MaxPendingRegistrations(ctx context.Context, tenantID uuid.UUID) (*int, error) {
	l, err := p.limits.For(ctx, tenantID, plandomain.AudienceTenant)
	return l.MaxPendingRegistrations, err
}

var (
	_ propertyuc.PlanLimits = planLimits{}
	_ contractuc.PlanLimits = planLimits{}
)

// listingGuards tells property which rooms a listing holds: a room with a listing that is waiting for review or live
// cannot be deleted (it has to be taken down first).
type listingGuards struct{ links *listinguc.RoomLinks }

// Blockers implements propertyuc.RoomGuards.
func (g listingGuards) Blockers(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]propertydomain.Blocker, error) {
	open, err := g.links.Open(ctx, roomIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]propertydomain.Blocker, len(open))
	for id := range open {
		out[id] = propertydomain.Blocker{OpenListing: true}
	}
	return out, nil
}

// listingObserver tells listings about rooms: when the content of a room changes, its waiting or live listing goes back
// to a draft, off find rooms, because what the admins saw (or approved) is not what renters would now see. It runs in
// the transaction of the room change. A change of status or a deletion is no concern of listings (a deleted room
// cannot have an open listing: the guard above refuses the deletion).
type listingObserver struct{ links *listinguc.RoomLinks }

// StatusChanged implements propertyuc.RoomObserver.
func (listingObserver) StatusChanged(context.Context, []propertydomain.StatusChange) error {
	return nil
}

// ContentChanged implements propertyuc.RoomObserver.
func (o listingObserver) ContentChanged(ctx context.Context, roomIDs []uuid.UUID) error {
	return o.links.ReturnToDraft(ctx, roomIDs)
}

// Deleted implements propertyuc.RoomObserver.
func (listingObserver) Deleted(context.Context, []uuid.UUID) error { return nil }

// roomGuards combines the guards of several contexts: a room is blocked by every guard that blocks it.
type roomGuards []propertyuc.RoomGuards

// Blockers implements propertyuc.RoomGuards.
func (gs roomGuards) Blockers(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]propertydomain.Blocker, error) {
	out := map[uuid.UUID]propertydomain.Blocker{}
	for _, g := range gs {
		found, err := g.Blockers(ctx, roomIDs)
		if err != nil {
			return nil, err
		}
		for id, b := range found {
			merged := out[id]
			merged.ActiveContract = merged.ActiveContract || b.ActiveContract
			merged.OpenListing = merged.OpenListing || b.OpenListing
			out[id] = merged
		}
	}
	return out, nil
}

// roomObservers tells several contexts about a change of rooms, in order; the first failure stops the rest (the
// transaction of the room change is undone).
type roomObservers []propertyuc.RoomObserver

// StatusChanged implements propertyuc.RoomObserver.
func (os roomObservers) StatusChanged(ctx context.Context, changes []propertydomain.StatusChange) error {
	for _, o := range os {
		if err := o.StatusChanged(ctx, changes); err != nil {
			return err
		}
	}
	return nil
}

// ContentChanged implements propertyuc.RoomObserver.
func (os roomObservers) ContentChanged(ctx context.Context, roomIDs []uuid.UUID) error {
	for _, o := range os {
		if err := o.ContentChanged(ctx, roomIDs); err != nil {
			return err
		}
	}
	return nil
}

// Deleted implements propertyuc.RoomObserver.
func (os roomObservers) Deleted(ctx context.Context, roomIDs []uuid.UUID) error {
	for _, o := range os {
		if err := o.Deleted(ctx, roomIDs); err != nil {
			return err
		}
	}
	return nil
}

// searchPriority tells the search of find rooms how high the plan of each landlord ranks their rooms, from the plan
// use cases (one query for the running plans of all of them).
type searchPriority struct{ limits *planuc.Limits }

// SearchPriorities implements listinguc.SearchPriority.
func (p searchPriority) SearchPriorities(ctx context.Context, landlordIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	return p.limits.SearchPriorities(ctx, landlordIDs)
}

var (
	_ propertyuc.RoomGuards    = roomGuards(nil)
	_ propertyuc.RoomGuards    = listingGuards{}
	_ propertyuc.RoomObserver  = roomObservers(nil)
	_ propertyuc.RoomObserver  = listingObserver{}
	_ listinguc.SearchPriority = searchPriority{}
)

// planFeatures tells invoices whether the plan of a landlord switches automatic invoicing on, from the plan use cases. Like
// the limits it asks the database on each call.
type planFeatures struct{ limits *planuc.Limits }

// AutoInvoicing implements invoiceuc.PlanFeatures.
func (p planFeatures) AutoInvoicing(ctx context.Context, landlordID uuid.UUID) (bool, error) {
	return p.limits.HasFeature(ctx, landlordID, plandomain.AudienceLandlord, plandomain.FeatureAutoInvoicing)
}

var _ invoiceuc.PlanFeatures = planFeatures{}

// invitationMailer lets contracts email an invitation through the mail adapter (which takes plain values).
type invitationMailer struct{ m *mailadapter.InvitationMailer }

// SendInvitation implements contractuc.InvitationMailer.
func (i invitationMailer) SendInvitation(ctx context.Context, m contractuc.InvitationMail) error {
	return i.m.Send(ctx, m.To, m.Landlord, m.Room, m.URL, m.ExpiresAt)
}

// The invitation context's use cases are the invitees port of contracts as they stand: both speak the shared kernel's
// types (invitation.Addressee, invitation.Person), so nothing has to be converted.
var (
	_ contractuc.InvitationMailer = invitationMailer{}
	_ contractuc.Invitees         = (*invitationuc.Invitees)(nil)
)
