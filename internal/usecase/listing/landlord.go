package listing

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

// Listings is the landlord's use cases: they write, send, take down and delete their own listings. Every lookup is
// scoped to the landlord, so somebody else's listing is not found.
type Listings struct{ *core }

// NewListings builds the landlord's use cases.
func NewListings(d Deps) *Listings { return &Listings{newCore(d)} }

// List returns a page of the landlord's listings (newest first unless sorted) and how many there are per status.
func (s *Listings) List(ctx context.Context, landlordID uuid.UUID, f ListFilter, limit, offset int) (Page, error) {
	return list(ctx, s.core, &landlordID, false, f, limit, offset)
}

// list is the paged list the landlord and the admins share; excludeDrafts hides the drafts (the admins').
func list(ctx context.Context, c *core, landlordID *uuid.UUID, excludeDrafts bool, f ListFilter, limit, offset int) (Page, error) {
	q := domain.Query{
		LandlordID: landlordID, Status: f.Status, ExcludeDrafts: excludeDrafts, SearchLike: domain.SearchPattern(f.Search),
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo, Sort: f.Sort, Limit: limit, Offset: offset,
	}
	items, total, err := c.listings.Search(ctx, q)
	if err != nil {
		return Page{}, err
	}
	counts, err := c.listings.Counts(ctx, landlordID, excludeDrafts)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Counts: counts}, nil
}

// Get returns one of the landlord's listings with its room's costs and pictures.
func (s *Listings) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.Detail, error) {
	d, err := s.detail(ctx, &landlordID, id)
	if err != nil {
		return domain.Detail{}, err
	}
	return s.withExtras(ctx, d)
}

// Create writes a draft that offers one of the landlord's rooms. A room is offered by one listing at a time, so it
// is refused while the room has one that is waiting or live.
func (s *Listings) Create(ctx context.Context, landlordID uuid.UUID, in CreateInput) (domain.Detail, error) {
	var id uuid.UUID
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		room, err := s.rooms.Lock(ctx, in.RoomID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return domain.ErrRoomNotFound
			}
			return err
		}
		// Somebody else's room is "not found", like a room that does not exist.
		if !room.Live || room.LandlordID != landlordID {
			return domain.ErrRoomNotFound
		}
		if err := s.assertRoomFree(ctx, room, nil); err != nil {
			return err
		}
		l := domain.Listing{RoomID: room.ID, LandlordID: landlordID, Title: in.Title, Description: in.Description, Status: domain.StatusDraft}
		if id, err = s.listings.Insert(ctx, l); err != nil {
			if shared.IsUnique(err) {
				return &domain.RoomAlreadyListedError{Room: room.Name}
			}
			return err
		}
		l.ID = id
		return s.record(ctx, AuditCreated, landlordID, l, nil)
	})
	if err != nil {
		return domain.Detail{}, err
	}
	return s.detail(ctx, &landlordID, id)
}

// Update changes the headline and the text. A listing that was waiting or live goes back to a draft (off find rooms
// and out of the admins' queue) and the landlord sends it again when ready, like when the room behind it changes.
// Saving without a change leaves it alone.
func (s *Listings) Update(ctx context.Context, landlordID, id uuid.UUID, in UpdateInput) (domain.Detail, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.detail(ctx, &landlordID, id); err != nil {
			return err
		}
		l, err := s.listings.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		changed, back, err := l.Edit(in.Title, in.Description.Value, in.Description.Set)
		if err != nil || !changed {
			return err
		}
		if err := s.listings.Save(ctx, l); err != nil {
			return err
		}
		return s.record(ctx, AuditUpdated, landlordID, l, map[string]any{"back_to_draft": back})
	})
	if err != nil {
		return domain.Detail{}, err
	}
	return s.detail(ctx, &landlordID, id)
}

// Submit sends the listing to the admins (also after a refusal or after a take-down) and tells them.
func (s *Listings) Submit(ctx context.Context, landlordID, id uuid.UUID) (domain.Detail, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.detail(ctx, &landlordID, id)
		if err != nil {
			return err
		}
		room, err := s.rooms.Lock(ctx, d.RoomID)
		if err != nil {
			return notFound(err)
		}
		if !room.Live {
			return domain.ErrRoomNotFound
		}
		l, err := s.listings.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := l.Submit(s.clock.Now()); err != nil {
			return err
		}
		if err := s.assertRoomFree(ctx, room, &id); err != nil {
			return err
		}
		if err := s.checkLimit(ctx, landlordID); err != nil {
			return err
		}
		if err := s.listings.Save(ctx, l); err != nil {
			if shared.IsUnique(err) {
				return &domain.RoomAlreadyListedError{Room: room.Name}
			}
			return err
		}
		return s.record(ctx, AuditSubmitted, landlordID, l, nil)
	})
	if err != nil {
		return domain.Detail{}, err
	}
	d, err := s.detail(ctx, &landlordID, id)
	if err != nil {
		return domain.Detail{}, err
	}
	s.tellAdmins(ctx, d)
	return d, nil
}

// Unpublish takes a live listing off find rooms.
func (s *Listings) Unpublish(ctx context.Context, landlordID, id uuid.UUID) (domain.Detail, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.detail(ctx, &landlordID, id); err != nil {
			return err
		}
		l, err := s.listings.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := l.Unpublish(s.clock.Now()); err != nil {
			return err
		}
		if err := s.listings.Save(ctx, l); err != nil {
			return err
		}
		return s.record(ctx, AuditUnpublished, landlordID, l, nil)
	})
	if err != nil {
		return domain.Detail{}, err
	}
	return s.detail(ctx, &landlordID, id)
}

// Delete removes a listing that is not live; a live one has to be taken down first.
func (s *Listings) Delete(ctx context.Context, landlordID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.detail(ctx, &landlordID, id); err != nil {
			return err
		}
		l, err := s.listings.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := l.CheckDelete(); err != nil {
			return err
		}
		if err := s.listings.Delete(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, AuditDeleted, landlordID, l, map[string]any{"status": string(l.Status)})
	})
}

// assertRoomFree refuses a second open listing for a room (except is the listing being sent).
func (c *core) assertRoomFree(ctx context.Context, room domain.RoomFacts, except *uuid.UUID) error {
	taken, err := c.listings.OpenOfRoom(ctx, room.ID, except)
	if err != nil {
		return err
	}
	if taken {
		return &domain.RoomAlreadyListedError{Room: room.Name}
	}
	return nil
}

// checkLimit refuses one more open listing past the landlord's plan limit.
func (c *core) checkLimit(ctx context.Context, landlordID uuid.UUID) error {
	limit, err := c.plans.MaxActiveListings(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	if err := c.listings.LockLandlord(ctx, landlordID); err != nil {
		return err
	}
	open, err := c.listings.CountOpen(ctx, landlordID)
	if err != nil {
		return err
	}
	if open >= *limit {
		return &domain.LimitError{Limit: *limit}
	}
	return nil
}
