package notification

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

var (
	adminS    = domain.Sender{UserID: uuid.New(), Role: "admin"}
	superS    = domain.Sender{UserID: uuid.New(), Role: "super_admin"}
	landlordS = domain.Sender{UserID: uuid.New(), Role: "landlord"}
)

type annWorld struct {
	uc    *Announcements
	repo  *annFake
	push  *pushFake
	audit *auditFake
	now   time.Time
}

func newAnnWorld() *annWorld {
	w := &annWorld{repo: newAnnFake(), push: &pushFake{}, audit: &auditFake{}}
	w.now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	w.uc = NewAnnouncements(Deps{
		Tx: txFake{}, Announcements: w.repo, Pusher: w.push, Audit: w.audit, Clock: ClockFunc(func() time.Time { return w.now }),
	}, Options{})
	return w
}

func good(scope domain.Scope) domain.Content {
	return domain.Content{
		Scope: scope, Type: domain.TypeNotice, SendNotification: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium,
		Title: domain.Texts{"vi": "Chào", "en": "Hi"}, Body: domain.Texts{"vi": "Nội dung", "en": "Text"},
	}
}

func users(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func TestCreateAndPublishDeliversToTheAudience(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(3)
	d, err := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.repo.notes) != 3 || d.RecipientCount != 3 || d.Status != domain.StatusActive || d.SentAt == nil {
		t.Fatalf("notes %d, count %d, status %s", len(w.repo.notes), d.RecipientCount, d.Status)
	}
	n := w.repo.notes[0]
	if n.params["title_vi"] != "Chào" || n.params["body_en"] != "Text" || n.params["announcement_id"] != d.ID.String() || n.audience != domain.AudienceTenant {
		t.Errorf("params %v audience %s", n.params, n.audience)
	}
	if len(w.push.calls) != 1 || w.push.last.Kind != domain.KindAnnouncement || w.push.last.URL != BellURL {
		t.Errorf("one push of the announcement kind: %+v", w.push)
	}
	if got := w.audit.actions; len(got) != 2 || got[0] != AuditAnnouncementCreated || got[1] != AuditAnnouncementSent {
		t.Errorf("audit: %v", got)
	}
}

func TestPushesInChunksOf100(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(250)
	if _, err := w.uc.Create(ctx, adminS, good(domain.ScopeEveryone), true); err != nil {
		t.Fatal(err)
	}
	if len(w.push.calls) != 3 || len(w.push.calls[0]) != 100 || len(w.push.calls[2]) != 50 {
		t.Errorf("chunks: %d", len(w.push.calls))
	}
}

func TestDraftDeliversNothingAndScheduledWaitsForDispatchDue(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(2)
	draft, err := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), false)
	if err != nil || draft.Status != domain.StatusDraft || len(w.repo.notes) != 0 {
		t.Fatalf("draft: %v %s %d", err, draft.Status, len(w.repo.notes))
	}

	later := w.now.Add(2 * time.Minute)
	c := good(domain.ScopeTenants)
	c.StartsAt = &later
	scheduled, err := w.uc.Create(ctx, adminS, c, true)
	if err != nil || scheduled.Status != domain.StatusScheduled || len(w.repo.notes) != 0 {
		t.Fatalf("scheduled: %v %s %d", err, scheduled.Status, len(w.repo.notes))
	}
	if n, err := w.uc.DispatchDue(ctx); err != nil || n != 0 {
		t.Errorf("not yet due: %d %v", n, err)
	}
	w.now = later.Add(time.Second)
	if n, err := w.uc.DispatchDue(ctx); err != nil || n != 1 || len(w.repo.notes) != 2 {
		t.Errorf("due now: %d %v %d", n, err, len(w.repo.notes))
	}
	if n, _ := w.uc.DispatchDue(ctx); n != 0 || len(w.repo.notes) != 2 {
		t.Errorf("a second run delivers nothing: %d %d", n, len(w.repo.notes))
	}
}

func TestDeliverTwiceDeliversOnce(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(2)
	d, _ := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	if n, err := w.uc.Deliver(ctx, d.ID); err != nil || n != 0 {
		t.Errorf("the second call: %d %v", n, err)
	}
	if len(w.repo.notes) != 2 {
		t.Errorf("%d notifications", len(w.repo.notes))
	}
}

func TestDispatchDueGoesOnAfterOneFailure(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(1)
	later := w.now.Add(time.Minute)
	mk := func() uuid.UUID {
		c := good(domain.ScopeTenants)
		c.StartsAt = &later
		d, err := w.uc.Create(ctx, adminS, c, true)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	broken, fine := mk(), mk()
	w.repo.deliverErr[broken] = errBoom
	w.now = later.Add(time.Second)
	n, err := w.uc.DispatchDue(ctx)
	if err != nil || n != 1 {
		t.Fatalf("one message delivered: %d %v", n, err)
	}
	if w.repo.items[fine].SentAt == nil || len(w.repo.notes) != 1 {
		t.Error("the good one went out")
	}
}

func TestPopupOnlyMessageStoresTheReachAsRecipientCount(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(4)
	c := good(domain.ScopeTenants)
	c.SendNotification, c.ShowPopup = false, true
	d, err := w.uc.Create(ctx, adminS, c, true)
	if err != nil || len(w.repo.notes) != 0 || d.RecipientCount != 4 {
		t.Errorf("%v %d %d", err, len(w.repo.notes), d.RecipientCount)
	}
}

func TestLandlordMessageNeedsPeopleAndPickedAreCutDown(t *testing.T) {
	w := newAnnWorld()
	if _, err := w.uc.Create(ctx, landlordS, good(domain.ScopeMyTenants), true); !errors.Is(err, domain.ErrNoPeople) {
		t.Errorf("nobody: %v", err)
	}
	mine, stranger := users(2), uuid.New()
	w.repo.mine = mine
	c := good(domain.ScopeSelectedPeople)
	c.PeopleIDs = []uuid.UUID{mine[0], stranger}
	d, err := w.uc.Create(ctx, landlordS, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.repo.recipients[d.ID]; len(got) != 1 || got[0] != mine[0] {
		t.Errorf("only the landlord's tenant is kept: %v", got)
	}
	if len(w.repo.notes) != 1 || w.repo.notes[0].user != mine[0] {
		t.Errorf("one notification: %d", len(w.repo.notes))
	}
	c.PeopleIDs = []uuid.UUID{stranger}
	if _, err := w.uc.Create(ctx, landlordS, c, true); !errors.Is(err, domain.ErrNoPeople) {
		t.Errorf("only strangers: %v", err)
	}
}

func TestScopeNotAllowedAndInvalidContent(t *testing.T) {
	w := newAnnWorld()
	if _, err := w.uc.Create(ctx, landlordS, good(domain.ScopeEveryone), true); !errors.Is(err, domain.ErrScopeNotAllowed) {
		t.Errorf("landlord to everyone: %v", err)
	}
	if _, err := w.uc.Create(ctx, adminS, good(domain.ScopeMyTenants), true); !errors.Is(err, domain.ErrScopeNotAllowed) {
		t.Errorf("admin to my tenants: %v", err)
	}
	c := good(domain.ScopeTenants)
	c.Body = domain.Texts{"en": "Text"}
	var ve *domain.ValidationError
	if _, err := w.uc.Create(ctx, adminS, c, true); !errors.As(err, &ve) {
		t.Errorf("title without body: %v", err)
	}
	if w.repo.inserted != 0 {
		t.Error("nothing is stored for a refused message")
	}
}

func TestOwnershipIsPerSenderKind(t *testing.T) {
	w := newAnnWorld()
	w.repo.mine, w.repo.people = users(1), users(1)
	platform, _ := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), false)
	own, _ := w.uc.Create(ctx, landlordS, good(domain.ScopeMyTenants), false)
	other := domain.Sender{UserID: uuid.New(), Role: "landlord"}

	if _, err := w.uc.Get(ctx, superS, platform.ID); err != nil {
		t.Errorf("any staff sees the platform's: %v", err)
	}
	for name, c := range map[string]struct {
		s  domain.Sender
		id uuid.UUID
	}{
		"admin reads a landlord's":    {adminS, own.ID},
		"other landlord reads":        {other, own.ID},
		"landlord reads the platform": {landlordS, platform.ID},
	} {
		if _, err := w.uc.Get(ctx, c.s, c.id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := w.uc.Publish(ctx, c.s, c.id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s publish: %v", name, err)
		}
		if err := w.uc.Recall(ctx, c.s, c.id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s recall: %v", name, err)
		}
	}
}

func TestRecallRemovesNotificationsAndCannotBeRepeated(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(3)
	d, _ := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	if err := w.uc.Recall(ctx, adminS, d.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.repo.notes) != 0 {
		t.Errorf("%d left", len(w.repo.notes))
	}
	if err := w.uc.Recall(ctx, adminS, d.ID); !errors.Is(err, domain.ErrRecalled) {
		t.Errorf("again: %v", err)
	}
	if _, err := w.uc.Update(ctx, adminS, d.ID, good(domain.ScopeTenants), false, false); !errors.Is(err, domain.ErrRecalled) {
		t.Errorf("update: %v", err)
	}
	if _, err := w.uc.Publish(ctx, adminS, d.ID); !errors.Is(err, domain.ErrRecalled) {
		t.Errorf("publish: %v", err)
	}
}

func TestUpdateReshowBumpsRevisionAndNotificationsKeepTheirWords(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(1)
	d, _ := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	c := good(domain.ScopeTenants)
	c.Title = domain.Texts{"vi": "Mới", "en": "New"}
	got, err := w.uc.Update(ctx, adminS, d.ID, c, false, true)
	if err != nil || got.Revision != 2 || got.Title["en"] != "New" {
		t.Fatalf("%v %+v", err, got)
	}
	if w.repo.notes[0].params["title_en"] != "Hi" {
		t.Error("the sent notification keeps its words")
	}
	again, _ := w.uc.Update(ctx, adminS, d.ID, c, false, false)
	if again.Revision != 2 {
		t.Errorf("no reshow, no bump: %d", again.Revision)
	}
}

func TestUnpublishStopsAndRepublishDoesNotResend(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(2)
	d, _ := w.uc.Create(ctx, adminS, good(domain.ScopeTenants), true)
	u, err := w.uc.Unpublish(ctx, adminS, d.ID)
	if err != nil || u.Status != domain.StatusDraft {
		t.Fatalf("%v %s", err, u.Status)
	}
	if _, err := w.uc.Publish(ctx, adminS, d.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.repo.notes) != 2 {
		t.Errorf("not sent twice: %d", len(w.repo.notes))
	}
}

func TestAdminTakesOverASuperAdminMessageAtTheirOwnRole(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(1)
	c := good(domain.ScopeCustom)
	c.Criteria = domain.Criteria{Roles: []string{"admin"}}
	d, err := w.uc.Create(ctx, superS, c, false)
	if err != nil || d.SenderRole != "super_admin" {
		t.Fatalf("%v %s", err, d.SenderRole)
	}
	// The admin may not keep a message aimed at admins.
	var ve *domain.ValidationError
	if _, err := w.uc.Update(ctx, adminS, d.ID, c, false, false); !errors.As(err, &ve) {
		t.Fatalf("expected a validation error: %v", err)
	}
	c.Criteria = domain.Criteria{Roles: []string{"tenant"}}
	got, err := w.uc.Update(ctx, adminS, d.ID, c, false, false)
	if err != nil || got.SenderRole != "admin" {
		t.Errorf("%v %s", err, got.SenderRole)
	}
	// A super admin editing an admin's message keeps the admin role.
	got, err = w.uc.Update(ctx, superS, d.ID, c, false, false)
	if err != nil || got.SenderRole != "admin" {
		t.Errorf("%v %s", err, got.SenderRole)
	}
}

func TestStoredSenderDecidesTheFilterNotTheCreatorsCurrentRole(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(1)
	c := good(domain.ScopeCustom)
	c.Criteria = domain.Criteria{Roles: []string{"admin"}}
	if _, err := w.uc.Create(ctx, superS, c, true); err != nil {
		t.Fatal(err)
	}
	last := w.repo.filters[len(w.repo.filters)-1]
	if len(last.ExcludedRoles) != 1 || last.ExcludedRoles[0] != "super_admin" || len(last.Roles) != 1 || last.Roles[0] != "admin" {
		t.Errorf("a super admin's message reaches admins only: %+v", last)
	}
}

func TestReachCountsWithTheDeliveryDefinition(t *testing.T) {
	w := newAnnWorld()
	w.repo.people = users(5)
	n, err := w.uc.Reach(ctx, adminS, ReachInput{Scope: domain.ScopeTenants})
	if err != nil || n != 5 {
		t.Errorf("%d %v", n, err)
	}
	if _, err := w.uc.Reach(ctx, adminS, ReachInput{Scope: domain.ScopeCustom}); !errors.Is(err, domain.ErrAudienceEmpty) {
		t.Errorf("custom without criteria: %v", err)
	}
	if _, err := w.uc.Reach(ctx, landlordS, ReachInput{Scope: domain.ScopeTenants}); !errors.Is(err, domain.ErrScopeNotAllowed) {
		t.Errorf("landlord to tenants: %v", err)
	}
	if got := w.repo.filters[0]; got.Scope != "tenants" {
		t.Errorf("filter: %+v", got)
	}
}

func TestPeopleIsForLandlordsOnly(t *testing.T) {
	w := newAnnWorld()
	if _, _, err := w.uc.People(ctx, adminS, "", 20, 0); !errors.Is(err, domain.ErrScopeNotAllowed) {
		t.Errorf("%v", err)
	}
	if _, _, err := w.uc.People(ctx, landlordS, "", 20, 0); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSeedWelcomeDraftOnceAndOnlyWithAnAdmin(t *testing.T) {
	repo := newAnnFake()
	s := NewSeeder(Deps{Notifications: &repoFake{}, Announcements: repo}, Options{})
	admin := uuid.New()
	n, err := s.SeedDemo(ctx, map[string]uuid.UUID{"admin@rental.test": admin})
	if err != nil || n != 2 || repo.inserted != 1 {
		t.Fatalf("%d %v %d", n, err, repo.inserted)
	}
	for _, a := range repo.items {
		if a.Type != domain.TypeWelcome || a.PublishedAt != nil || !a.ShowPopup || a.SendNotification || a.Criteria.RegisteredWithinDays == nil || *a.Criteria.RegisteredWithinDays != 14 {
			t.Errorf("a draft welcome popup: %+v", a)
		}
	}
	repo.welcome = true
	if n, _ := s.SeedDemo(ctx, map[string]uuid.UUID{"admin@rental.test": admin}); n != 0 || repo.inserted != 1 {
		t.Errorf("the second run adds nothing: %d %d", n, repo.inserted)
	}
	if n, _ := s.SeedDemo(ctx, map[string]uuid.UUID{}); n != 0 {
		t.Errorf("no admin, no draft: %d", n)
	}
}
