package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func tp(t time.Time) *time.Time { return &t }

func TestStatusAt(t *testing.T) {
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := map[string]struct {
		a    Announcement
		want Status
	}{
		"draft":            {Announcement{}, StatusDraft},
		"recalled draft":   {Announcement{RecalledAt: tp(past)}, StatusRecalled},
		"recalled live":    {Announcement{RecalledAt: tp(past), PublishedAt: tp(past)}, StatusRecalled},
		"scheduled":        {Announcement{PublishedAt: tp(past), StartsAt: tp(future)}, StatusScheduled},
		"started":          {Announcement{PublishedAt: tp(past), StartsAt: tp(past)}, StatusActive},
		"ended":            {Announcement{PublishedAt: tp(past), EndsAt: tp(past)}, StatusEnded},
		"ends exactly now": {Announcement{PublishedAt: tp(past), EndsAt: tp(now)}, StatusEnded},
		"active":           {Announcement{PublishedAt: tp(past), EndsAt: tp(future)}, StatusActive},
	}
	for name, c := range cases {
		if got := c.a.StatusAt(now); got != c.want {
			t.Errorf("%s: %s", name, got)
		}
	}
}

func TestOwned(t *testing.T) {
	other := uuid.New()
	platform := Announcement{SenderRole: "super_admin"}
	mine := Announcement{SenderRole: "landlord", LandlordID: &landlord.UserID}
	theirs := Announcement{SenderRole: "landlord", LandlordID: &other}
	if !platform.Owned(admin) || !platform.Owned(super) || platform.Owned(landlord) {
		t.Error("platform messages belong to any staff")
	}
	if !mine.Owned(landlord) || theirs.Owned(landlord) || mine.Owned(admin) {
		t.Error("a landlord's message belongs to that landlord only")
	}
}

func TestDeliveryDue(t *testing.T) {
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	base := Announcement{PublishedAt: tp(past), SendNotification: true}
	if !base.DeliveryDue(now) {
		t.Error("published with a notification")
	}
	for name, a := range map[string]Announcement{
		"draft":       {SendNotification: true},
		"popup only":  {PublishedAt: tp(past)},
		"sent":        {PublishedAt: tp(past), SendNotification: true, SentAt: tp(past)},
		"recalled":    {PublishedAt: tp(past), SendNotification: true, RecalledAt: tp(past)},
		"not started": {PublishedAt: tp(past), SendNotification: true, StartsAt: tp(future)},
	} {
		if a.DeliveryDue(now) {
			t.Errorf("%s is not due", name)
		}
	}
}

func TestScopeRules(t *testing.T) {
	if ScopeLandlords.NotificationAudience() != AudienceLandlord || ScopeMyTenants.NotificationAudience() != AudienceTenant ||
		ScopeEveryone.NotificationAudience() != AudienceNone || ScopeCustom.NotificationAudience() != AudienceNone {
		t.Error("notification audiences")
	}
	for _, s := range Scopes() {
		if p, ok := ParseScope(string(s)); !ok || p != s {
			t.Errorf("parse %s", s)
		}
	}
	if _, ok := ParseScope("selected_tenants"); ok {
		t.Error("the legacy alias is dropped")
	}
}
