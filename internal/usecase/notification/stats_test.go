package notification

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

func newStatsWorld() (*Statistics, *statsFake, *annWorld) {
	w := newAnnWorld()
	fake := &statsFake{messages: domain.Messages{Total: 2, Recipients: 4}}
	return NewStatistics(Deps{Announcements: w.repo, Stats: fake, Clock: ClockFunc(func() time.Time { return w.now })}, Options{}), fake, w
}

func TestOverviewScopesToTheSenderAndTheDays(t *testing.T) {
	uc, fake, w := newStatsWorld()
	r, err := uc.Overview(ctx, landlordS, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fake.queries[0].LandlordID == nil || *fake.queries[0].LandlordID != landlordS.UserID {
		t.Error("a landlord sees their own")
	}
	if len(r.Stats.Series) != domain.DefaultStatDays || r.Stats.Messages.Total != 2 || r.Stats.Totals.Notification.Recipients != 4 {
		t.Errorf("%d days, %+v", len(r.Stats.Series), r.Stats)
	}
	if r.Last.Format("2006-01-02") != w.now.In(ictZone).Format("2006-01-02") {
		t.Errorf("ends today in ICT: %v", r.Last)
	}
	if _, err := uc.Overview(ctx, adminS, nil, nil, nil); err != nil || fake.queries[len(fake.queries)-1].LandlordID != nil {
		t.Errorf("the platform's: %v", err)
	}
	if _, err := uc.Overview(ctx, domain.Sender{UserID: uuid.New(), Role: "tenant"}, nil, nil, nil); !errors.Is(err, domain.ErrScopeNotAllowed) {
		t.Errorf("a tenant sends nothing: %v", err)
	}
}

func TestOverviewRangeErrors(t *testing.T) {
	uc, _, _ := newStatsWorld()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, ictZone)
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, ictZone)
	if _, err := uc.Overview(ctx, adminS, &from, &to, nil); !errors.Is(err, domain.ErrRangeTooLong) {
		t.Errorf("a year: %v", err)
	}
	if _, err := uc.Overview(ctx, adminS, &to, &from, nil); !errors.Is(err, domain.ErrRangeBackwards) {
		t.Errorf("backwards: %v", err)
	}
}

func TestDetailChecksOwnershipAndUsesTheRecipientCount(t *testing.T) {
	uc, fake, w := newStatsWorld()
	w.repo.people = users(3)
	d, err := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	if err != nil {
		t.Fatal(err)
	}
	popup := domain.ChannelPopup
	r, err := uc.Detail(ctx, adminS, d.ID, nil, nil, &popup)
	if err != nil || r.Stats.Totals.Notification != nil {
		t.Fatalf("only the popup was asked for: %v %+v", err, r.Stats.Totals)
	}
	if _, err := uc.Detail(ctx, adminS, d.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.queries[len(fake.queries)-2]; got.AnnouncementID == nil || *got.AnnouncementID != d.ID {
		t.Errorf("the query names the message: %+v", got)
	}
	if _, err := uc.Detail(ctx, landlordS, d.ID, nil, nil, nil); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another sender's: %v", err)
	}
}
