//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/testenv"
	plandomain "rental-management/internal/domain/plan"
	planuc "rental-management/internal/usecase/plan"
)

// The plan limits are tested through the real composition code: a plan stored in the database must limit what the
// property and contract APIs let a landlord or a tenant do, and a bigger plan must lift the limit again.

func ptr[T any](v T) *T { return &v }

// seedPlans writes the base catalog and returns the id of a plan by code.
func (e *bridgeEnv) seedPlans() func(code string) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	if err := e.plans.seeder.SeedPlans(ctx); err != nil {
		e.t.Fatal(err)
	}
	all, _, err := e.plans.catalog.List(ctx, 100, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return func(code string) uuid.UUID {
		for _, p := range all {
			if p.Code == code {
				return p.ID
			}
		}
		e.t.Fatalf("no plan %s", code)
		return uuid.Nil
	}
}

// limit changes what a plan allows.
func (e *bridgeEnv) limit(id uuid.UUID, patch planuc.PlanPatch) {
	e.t.Helper()
	if _, err := e.plans.catalog.Update(context.Background(), uuid.Nil, id, patch); err != nil {
		e.t.Fatal(err)
	}
}

func (e *bridgeEnv) assign(user, plan uuid.UUID) {
	e.t.Helper()
	if _, err := e.plans.subscriptions.Assign(context.Background(), uuid.Nil, user, plan, nil); err != nil {
		e.t.Fatal(err)
	}
}

func opt(v *int) planuc.Optional[int] { return planuc.Optional[int]{Set: true, Value: v} }

func TestTheRoomLimitOfAPlanStopsTheSecondRoomUntilABiggerPlanIsAssigned(t *testing.T) {
	e := newBridgeEnv(t)
	plan := e.seedPlans()
	e.limit(plan("landlord_free"), planuc.PlanPatch{MaxRooms: opt(ptr(1))})

	e.newRoom("R1", "available")
	w := e.call(http.MethodPost, "/v1/rooms", map[string]any{"name": "R2", "address_detail": "x", "price_vnd": 2_000_000}, "landlord")
	body := testenv.ExpectError(t, w, http.StatusConflict, apperr.CodeRoomLimit)
	if !strings.Contains(body.Error.Message, "1") {
		t.Errorf("the message names the limit: %q", body.Error.Message)
	}

	// A bigger plan lifts it; so does one with no room limit at all (VIP).
	e.assign(e.landlord, plan("landlord_pro"))
	e.newRoom("R2", "available")
	e.assign(e.landlord, plan("landlord_vip"))
	e.newRoom("R3", "available")

	// And the limit comes back when the subscription is cancelled: three rooms already exist, the default allows one.
	if err := e.plans.subscriptions.Cancel(context.Background(), e.landlord, "landlord"); err != nil {
		t.Fatal(err)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/rooms", map[string]any{"name": "R4", "address_detail": "x", "price_vnd": 2_000_000}, "landlord"), http.StatusConflict, apperr.CodeRoomLimit)
}

func TestTheBuildingLimitOfAPlanStopsTheSecondBuilding(t *testing.T) {
	e := newBridgeEnv(t)
	plan := e.seedPlans()
	e.limit(plan("landlord_free"), planuc.PlanPatch{MaxBuildings: opt(ptr(1))})
	newBuilding := func(name string) *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/buildings", map[string]any{"name": name, "ward_code": 4, "address_detail": "x", "price_vnd": 2_000_000}, "landlord")
	}

	testenv.ExpectStatus(t, newBuilding("B1"), http.StatusCreated)
	testenv.ExpectError(t, newBuilding("B2"), http.StatusConflict, apperr.CodeBuildingLimit)
	e.assign(e.landlord, plan("landlord_pro"))
	testenv.ExpectStatus(t, newBuilding("B2"), http.StatusCreated)
}

func TestTheContractLimitsOfAPlanReachTheContractApi(t *testing.T) {
	e := newBridgeEnv(t)
	plan := e.seedPlans()
	e.limit(plan("landlord_free"), planuc.PlanPatch{MaxActiveContracts: opt(ptr(1))})
	e.limit(plan("tenant_free"), planuc.PlanPatch{MaxPendingRegistrations: opt(ptr(1))})

	today := time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")
	r1, r2 := e.newRoom("R1", "available"), e.newRoom("R2", "available")
	e.publish("landlord", r1.ID)
	e.publish("landlord", r2.ID)
	register := func(room room) *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": today}, "tenant")
	}

	// The tenant may have one registration waiting; a plan with more room lifts it.
	first := register(r1)
	testenv.ExpectStatus(t, first, http.StatusCreated)
	testenv.ExpectError(t, register(r2), http.StatusConflict, apperr.CodeRegistrationLimit)
	e.assign(e.tenant, plan("tenant_plus"))
	second := register(r2)
	testenv.ExpectStatus(t, second, http.StatusCreated)

	// The landlord may run one contract; the second approval is refused until a bigger plan is assigned.
	approve := func(c contractRow) *httptest.ResponseRecorder {
		return e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord")
	}
	c1, c2 := testenv.DecodeData[contractRow](t, first), testenv.DecodeData[contractRow](t, second)
	testenv.ExpectStatus(t, approve(c1), http.StatusOK)
	testenv.ExpectError(t, approve(c2), http.StatusConflict, apperr.CodeContractLimit)
	e.assign(e.landlord, plan("landlord_pro"))
	testenv.ExpectStatus(t, approve(c2), http.StatusOK)
}

func TestEveryLimitPortReadsTheRightFieldOfTheRightSide(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()

	// With no plan at all (nothing seeded) nothing is limited.
	limits := planLimits{limits: e.plans.limits}
	for name, got := range map[string]func() (*int, error){
		"rooms":     func() (*int, error) { return limits.MaxRooms(ctx, e.landlord) },
		"buildings": func() (*int, error) { return limits.MaxBuildings(ctx, e.landlord) },
		"media":     func() (*int, error) { return limits.MaxMediaPerOwner(ctx, e.landlord) },
		"contracts": func() (*int, error) { return limits.MaxActiveContracts(ctx, e.landlord) },
		"pending":   func() (*int, error) { return limits.MaxPendingRegistrations(ctx, e.tenant) },
	} {
		if v, err := got(); err != nil || v != nil {
			t.Errorf("%s without a plan: %v, %v", name, v, err)
		}
	}

	plan := e.seedPlans()
	e.limit(plan("landlord_pro"), planuc.PlanPatch{
		MaxRooms: opt(ptr(11)), MaxBuildings: opt(ptr(12)), MaxImagesPerRoom: opt(ptr(13)), MaxActiveContracts: opt(ptr(14)), SearchPriority: ptr(15),
	})
	e.limit(plan("tenant_plus"), planuc.PlanPatch{MaxPendingRegistrations: opt(ptr(16))})
	e.assign(e.landlord, plan("landlord_pro"))
	e.assign(e.landlord, plan("tenant_plus"))

	for name, tc := range map[string]struct {
		got  func() (*int, error)
		want int
	}{
		"rooms":     {func() (*int, error) { return limits.MaxRooms(ctx, e.landlord) }, 11},
		"buildings": {func() (*int, error) { return limits.MaxBuildings(ctx, e.landlord) }, 12},
		"media":     {func() (*int, error) { return limits.MaxMediaPerOwner(ctx, e.landlord) }, 13},
		"contracts": {func() (*int, error) { return limits.MaxActiveContracts(ctx, e.landlord) }, 14},
		// The same account, on its tenant side, has its own plan.
		"pending": {func() (*int, error) { return limits.MaxPendingRegistrations(ctx, e.landlord) }, 16},
	} {
		if v, err := tc.got(); err != nil || v == nil || *v != tc.want {
			t.Errorf("%s = %v, %v; want %d", name, v, err, tc.want)
		}
	}
	if p, err := e.plans.limits.SearchPriority(ctx, e.landlord); err != nil || p != 15 {
		t.Errorf("search priority = %d, %v", p, err)
	}
	// The search asks for the priorities of many landlords at once: the subscriber's plan and the default's (0).
	ranks, err := searchPriority{limits: e.plans.limits}.SearchPriorities(ctx, []uuid.UUID{e.landlord, e.landlord2})
	if err != nil || ranks[e.landlord] != 15 || ranks[e.landlord2] != 0 || len(ranks) != 2 {
		t.Errorf("search priorities = %v, %v", ranks, err)
	}
	if got, _ := e.plans.limits.For(ctx, e.tenant, plandomain.AudienceTenant); got.MaxPendingRegistrations == nil || *got.MaxPendingRegistrations != 3 {
		t.Errorf("a tenant without a subscription is on the default plan: %+v", got)
	}
}

func TestSeedDemoSubscriptionsPutsTheDemoAccountsOnPaidPlans(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	e.seedPlans()
	// Safe to run twice, and an account that is not in the data is skipped.
	for i := 0; i < 2; i++ {
		done, err := e.plans.seeder.SeedDemoSubscriptions(ctx, map[string]uuid.UUID{"landlord@rental.test": e.landlord, "tenant@rental.test": e.tenant})
		if err != nil || (i == 0 && done != 2) || (i == 1 && done != 0) {
			t.Fatalf("run %d: %d, %v", i, done, err)
		}
	}
	mine, err := e.plans.subscriptions.Mine(ctx, e.landlord, "landlord")
	if err != nil || mine.Plan.Code != "landlord_vip" || mine.Subscription == nil || mine.Subscription.EndsAt == nil {
		t.Fatalf("landlord = %+v, %v", mine, err)
	}
	if mine, _ := e.plans.subscriptions.Mine(ctx, e.tenant, "tenant"); mine.Plan.Code != "tenant_plus" {
		t.Errorf("tenant = %+v", mine.Plan)
	}
}
