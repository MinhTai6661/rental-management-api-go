package report

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Seeder writes demo reports through the use cases (so the rules, the audit trail and the notifications apply to the
// demo data too). It is safe to run again: a report whose subject the tenant already wrote is left alone.
type Seeder struct{ *core }

// NewSeeder returns the seeder.
func NewSeeder(d Deps) *Seeder { return &Seeder{newCore(d)} }

// Subjects of the demo reports (they are also how a second run recognises them).
const (
	demoRoomSubject    = "Demo: the tap in the bathroom leaks"
	demoFeatureSubject = "Demo: please add a dark mode"
)

// SeedDemo writes a room report of the demo tenant with a short conversation with the landlord, and a feature report
// with an answer from the admin. It returns how many reports it wrote.
func (s *Seeder) SeedDemo(ctx context.Context, tenantID, landlordID, adminID uuid.UUID) (int, error) {
	tenant := domain.Viewer{UserID: tenantID, Role: authz.RoleTenant}
	landlord := domain.Viewer{UserID: landlordID, Role: authz.RoleLandlord}
	admin := domain.Viewer{UserID: adminID, Role: authz.RoleAdmin}
	written := 0

	known, err := s.known(ctx, tenant, demoRoomSubject)
	if err != nil {
		return written, err
	}
	if !known {
		room, err := s.Rooms.FirstRentedFrom(ctx, tenantID, landlordID)
		if err != nil && !errors.Is(err, shared.ErrNotFound) {
			return written, err
		}
		if err == nil {
			created, err := s.newReports().Create(ctx, tenant, CreateInput{
				Kind: domain.KindRoom, RoomID: &room.ID, Subject: demoRoomSubject,
				Body: "Water drips from the tap all night. Could somebody have a look this week?",
			})
			if err != nil {
				return written, err
			}
			written++
			uc := s.newReports()
			if _, err := uc.Reply(ctx, landlord, created.ID, "Thanks for telling me, a plumber comes on Thursday."); err != nil {
				return written, err
			}
			if _, err := uc.SetStatus(ctx, landlord, created.ID, domain.StatusInProgress); err != nil {
				return written, err
			}
			if _, err := uc.Reply(ctx, tenant, created.ID, "Thursday is fine, I will be at home."); err != nil {
				return written, err
			}
		}
	}

	known, err = s.known(ctx, tenant, demoFeatureSubject)
	if err != nil || known {
		return written, err
	}
	uc := s.newReports()
	created, err := uc.Create(ctx, tenant, CreateInput{
		Kind: domain.KindFeature, Subject: demoFeatureSubject, Body: "I read the bills at night; a dark theme would be easier on the eyes.",
	})
	if err != nil {
		return written, err
	}
	if _, err := uc.Reply(ctx, admin, created.ID, "Good idea, we noted it."); err != nil {
		return written, err
	}
	return written + 1, nil
}

func (s *Seeder) newReports() *Reports { return &Reports{s.core} }

// known says whether the tenant already wrote a report with the subject.
func (s *Seeder) known(ctx context.Context, tenant domain.Viewer, subject string) (bool, error) {
	_, total, err := s.Reports.ListMine(ctx, tenant.UserID, domain.Filter{Search: subject}, 1, 0)
	return total > 0, err
}
