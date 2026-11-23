package listing

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// Seeder writes demo listings.
type Seeder struct{ *core }

// NewSeeder returns a seeder.
func NewSeeder(d Deps) *Seeder { return &Seeder{newCore(d)} }

// demoStatuses is the status the demo listings of a landlord's rooms take, in the order of the rooms: most are
// approved (so find rooms has something to show), one waits for the admins (to try the review screen), one is
// refused and one is still a draft.
var demoStatuses = []domain.Status{
	domain.StatusApproved, domain.StatusApproved, domain.StatusApproved, domain.StatusPending, domain.StatusRejected,
	domain.StatusDraft,
}

// SeedDemo writes a listing for each available room of the landlord that has none yet and returns how many it wrote.
// reviewerID is the admin who "reviewed" the approved and refused ones (nil: nobody). It is safe to repeat.
func (s *Seeder) SeedDemo(ctx context.Context, landlordID uuid.UUID, reviewerID *uuid.UUID) (int, error) {
	rooms, err := s.seed.WithoutListing(ctx, landlordID)
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	done := 0
	for i, room := range rooms {
		l := demoListing(room, landlordID, reviewerID, demoStatuses[i%len(demoStatuses)], now)
		if err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			_, err := s.listings.Insert(ctx, l)
			return err
		}); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// demoListing is the demo listing of a room in a status.
func demoListing(room domain.SeedRoom, landlordID uuid.UUID, reviewer *uuid.UUID, status domain.Status, now time.Time) domain.Listing {
	l := domain.Listing{
		RoomID: room.ID, LandlordID: landlordID, Title: "Phòng " + room.Name + " cho thuê", Description: room.Description,
		Status: status,
	}
	if status != domain.StatusDraft {
		l.SubmittedAt = &now
	}
	if status == domain.StatusApproved || status == domain.StatusRejected {
		l.ReviewedByID, l.ReviewedAt = reviewer, &now
	}
	switch status {
	case domain.StatusApproved:
		l.PublishedAt = &now
	case domain.StatusRejected:
		note := "Cần bổ sung thêm ảnh thực tế của phòng"
		l.ReviewNote = &note
	}
	return l
}
