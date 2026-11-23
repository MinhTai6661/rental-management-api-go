package listing

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// Reviews is the admins' use cases: they decide which listings go on find rooms. A draft is the landlord's own
// business until it is sent, so the admins never see one.
type Reviews struct{ *core }

// NewReviews builds the admins' use cases.
func NewReviews(d Deps) *Reviews { return &Reviews{newCore(d)} }

// List returns a page of the listings in a status (the waiting ones by default, oldest sent first) and how many
// there are per status, drafts left out.
func (s *Reviews) List(ctx context.Context, f ListFilter, limit, offset int) (Page, error) {
	status := domain.ReviewStatus(f.Status)
	f.Status = &status
	if f.Sort == "" {
		f.Sort = domain.DefaultSort(status)
	}
	return list(ctx, s.core, nil, true, f, limit, offset)
}

// Get returns one listing with its room's costs and pictures (domain.ErrNotFound for a draft).
func (s *Reviews) Get(ctx context.Context, id uuid.UUID) (domain.Detail, error) {
	d, err := s.detail(ctx, nil, id)
	if err != nil {
		return domain.Detail{}, err
	}
	if d.Status == domain.StatusDraft {
		return domain.Detail{}, domain.ErrNotFound
	}
	return s.withExtras(ctx, d)
}

// Approve puts the listing on find rooms and tells the landlord.
func (s *Reviews) Approve(ctx context.Context, adminID, id uuid.UUID, note *string) (domain.Detail, error) {
	return s.decide(ctx, adminID, id, AuditApproved, NotifyApproved, func(l *domain.Listing) (map[string]any, error) {
		return map[string]any{"note": note}, l.Approve(adminID, s.clock.Now(), note)
	})
}

// Reject refuses the listing with the reason the landlord is shown; they can fix it and send it again.
func (s *Reviews) Reject(ctx context.Context, adminID, id uuid.UUID, reason string) (domain.Detail, error) {
	return s.decide(ctx, adminID, id, AuditRejected, NotifyRejected, func(l *domain.Listing) (map[string]any, error) {
		return map[string]any{"reason": reason}, l.Reject(adminID, s.clock.Now(), reason)
	})
}

// TakeDown takes a live listing off find rooms, with the reason the landlord is shown; they can fix it and send it
// again.
func (s *Reviews) TakeDown(ctx context.Context, adminID, id uuid.UUID, reason string) (domain.Detail, error) {
	return s.decide(ctx, adminID, id, AuditTakenDown, NotifyTakenDown, func(l *domain.Listing) (map[string]any, error) {
		return map[string]any{"reason": reason}, l.TakeDown(adminID, s.clock.Now(), reason)
	})
}

// decide locks the listing, applies one decision, stores it with its audit record and tells the landlord.
func (s *Reviews) decide(ctx context.Context, adminID, id uuid.UUID, action, kind string,
	apply func(l *domain.Listing) (map[string]any, error),
) (domain.Detail, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		l, err := s.listings.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		meta, err := apply(&l)
		if err != nil {
			return err
		}
		if err := s.listings.Save(ctx, l); err != nil {
			return err
		}
		return s.record(ctx, action, adminID, l, meta)
	})
	if err != nil {
		return domain.Detail{}, err
	}
	d, err := s.detail(ctx, nil, id)
	if err != nil {
		return domain.Detail{}, err
	}
	s.tellLandlord(ctx, kind, d)
	return s.withExtras(ctx, d)
}

// Bulk approves or rejects several listings; one that cannot be reviewed is reported and the others still go
// through. A rejection carries one reason for all of them. An id that is not a listing is refused up front
// (domain.ErrItemsNotFound).
func (s *Reviews) Bulk(ctx context.Context, adminID uuid.UUID, action Action, ids []uuid.UUID, note string) (BulkResult, error) {
	found, err := s.listings.ByIDs(ctx, ids)
	if err != nil {
		return BulkResult{}, err
	}
	byID := make(map[uuid.UUID]domain.Listing, len(found))
	for _, l := range found {
		byID[l.ID] = l
	}
	if len(byID) != len(distinct(ids)) {
		return BulkResult{}, domain.ErrItemsNotFound
	}
	result := BulkResult{Done: []uuid.UUID{}}
	for _, id := range distinct(ids) {
		var err error
		if action == ActionApprove {
			var n *string
			if note != "" {
				n = &note
			}
			_, err = s.Approve(ctx, adminID, id, n)
		} else {
			_, err = s.Reject(ctx, adminID, id, note)
		}
		if err != nil {
			if !isRefusal(err) {
				return result, err
			}
			result.Failed = append(result.Failed, BulkFailure{ID: id, Name: byID[id].Title, Err: err})
			continue
		}
		result.Done = append(result.Done, id)
	}
	return result, nil
}

// isRefusal says whether err is one of the domain's reasons for refusing a decision (the others are failures of
// the system and stop the bulk run).
func isRefusal(err error) bool {
	return errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrNoteRequired) || errors.Is(err, domain.ErrNotFound)
}

func distinct(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
