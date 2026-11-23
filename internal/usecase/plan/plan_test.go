package plan

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

var ctx = context.Background()

// kit is a landlord who is also a tenant, an admin and a catalog with a default and a paid plan on each side.
type kit struct {
	*world
	landlord, tenant, admin uuid.UUID
	lFree, lPro, tFree, tPl *domain.Plan
}

func newKit() *kit {
	w := newWorld()
	k := &kit{world: w}
	k.landlord = w.store.account("Lan", authz.RoleLandlord, authz.RoleTenant)
	k.tenant = w.store.account("Tam", authz.RoleTenant)
	k.admin = w.store.account("Ada", authz.RoleAdmin)
	k.lFree = w.store.plan("landlord_free", domain.AudienceLandlord, 0)
	k.lFree.IsDefault, k.lFree.Limits = true, domain.Limits{MaxRooms: n(2), MaxBuildings: n(1), MaxImagesPerRoom: n(4)}
	k.lPro = w.store.plan("landlord_pro", domain.AudienceLandlord, 199_000)
	k.lPro.SearchPriority = 10
	k.tFree = w.store.plan("tenant_free", domain.AudienceTenant, 0)
	k.tFree.IsDefault, k.tFree.Limits = true, domain.Limits{MaxPendingRegistrations: n(3)}
	k.tPl = w.store.plan("tenant_plus", domain.AudienceTenant, 49_000)
	k.tPl.Limits = domain.Limits{MaxPendingRegistrations: n(10)}
	return k
}

func TestSubscribeToAFreePlanTakesEffectAtOnce(t *testing.T) {
	k := newKit()
	other := k.store.plan("landlord_other_free", domain.AudienceLandlord, 0)

	got, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusActive || got.EndsAt != nil || got.GrantedByID != nil || got.Plan.Code != other.Code {
		t.Fatalf("a free plan is active with no end and nobody granted it: %+v", got)
	}
	if !slices.Equal(k.rec.actions(), []string{AuditSubscriptionAssigned}) || len(k.rec.notices) != 0 {
		t.Errorf("audit %v, notices %d", k.rec.actions(), len(k.rec.notices))
	}
	if k.rec.events[0].ActorID != k.landlord {
		t.Error("the user is the actor of their own change")
	}
	mine, err := k.subs.Mine(ctx, k.landlord, authz.RoleLandlord)
	if err != nil || mine.Plan.Code != other.Code || mine.Subscription == nil || mine.Pending != nil {
		t.Fatalf("mine = %+v, %v", mine, err)
	}
}

func TestSubscribeToTheDefaultPlanIsRefused(t *testing.T) {
	k := newKit()
	_, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lFree.ID)
	if !errors.Is(err, domain.ErrAlreadyOnPlan) {
		t.Fatalf("err = %v", err)
	}
	// On a paid plan, asking for it again is refused as well.
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	if _, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID); !errors.Is(err, domain.ErrAlreadyOnPlan) {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeToAPaidPlanWaitsForAnAdmin(t *testing.T) {
	k := newKit()
	got, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusPending || got.EndsAt != nil {
		t.Fatalf("a paid plan is pending: %+v", got)
	}
	mine, _ := k.subs.Mine(ctx, k.landlord, authz.RoleLandlord)
	if mine.Plan.Code != "landlord_free" || mine.Subscription != nil || mine.Pending == nil || mine.Pending.Plan.Code != "landlord_pro" {
		t.Fatalf("the user stays on the default plan while the request waits: %+v", mine)
	}
	if len(k.rec.notices) != 1 || !k.rec.notices[0].ToAdmins || k.rec.notices[0].Kind != NotifySubscriptionRequested ||
		k.rec.notices[0].Params["user"] != "Lan" || k.rec.notices[0].Params["plan"] != "landlord_pro" {
		t.Errorf("admins are told: %+v", k.rec.notices)
	}
	if !slices.Equal(k.rec.actions(), []string{AuditSubscriptionRequested}) {
		t.Errorf("audit %v", k.rec.actions())
	}

	// A second request replaces the first one: still a single pending request.
	if _, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID); err != nil {
		t.Fatal(err)
	}
	if k.store.countStatus(k.landlord, domain.AudienceLandlord, domain.StatusPending) != 1 ||
		k.store.countStatus(k.landlord, domain.AudienceLandlord, domain.StatusCanceled) != 1 {
		t.Error("the older request is canceled")
	}
}

func TestSubscribeRefusesWhatDoesNotFit(t *testing.T) {
	k := newKit()
	inactive := k.store.plan("old", domain.AudienceLandlord, 0)
	inactive.IsActive = false

	cases := map[string]struct {
		user uuid.UUID
		role string
		plan uuid.UUID
		want error
	}{
		"a tenant cannot take a landlord plan":                      {k.tenant, authz.RoleTenant, k.lPro.ID, domain.ErrWrongAudience},
		"a landlord acting as a tenant cannot take a landlord plan": {k.landlord, authz.RoleTenant, k.lPro.ID, domain.ErrWrongAudience},
		"a landlord acting as a landlord cannot take a tenant plan": {k.landlord, authz.RoleLandlord, k.tPl.ID, domain.ErrWrongAudience},
		"admin holds neither":                                       {k.admin, authz.RoleAdmin, k.tPl.ID, domain.ErrWrongAudience},
		"an inactive plan":                                          {k.landlord, authz.RoleLandlord, inactive.ID, domain.ErrPlanInactive},
		"an unknown plan":                                           {k.landlord, authz.RoleLandlord, uuid.New(), domain.ErrPlanUnknown},
		"an unknown account":                                        {uuid.New(), authz.RoleTenant, k.tPl.ID, domain.ErrAccountNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := k.subs.Subscribe(ctx, tc.user, tc.role, tc.plan); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if len(k.store.subs) != 0 {
		t.Error("nothing is stored for a refused request")
	}
}

func TestALandlordHasAPlanOnEachSide(t *testing.T) {
	k := newKit()
	for _, id := range []uuid.UUID{k.lPro.ID} {
		if _, err := k.subs.Assign(ctx, k.admin, k.landlord, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.subs.Assign(ctx, k.admin, k.landlord, k.tPl.ID, nil); err != nil {
		t.Fatal(err)
	}
	if k.store.countStatus(k.landlord, domain.AudienceLandlord, domain.StatusActive) != 1 ||
		k.store.countStatus(k.landlord, domain.AudienceTenant, domain.StatusActive) != 1 {
		t.Fatal("one active subscription on each side")
	}
	asLandlord, _ := k.subs.Mine(ctx, k.landlord, authz.RoleLandlord)
	asTenant, _ := k.subs.Mine(ctx, k.landlord, authz.RoleTenant)
	if asLandlord.Plan.Code != "landlord_pro" || asTenant.Plan.Code != "tenant_plus" {
		t.Errorf("landlord side %s, tenant side %s", asLandlord.Plan.Code, asTenant.Plan.Code)
	}
	if _, ok := asLandlord.Usage["rooms"]; !ok || len(asTenant.Usage) != 1 || asTenant.Usage["pending_registrations"] != 3 {
		t.Errorf("usage by audience: %v / %v", asLandlord.Usage, asTenant.Usage)
	}
}

func TestAssignReplacesTheRunningPlanAndKeepsOneActive(t *testing.T) {
	k := newKit()
	vip := k.store.plan("landlord_vip", domain.AudienceLandlord, 499_000)

	first, err := k.subs.Assign(ctx, k.admin, k.landlord, k.lPro.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := epoch.AddDate(0, 1, 0)
	if first.Status != domain.StatusActive || first.EndsAt == nil || !first.EndsAt.Equal(wantEnd) ||
		first.GrantedByID == nil || *first.GrantedByID != k.admin {
		t.Fatalf("a paid plan lasts a month and remembers who gave it: %+v", first)
	}
	second, err := k.subs.Assign(ctx, k.admin, k.landlord, vip.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.store.countStatus(k.landlord, domain.AudienceLandlord, domain.StatusActive) != 1 || k.store.subs[first.ID].Status != domain.StatusCanceled {
		t.Fatal("the new plan replaces the old one")
	}
	if got, _ := k.limit.For(ctx, k.landlord, domain.AudienceLandlord); got.MaxRooms != nil {
		t.Error("VIP has no room limit")
	}
	_ = second

	// An explicit end wins; one in the past is refused; a free plan given by an admin has no end.
	end := epoch.Add(72 * time.Hour)
	got, err := k.subs.Assign(ctx, k.admin, k.tenant, k.tPl.ID, &end)
	if err != nil || got.EndsAt == nil || !got.EndsAt.Equal(end) {
		t.Fatalf("explicit end: %+v, %v", got, err)
	}
	past := epoch.Add(-time.Minute)
	if _, err := k.subs.Assign(ctx, k.admin, k.tenant, k.tPl.ID, &past); !errors.Is(err, domain.ErrEndInPast) {
		t.Errorf("past end: %v", err)
	}
	free, err := k.subs.Assign(ctx, k.admin, k.tenant, k.tFree.ID, nil)
	if err != nil || free.EndsAt != nil {
		t.Errorf("a free plan has no end: %+v, %v", free, err)
	}
}

func TestAssignRefusesAnAccountOfTheWrongSideAndUnknownThings(t *testing.T) {
	k := newKit()
	if _, err := k.subs.Assign(ctx, k.admin, k.tenant, k.lPro.ID, nil); !errors.Is(err, domain.ErrWrongAudience) {
		t.Errorf("a tenant does not hold the landlord role: %v", err)
	}
	if _, err := k.subs.Assign(ctx, k.admin, uuid.New(), k.lPro.ID, nil); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := k.subs.Assign(ctx, k.admin, k.landlord, uuid.New(), nil); !errors.Is(err, domain.ErrPlanUnknown) {
		t.Errorf("unknown plan: %v", err)
	}
	// An admin may hand out a plan to a landlord whatever role the landlord acts in right now: nobody acts for them.
	if _, err := k.subs.Assign(ctx, k.admin, k.landlord, k.lPro.ID, nil); err != nil {
		t.Errorf("assign to a landlord: %v", err)
	}
}

func TestActivateTurnsAPendingRequestIntoTheRunningPlan(t *testing.T) {
	k := newKit()
	running := k.store.putSub(k.landlord, k.lFree, domain.StatusActive, nil)
	req, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID)
	if err != nil {
		t.Fatal(err)
	}
	k.rec.notices, k.rec.events = nil, nil
	k.store.now = epoch.Add(24 * time.Hour)

	got, err := k.subs.Activate(ctx, k.admin, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != req.ID || got.Status != domain.StatusActive || !got.StartsAt.Equal(k.store.now) ||
		got.EndsAt == nil || !got.EndsAt.Equal(k.store.now.AddDate(0, 1, 0)) || got.GrantedByID == nil || *got.GrantedByID != k.admin {
		t.Fatalf("activated = %+v", got)
	}
	if k.store.subs[running.ID].Status != domain.StatusCanceled {
		t.Error("the plan it replaces ends")
	}
	if k.store.countStatus(k.landlord, domain.AudienceLandlord, domain.StatusActive) != 1 {
		t.Error("one active plan")
	}
	if len(k.rec.notices) != 1 || k.rec.notices[0].UserID != k.landlord || k.rec.notices[0].Kind != NotifySubscriptionActivated ||
		k.rec.notices[0].Audience != "landlord" {
		t.Errorf("the user is told: %+v", k.rec.notices)
	}
	if !slices.Equal(k.rec.actions(), []string{AuditSubscriptionActivated}) {
		t.Errorf("audit %v", k.rec.actions())
	}

	// Only a pending request can be activated; an unknown one is not found.
	if _, err := k.subs.Activate(ctx, k.admin, req.ID); !errors.Is(err, domain.ErrNotPending) {
		t.Errorf("second activation: %v", err)
	}
	if _, err := k.subs.Activate(ctx, k.admin, uuid.New()); !errors.Is(err, domain.ErrSubscriptionNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestANotificationFailureDoesNotUndoTheChange(t *testing.T) {
	k := newKit()
	k.rec.failing = true
	got, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID)
	if err != nil || got.Status != domain.StatusPending {
		t.Fatalf("the request is stored even when the notice fails: %+v, %v", got, err)
	}
}

func TestCancelFallsBackToTheDefaultPlanOfTheSideOnly(t *testing.T) {
	k := newKit()
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	k.store.putSub(k.landlord, k.tPl, domain.StatusActive, nil)
	if _, err := k.subs.Subscribe(ctx, k.landlord, authz.RoleLandlord, k.lPro.ID); err == nil {
		t.Fatal("already on it")
	}

	if err := k.subs.Cancel(ctx, k.landlord, authz.RoleLandlord); err != nil {
		t.Fatal(err)
	}
	asLandlord, _ := k.subs.Mine(ctx, k.landlord, authz.RoleLandlord)
	asTenant, _ := k.subs.Mine(ctx, k.landlord, authz.RoleTenant)
	if asLandlord.Plan.Code != "landlord_free" || asTenant.Plan.Code != "tenant_plus" {
		t.Errorf("only the landlord side fell back: %s / %s", asLandlord.Plan.Code, asTenant.Plan.Code)
	}
	if !slices.Equal(k.rec.actions(), []string{AuditSubscriptionCanceled}) {
		t.Errorf("audit %v", k.rec.actions())
	}
	// Cancelling again changes nothing and records nothing.
	if err := k.subs.Cancel(ctx, k.landlord, authz.RoleLandlord); err != nil || len(k.rec.events) != 1 {
		t.Errorf("second cancel: %v, %d events", err, len(k.rec.events))
	}
}

func TestRevokeEndsEveryPlanOfTheUser(t *testing.T) {
	k := newKit()
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	k.store.putSub(k.landlord, k.tPl, domain.StatusActive, nil)
	k.store.putSub(k.landlord, k.tPl, domain.StatusPending, nil)

	if err := k.subs.Revoke(ctx, k.admin, k.landlord); err != nil {
		t.Fatal(err)
	}
	for _, a := range []domain.Audience{domain.AudienceLandlord, domain.AudienceTenant} {
		if k.store.countStatus(k.landlord, a, domain.StatusActive)+k.store.countStatus(k.landlord, a, domain.StatusPending) != 0 {
			t.Errorf("%s side still open", a)
		}
	}
	if len(k.rec.events) != 1 || k.rec.events[0].Meta["ended"] != 3 || k.rec.events[0].ActorID != k.admin {
		t.Errorf("events = %+v", k.rec.events)
	}
	if err := k.subs.Revoke(ctx, k.admin, uuid.New()); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
}

func TestExpireDueMarksOnlyLapsedSubscriptionsAndIsIdempotent(t *testing.T) {
	k := newKit()
	past, future := epoch.Add(-time.Hour), epoch.Add(time.Hour)
	lapsed := k.store.putSub(k.landlord, k.lPro, domain.StatusActive, &past)
	live := k.store.putSub(k.tenant, k.tPl, domain.StatusActive, &future)
	forever := k.store.putSub(k.admin, k.tFree, domain.StatusActive, nil)
	pending := k.store.putSub(k.landlord, k.tPl, domain.StatusPending, &past)

	got, err := k.subs.ExpireDue(ctx)
	if err != nil || got != 1 {
		t.Fatalf("expired %d, %v", got, err)
	}
	if lapsed.Status != domain.StatusExpired || live.Status != domain.StatusActive || forever.Status != domain.StatusActive || pending.Status != domain.StatusPending {
		t.Error("only the lapsed active subscription expires")
	}
	if len(k.rec.events) != 1 || k.rec.events[0].Action != AuditSubscriptionsExpired || k.rec.events[0].ActorID != uuid.Nil {
		t.Errorf("audit = %+v", k.rec.events)
	}
	if again, err := k.subs.ExpireDue(ctx); err != nil || again != 0 || len(k.rec.events) != 1 {
		t.Errorf("a second run does nothing: %d, %v, %d events", again, err, len(k.rec.events))
	}
}

func TestExpireDueWorksThroughEveryBatch(t *testing.T) {
	k := newKit()
	past := epoch.Add(-time.Hour)
	for range 2*expireBatch + 50 {
		k.store.putSub(uuid.New(), k.tPl, domain.StatusActive, &past)
	}
	got, err := k.subs.ExpireDue(ctx)
	if err != nil || got != 2*expireBatch+50 {
		t.Fatalf("expired %d, %v", got, err)
	}
	if len(k.rec.events) != 3 {
		t.Errorf("one audit event per batch, got %d", len(k.rec.events))
	}
}

func TestSearchFiltersAndPages(t *testing.T) {
	k := newKit()
	k.store.putSub(k.landlord, k.lPro, domain.StatusPending, nil)
	k.store.putSub(k.tenant, k.tPl, domain.StatusPending, nil)
	k.store.putSub(k.tenant, k.tFree, domain.StatusActive, nil)

	pending, aud := domain.StatusPending, domain.AudienceTenant
	got, total, err := k.subs.Search(ctx, domain.SubscriptionQuery{Status: &pending, Limit: 1})
	if err != nil || total != 2 || len(got) != 1 {
		t.Fatalf("page: %d of %d, %v", len(got), total, err)
	}
	got, total, _ = k.subs.Search(ctx, domain.SubscriptionQuery{Status: &pending, Audience: &aud, Limit: 10})
	if total != 1 || got[0].UserName != "Tam" {
		t.Errorf("filtered: %+v", got)
	}
}

func TestLimitsFollowTheEffectivePlan(t *testing.T) {
	k := newKit()
	got, err := k.limit.For(ctx, k.landlord, domain.AudienceLandlord)
	if err != nil || got.MaxRooms == nil || *got.MaxRooms != 2 || *got.MaxBuildings != 1 || *got.MaxImagesPerRoom != 4 {
		t.Fatalf("the default plan applies: %+v, %v", got, err)
	}

	// A running subscription replaces the default, per audience.
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	k.lPro.Limits = domain.Limits{MaxRooms: n(50)}
	if got, _ = k.limit.For(ctx, k.landlord, domain.AudienceLandlord); *got.MaxRooms != 50 {
		t.Errorf("the subscription's plan applies: %+v", got)
	}
	if got, _ = k.limit.For(ctx, k.landlord, domain.AudienceTenant); *got.MaxPendingRegistrations != 3 {
		t.Errorf("the tenant side is separate: %+v", got)
	}
	if p, _ := k.limit.SearchPriority(ctx, k.landlord); p != 10 {
		t.Errorf("search priority = %d", p)
	}
}

func TestSearchPrioritiesOfSeveralLandlords(t *testing.T) {
	k := newKit()
	k.lFree.SearchPriority = 1
	other := k.store.account("Other", authz.RoleLandlord)
	past := epoch.Add(-time.Minute)
	lapsed := k.store.account("Lapsed", authz.RoleLandlord)
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	k.store.putSub(lapsed, k.lPro, domain.StatusActive, &past)

	got, err := k.limit.SearchPriorities(ctx, []uuid.UUID{k.landlord, other, lapsed})
	if err != nil || got[k.landlord] != 10 || got[other] != 1 || got[lapsed] != 1 || len(got) != 3 {
		t.Errorf("a running plan ranks by its priority, everybody else by the default's: %v, %v", got, err)
	}
	if got, err := k.limit.SearchPriorities(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("nobody asked: %v, %v", got, err)
	}
	empty := newWorld() // no plan at all: every priority is 0
	user := empty.store.account("U", authz.RoleLandlord)
	if got, err := empty.limit.SearchPriorities(ctx, []uuid.UUID{user}); err != nil || got[user] != 0 {
		t.Errorf("no plan: %v, %v", got, err)
	}
}

func TestLimitsIgnoreLapsedAndPendingSubscriptions(t *testing.T) {
	k := newKit()
	past := epoch.Add(-time.Minute)
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, &past)
	k.lPro.Limits = domain.Limits{MaxRooms: n(50)}
	k.store.putSub(k.tenant, k.tPl, domain.StatusPending, nil)

	if got, _ := k.limit.For(ctx, k.landlord, domain.AudienceLandlord); *got.MaxRooms != 2 {
		t.Errorf("a lapsed subscription falls back to the default: %+v", got)
	}
	if got, _ := k.limit.For(ctx, k.tenant, domain.AudienceTenant); *got.MaxPendingRegistrations != 3 {
		t.Errorf("a pending request is not a plan: %+v", got)
	}
}

func TestLimitsFailOpenWhenThereIsNoPlan(t *testing.T) {
	w := newWorld() // an empty catalog
	user := w.store.account("U", authz.RoleLandlord)
	got, err := w.limit.For(ctx, user, domain.AudienceLandlord)
	if err != nil || got != (domain.Limits{}) {
		t.Fatalf("no plan means no limit: %+v, %v", got, err)
	}
	if p, err := w.limit.SearchPriority(ctx, user); err != nil || p != 0 {
		t.Errorf("priority %d, %v", p, err)
	}
	// An inactive default plan is no default either.
	d := w.store.plan("free", domain.AudienceLandlord, 0)
	d.IsDefault, d.IsActive, d.Limits = true, false, domain.Limits{MaxRooms: n(1)}
	if got, _ := w.limit.For(ctx, user, domain.AudienceLandlord); got.MaxRooms != nil {
		t.Errorf("an inactive default is ignored: %+v", got)
	}
	// But a subscription to a plan that was switched off keeps working.
	off := w.store.plan("old", domain.AudienceLandlord, 100)
	off.IsActive, off.Limits = false, domain.Limits{MaxRooms: n(9)}
	w.store.putSub(user, off, domain.StatusActive, nil)
	if got, _ := w.limit.For(ctx, user, domain.AudienceLandlord); *got.MaxRooms != 9 {
		t.Errorf("the subscription to an inactive plan still applies: %+v", got)
	}
}

func TestLimitsPassTheStoreError(t *testing.T) {
	k := newKit()
	boom := errors.New("db down")
	k.limit.subs = failingSubs{boom}
	if _, err := k.limit.For(ctx, k.landlord, domain.AudienceLandlord); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := k.limit.SearchPriorities(ctx, []uuid.UUID{k.landlord}); !errors.Is(err, boom) {
		t.Errorf("priorities err = %v", err)
	}
}

type failingSubs struct{ err error }

func (f failingSubs) Running(context.Context, uuid.UUID, domain.Audience, time.Time) (domain.Detail, error) {
	return domain.Detail{}, f.err
}
func (f failingSubs) RunningPriorities(context.Context, []uuid.UUID, domain.Audience, time.Time) (map[uuid.UUID]int, error) {
	return nil, f.err
}
func (failingSubs) Insert(context.Context, domain.Subscription) (uuid.UUID, error) {
	return uuid.Nil, shared.ErrNotFound
}
func (failingSubs) Get(context.Context, uuid.UUID) (domain.Detail, error) {
	return domain.Detail{}, nil
}
func (failingSubs) Lock(context.Context, uuid.UUID) (domain.Subscription, error) {
	return domain.Subscription{}, nil
}
func (failingSubs) Pending(context.Context, uuid.UUID, domain.Audience) (domain.Detail, error) {
	return domain.Detail{}, nil
}
func (failingSubs) CancelPending(context.Context, uuid.UUID, domain.Audience, time.Time) error {
	return nil
}
func (failingSubs) EndOpen(context.Context, uuid.UUID, *domain.Audience, uuid.UUID, time.Time) (int, error) {
	return 0, nil
}
func (failingSubs) Activate(context.Context, uuid.UUID, time.Time, *time.Time, uuid.UUID) error {
	return nil
}
func (failingSubs) Search(context.Context, domain.SubscriptionQuery) ([]domain.Detail, int, error) {
	return nil, 0, nil
}
func (failingSubs) ExpireDue(context.Context, time.Time, int) ([]uuid.UUID, error) { return nil, nil }

// The catalog.

func TestCreateAPlanMakesItTheSoleDefaultOfItsAudience(t *testing.T) {
	k := newKit()
	got, err := k.plans.Create(ctx, k.admin, PlanInput{
		Code: "new_free", Name: "New free", Audience: domain.AudienceLandlord, Interval: domain.IntervalMonth, IsActive: true,
		IsDefault: true, Limits: domain.Limits{MaxRooms: n(3)}, Features: []domain.Feature{domain.FeatureVIPBadge},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsDefault || got.Code != "new_free" || got.Limits.MaxRooms == nil || *got.Limits.MaxRooms != 3 {
		t.Fatalf("created = %+v", got)
	}
	if k.lFree.IsDefault || !k.tFree.IsDefault {
		t.Error("only the other default of the same audience loses the flag")
	}
	if !slices.Equal(k.rec.actions(), []string{AuditPlanSaved}) || k.rec.events[0].Meta["created"] != true {
		t.Errorf("audit = %+v", k.rec.events)
	}
}

func TestCreateAPlanChecksTheCode(t *testing.T) {
	k := newKit()
	in := PlanInput{Code: "landlord_pro", Name: "Dup", Audience: domain.AudienceLandlord, Interval: domain.IntervalMonth, IsActive: true}
	if _, err := k.plans.Create(ctx, k.admin, in); !errors.Is(err, domain.ErrCodeTaken) {
		t.Errorf("duplicate: %v", err)
	}
	in.Code = "Bad Code"
	if _, err := k.plans.Create(ctx, k.admin, in); !errors.Is(err, domain.ErrInvalidCode) {
		t.Errorf("invalid: %v", err)
	}
}

func TestUpdateAPlanIsPartialAndNullMeansUnlimited(t *testing.T) {
	k := newKit()
	name := "Starter"
	off := false
	got, err := k.plans.Update(ctx, k.admin, k.lFree.ID, PlanPatch{
		Name: &name, IsActive: &off, MaxRooms: Optional[int]{Set: true, Value: nil}, MaxBuildings: Optional[int]{Set: true, Value: n(7)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Starter" || got.IsActive || got.Limits.MaxRooms != nil || got.Limits.MaxBuildings == nil || *got.Limits.MaxBuildings != 7 {
		t.Fatalf("patched = %+v", got)
	}
	if got.Limits.MaxImagesPerRoom == nil || *got.Limits.MaxImagesPerRoom != 4 || got.Code != "landlord_free" || !got.IsDefault {
		t.Errorf("what the patch does not carry stays: %+v", got)
	}
	if k.rec.events[0].Meta["created"] != false {
		t.Errorf("audit = %+v", k.rec.events)
	}
}

func TestUpdateADefaultClearsTheOthersAndChecksTheCode(t *testing.T) {
	k := newKit()
	yes := true
	if _, err := k.plans.Update(ctx, k.admin, k.lPro.ID, PlanPatch{IsDefault: &yes}); err != nil {
		t.Fatal(err)
	}
	if k.lFree.IsDefault || !k.lPro.IsDefault || !k.tFree.IsDefault {
		t.Error("the default moves within its audience only")
	}
	taken, bad := "tenant_plus", "No Good"
	if _, err := k.plans.Update(ctx, k.admin, k.tFree.ID, PlanPatch{Code: &taken}); !errors.Is(err, domain.ErrCodeTaken) {
		t.Errorf("taken: %v", err)
	}
	if _, err := k.plans.Update(ctx, k.admin, k.tFree.ID, PlanPatch{Code: &bad}); !errors.Is(err, domain.ErrInvalidCode) {
		t.Errorf("invalid: %v", err)
	}
	if _, err := k.plans.Update(ctx, k.admin, uuid.New(), PlanPatch{}); !errors.Is(err, domain.ErrPlanNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestTheCatalogListsActivePlansOfAnAudience(t *testing.T) {
	k := newKit()
	k.store.plan("old", domain.AudienceLandlord, 0).IsActive = false

	got, total, err := k.plans.Available(ctx, domain.AudienceLandlord, 10, 0)
	if err != nil || total != 2 || !slices.Equal(sortedCodes(got), []string{"landlord_free", "landlord_pro"}) {
		t.Errorf("available = %v of %d, %v", sortedCodes(got), total, err)
	}
	all, total, _ := k.plans.List(ctx, 100, 0)
	if total != 5 || len(all) != 5 {
		t.Errorf("the whole catalog has the inactive plan too: %d", total)
	}
}

// The seeds.

func TestSeedPlansIsSafeToRepeatAndKeepsAnAdminsDefault(t *testing.T) {
	w := newWorld()
	for range 2 {
		if err := w.seed.SeedPlans(ctx); err != nil {
			t.Fatal(err)
		}
	}
	all, total, _ := w.plans.List(ctx, 100, 0)
	if total != 5 || len(all) != 5 {
		t.Fatalf("5 plans after two runs, got %d", total)
	}
	d, err := w.store.Default(ctx, domain.AudienceLandlord)
	if err != nil || d.Code != "landlord_free" {
		t.Fatalf("default = %+v, %v", d, err)
	}

	// An admin edits a seeded plan: the seed writes the base values again but does not add a second default.
	pro, _ := w.store.GetByCode(ctx, "landlord_pro")
	yes, name := true, "Pro (renamed)"
	if _, err := w.plans.Update(ctx, uuid.Nil, pro.ID, PlanPatch{IsDefault: &yes, Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err := w.seed.SeedPlans(ctx); err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, p := range w.store.plans {
		if p.Audience == domain.AudienceLandlord && p.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("one default per audience, got %d", defaults)
	}
	if got, _ := w.store.GetByCode(ctx, "landlord_pro"); got.Name != "Pro" {
		t.Errorf("the seeded plan is written again: %q", got.Name)
	}
}

func TestSeedDemoSubscriptionsPutsTheDemoAccountsOnPlans(t *testing.T) {
	w := newWorld()
	landlord := w.store.account("L", authz.RoleLandlord, authz.RoleTenant)
	tenant := w.store.account("T", authz.RoleTenant)
	staff := w.store.account("S", authz.RoleAdmin)
	if err := w.seed.SeedPlans(ctx); err != nil {
		t.Fatal(err)
	}
	by := map[string]uuid.UUID{"landlord@rental.test": landlord, "tenant@rental.test": tenant}

	done, err := w.seed.SeedDemoSubscriptions(ctx, by)
	if err != nil || done != 2 {
		t.Fatalf("done %d, %v", done, err)
	}
	asLandlord, _ := w.subs.Mine(ctx, landlord, authz.RoleLandlord)
	asTenant, _ := w.subs.Mine(ctx, tenant, authz.RoleTenant)
	if asLandlord.Plan.Code != "landlord_vip" || asTenant.Plan.Code != "tenant_plus" {
		t.Errorf("plans: %s / %s", asLandlord.Plan.Code, asTenant.Plan.Code)
	}
	if again, err := w.seed.SeedDemoSubscriptions(ctx, by); err != nil || again != 0 {
		t.Errorf("a second run changes nothing: %d, %v", again, err)
	}
	if len(w.store.subs) != 2 {
		t.Errorf("%d subscriptions", len(w.store.subs))
	}
	// An account of the wrong side is an error, not a silent skip.
	if _, err := w.seed.SeedDemoSubscriptions(ctx, map[string]uuid.UUID{"landlord@rental.test": staff}); !errors.Is(err, domain.ErrWrongAudience) {
		t.Errorf("err = %v", err)
	}
}

func TestMineUsageForAMissingPlanStillWorks(t *testing.T) {
	w := newWorld()
	user := w.store.account("U", authz.RoleTenant)
	mine, err := w.subs.Mine(ctx, user, authz.RoleTenant)
	if err != nil || mine.Plan != nil || mine.Subscription != nil || mine.Pending != nil {
		t.Fatalf("no catalog, no plan: %+v, %v", mine, err)
	}
}

func TestAPlanGivenByTheSystemRemembersNoAdmin(t *testing.T) {
	k := newKit()
	got, err := k.subs.Assign(ctx, uuid.Nil, k.landlord, k.lPro.ID, nil)
	if err != nil || got.GrantedByID != nil {
		t.Fatalf("a nil actor grants nothing: %+v, %v", got, err)
	}
}

func TestHasFeatureFollowsTheEffectivePlanPerAudience(t *testing.T) {
	k := newKit()
	has := func(user uuid.UUID, a domain.Audience) bool {
		t.Helper()
		on, err := k.limit.HasFeature(ctx, user, a, domain.FeatureAutoInvoicing)
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
	if has(k.landlord, domain.AudienceLandlord) {
		t.Error("the free plan has no automatic invoicing")
	}

	k.lPro.Features = []domain.Feature{domain.FeatureAutoInvoicing}
	k.store.putSub(k.landlord, k.lPro, domain.StatusActive, nil)
	if !has(k.landlord, domain.AudienceLandlord) {
		t.Error("the running plan switches the feature on")
	}
	if has(k.landlord, domain.AudienceTenant) {
		t.Error("the tenant side has its own plan")
	}

	// No plan at all switches nothing on (unlike a limit, a feature is something a plan gives).
	empty := newWorld()
	user := empty.store.account("U", authz.RoleLandlord)
	if on, err := empty.limit.HasFeature(ctx, user, domain.AudienceLandlord, domain.FeatureAutoInvoicing); err != nil || on {
		t.Errorf("no plan: %v, %v", on, err)
	}
}
