//go:build integration

package planhttp_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
)

func codesOf(ps []PlanView) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func TestTheCatalogShowsThePlansOfTheSideTheCallerActsOn(t *testing.T) {
	e := newPlanEnv(t)

	w := e.call(http.MethodGet, "/v1/plans", nil, "landlord")
	got := listOf[PlanView](t, w)
	if !slices.Equal(codesOf(got.Data), []string{"landlord_free", "landlord_pro", "landlord_vip"}) || got.Meta.Total != 3 || got.Meta.Page != 1 {
		t.Fatalf("landlord catalog = %v, meta %+v", codesOf(got.Data), got.Meta)
	}
	pro := got.Data[1]
	if pro.PriceVND != 199_000 || pro.BillingInterval != "month" || pro.Audience != "landlord" || pro.IsDefault || !pro.IsActive ||
		*pro.Limits.MaxRooms != 100 || *pro.Limits.MaxBuildings != 20 || pro.Limits.MaxPendingRegistrations != nil ||
		!slices.Equal(pro.Features, []string{"auto_invoicing"}) {
		t.Errorf("pro = %+v", pro)
	}
	if vip := got.Data[2]; vip.Limits.MaxRooms != nil || vip.Limits.MaxActiveContracts != nil || *vip.Limits.MaxImagesPerRoom != 20 || len(vip.Features) != 3 {
		t.Errorf("vip has no room or contract limit: %+v", vip)
	}

	// The same landlord acting as a tenant sees the tenant plans; `audience` overrides the side.
	tenant := listOf[PlanView](t, e.call(http.MethodGet, "/v1/plans", nil, "landlord-as-tenant"))
	if !slices.Equal(codesOf(tenant.Data), []string{"tenant_free", "tenant_plus"}) {
		t.Errorf("tenant catalog = %v", codesOf(tenant.Data))
	}
	both := listOf[PlanView](t, e.call(http.MethodGet, "/v1/plans?audience=landlord", nil, "tenant"))
	if len(both.Data) != 3 {
		t.Errorf("audience override = %v", codesOf(both.Data))
	}

	// An inactive plan is not on offer; pages come with their meta.
	e.call(http.MethodPut, "/v1/admin/plans/"+e.planID("landlord_pro").String(), map[string]any{"is_active": false}, "super")
	got = listOf[PlanView](t, e.call(http.MethodGet, "/v1/plans?per_page=1&page=2", nil, "landlord"))
	if got.Meta.Total != 2 || got.Meta.PerPage != 1 || got.Meta.Page != 2 || codesOf(got.Data)[0] != "landlord_vip" {
		t.Errorf("page 2 of the active plans: %v %+v", codesOf(got.Data), got.Meta)
	}

	body := expectError(t, e.call(http.MethodGet, "/v1/plans?audience=admin", nil, "tenant"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["audience"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	expectError(t, e.call(http.MethodGet, "/v1/plans?per_page=0", nil, "tenant"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodGet, "/v1/plans", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
}

func TestMyPlanShowsTheDefaultLimitsAndUsage(t *testing.T) {
	e := newPlanEnv(t)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order)
		VALUES ($1, 'A1', 4, 'x', 1000000, 1), ($1, 'A2', 4, 'x', 1000000, 2)`, e.landlord); err != nil {
		t.Fatal(err)
	}

	m := e.mine("landlord")
	if m.Plan == nil || m.Plan.Code != "landlord_free" || *m.Plan.Limits.MaxRooms != 20 || m.Subscription != nil || m.PendingRequest != nil {
		t.Fatalf("mine = %+v", m)
	}
	if m.Usage["rooms"] != 2 || m.Usage["buildings"] != 0 || m.Usage["active_contracts"] != 0 || len(m.Usage) != 3 {
		t.Errorf("landlord usage = %v", m.Usage)
	}
	asTenant := e.mine("landlord-as-tenant")
	if asTenant.Plan.Code != "tenant_free" || len(asTenant.Usage) != 1 || asTenant.Usage["pending_registrations"] != 0 {
		t.Errorf("the same account as a tenant = %+v", asTenant)
	}
	if tenant := e.mine("tenant"); *tenant.Plan.Limits.MaxPendingRegistrations != 3 {
		t.Errorf("tenant plan = %+v", tenant.Plan)
	}
	expectError(t, e.call(http.MethodGet, "/v1/my/subscription", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
}

func TestAFreePlanSwitchesAtOnceAndAPaidOneWaitsForAnAdmin(t *testing.T) {
	e := newPlanEnv(t)
	// A second free landlord plan, so there is a free plan that is not the default.
	w := e.call(http.MethodPost, "/v1/admin/plans", map[string]any{"code": "landlord_lite", "name": "Lite", "audience": "landlord", "price_vnd": 0, "max_rooms": 5}, "super")
	expectStatus(t, w, http.StatusCreated)
	lite := decodeData[PlanView](t, w)

	w = e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": lite.ID}, "landlord")
	expectStatus(t, w, http.StatusCreated)
	m := decodeData[MineView](t, w)
	if m.Plan.Code != "landlord_lite" || m.Subscription == nil || m.Subscription.Status != "active" || m.Subscription.EndsAt != nil || m.PendingRequest != nil {
		t.Fatalf("a free plan is taken at once: %+v", m)
	}

	pro := e.planID("landlord_pro")
	w = e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": pro}, "landlord")
	expectStatus(t, w, http.StatusCreated)
	m = decodeData[MineView](t, w)
	if m.Plan.Code != "landlord_lite" || m.PendingRequest == nil || m.PendingRequest.Plan.Code != "landlord_pro" || m.PendingRequest.Status != "pending" {
		t.Fatalf("a paid plan is a request: %+v", m)
	}
	if len(e.notices.sent) != 1 || !e.notices.sent[0].ToAdmins || e.notices.sent[0].Kind != "subscription_requested" || e.notices.sent[0].Params["user"] != "Landlord" {
		t.Errorf("the admins are told: %+v", e.notices.sent)
	}
	// Asking again replaces the request: still one waiting.
	expectStatus(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": pro}, "landlord"), http.StatusCreated)
	if n := e.count(`SELECT count(*) FROM subscriptions WHERE status = 'pending'`); n != 1 {
		t.Errorf("pending requests = %d", n)
	}

	// Ending the plan puts the landlord back on the default plan and drops the request.
	expectStatus(t, e.call(http.MethodDelete, "/v1/my/subscription", nil, "landlord"), http.StatusNoContent)
	if m := e.mine("landlord"); m.Plan.Code != "landlord_free" || m.Subscription != nil || m.PendingRequest != nil {
		t.Errorf("after cancelling: %+v", m)
	}
	expectStatus(t, e.call(http.MethodDelete, "/v1/my/subscription", nil, "landlord"), http.StatusNoContent)
}

func TestSubscribingIsRefusedWhenThePlanDoesNotFit(t *testing.T) {
	e := newPlanEnv(t)
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": e.planID("landlord_free")}, "landlord"), http.StatusConflict, apperr.CodePlanAlreadyOn)
	// A tenant does not hold the landlord role; a landlord acting as a tenant cannot take a landlord plan either.
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": e.planID("landlord_pro")}, "tenant"), http.StatusConflict, apperr.CodePlanWrongAudience)
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": e.planID("landlord_pro")}, "landlord-as-tenant"), http.StatusConflict, apperr.CodePlanWrongAudience)
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": e.planID("tenant_plus")}, "admin"), http.StatusConflict, apperr.CodePlanWrongAudience)

	e.call(http.MethodPut, "/v1/admin/plans/"+e.planID("tenant_plus").String(), map[string]any{"is_active": false}, "super")
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": e.planID("tenant_plus")}, "tenant"), http.StatusConflict, apperr.CodePlanInactive)

	body := expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": uuid.New()}, "tenant"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["plan_id"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	body = expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{}, "tenant"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["plan_id"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	expectError(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": "nope"}, "tenant"), http.StatusBadRequest, apperr.CodeRequestMalformed)
	if n := e.count(`SELECT count(*) FROM subscriptions`); n != 0 {
		t.Errorf("a refused request stores nothing, got %d rows", n)
	}
}

func TestAnAdminListsActivatesAssignsAndRevokes(t *testing.T) {
	e := newPlanEnv(t)
	pro, plus := e.planID("landlord_pro"), e.planID("tenant_plus")
	expectStatus(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": pro}, "landlord"), http.StatusCreated)
	expectStatus(t, e.call(http.MethodPost, "/v1/my/subscription", map[string]any{"plan_id": plus}, "tenant"), http.StatusCreated)

	// The waiting requests are the default list, newest first, with their owners.
	got := listOf[SubscriptionView](t, e.call(http.MethodGet, "/v1/admin/subscriptions", nil, "admin"))
	if got.Meta.Total != 2 || len(got.Data) != 2 || got.Data[0].Plan.Code != "tenant_plus" || got.Data[0].User == nil || got.Data[0].User.FullName != "Tenant" || got.Data[1].User.ID != e.landlord {
		t.Fatalf("pending = %+v", got)
	}
	page := listOf[SubscriptionView](t, e.call(http.MethodGet, "/v1/admin/subscriptions?per_page=1&page=2&audience=landlord", nil, "admin"))
	if page.Meta.Total != 1 || len(page.Data) != 0 {
		t.Errorf("filtered second page = %+v", page)
	}
	if byUser := listOf[SubscriptionView](t, e.call(http.MethodGet, "/v1/admin/subscriptions?user_id="+e.tenant.String(), nil, "admin")); byUser.Meta.Total != 1 {
		t.Errorf("by user = %+v", byUser.Meta)
	}
	for _, q := range []string{"?status=zombie", "?audience=staff", "?user_id=nope", "?page=0"} {
		expectError(t, e.call(http.MethodGet, "/v1/admin/subscriptions"+q, nil, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}

	// Activate: the request becomes the running plan, for a month, granted by the admin.
	id := got.Data[1].ID
	w := e.call(http.MethodPost, "/v1/admin/subscriptions/"+id.String()+"/activate", nil, "admin")
	expectStatus(t, w, http.StatusOK)
	active := decodeData[SubscriptionView](t, w)
	if active.Status != "active" || active.ID != id || active.GrantedByID == nil || *active.GrantedByID != e.admin || active.EndsAt == nil ||
		!active.EndsAt.After(time.Now().AddDate(0, 0, 27)) || active.Plan.Code != "landlord_pro" {
		t.Fatalf("activated = %+v", active)
	}
	if m := e.mine("landlord"); m.Plan.Code != "landlord_pro" || m.Subscription == nil || m.PendingRequest != nil {
		t.Errorf("the landlord is on pro: %+v", m)
	}
	if len(e.notices.sent) != 3 || e.notices.sent[2].Kind != "subscription_activated" || e.notices.sent[2].UserID != e.landlord {
		t.Errorf("notices = %+v", e.notices.sent)
	}
	expectError(t, e.call(http.MethodPost, "/v1/admin/subscriptions/"+id.String()+"/activate", nil, "admin"), http.StatusConflict, apperr.CodePlanNotPending)
	expectError(t, e.call(http.MethodPost, "/v1/admin/subscriptions/"+uuid.NewString()+"/activate", nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodPost, "/v1/admin/subscriptions/nope/activate", nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)

	// Assign: right now, replacing what the user had; an explicit end wins.
	vip := e.planID("landlord_vip")
	end := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	w = e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": vip, "ends_at": end.Format(time.RFC3339)}, "super")
	expectStatus(t, w, http.StatusCreated)
	assigned := decodeData[SubscriptionView](t, w)
	if assigned.Status != "active" || assigned.Plan.Code != "landlord_vip" || assigned.EndsAt == nil || !assigned.EndsAt.Equal(end) || assigned.GrantedByID == nil || *assigned.GrantedByID != e.super {
		t.Fatalf("assigned = %+v", assigned)
	}
	if n := e.count(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND status = 'active' AND audience = 'landlord'`, e.landlord); n != 1 {
		t.Errorf("one active landlord plan, got %d", n)
	}

	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	body := expectError(t, e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": vip, "ends_at": past}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["ends_at"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	expectError(t, e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": vip, "ends_at": "soon"}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodPut, "/v1/admin/users/"+e.tenant.String()+"/subscription", map[string]any{"plan_id": vip}, "admin"), http.StatusConflict, apperr.CodePlanWrongAudience)
	expectError(t, e.call(http.MethodPut, "/v1/admin/users/"+uuid.NewString()+"/subscription", map[string]any{"plan_id": vip}, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": uuid.New()}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	// Revoke ends every plan and request of the user.
	expectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+e.landlord.String()+"/subscription", nil, "admin"), http.StatusNoContent)
	if m := e.mine("landlord"); m.Plan.Code != "landlord_free" || m.Subscription != nil {
		t.Errorf("after revoking: %+v", m)
	}
	expectError(t, e.call(http.MethodDelete, "/v1/admin/users/"+uuid.NewString()+"/subscription", nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestPlanAdministrationIsForAdminsOnly(t *testing.T) {
	e := newPlanEnv(t)
	plan := e.planID("landlord_pro").String()
	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/subscriptions"},
		{http.MethodPost, "/v1/admin/subscriptions/" + uuid.NewString() + "/activate"},
		{http.MethodPut, "/v1/admin/users/" + e.tenant.String() + "/subscription"},
		{http.MethodDelete, "/v1/admin/users/" + e.tenant.String() + "/subscription"},
		{http.MethodGet, "/v1/admin/plans"},
		{http.MethodPost, "/v1/admin/plans"},
		{http.MethodPut, "/v1/admin/plans/" + plan},
	}
	for _, r := range routes {
		for _, who := range []string{"landlord", "tenant"} {
			expectError(t, e.call(r.method, r.path, map[string]any{}, who), http.StatusForbidden, apperr.CodeForbidden)
		}
		expectError(t, e.call(r.method, r.path, map[string]any{}, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
	}
	// The catalog belongs to the super admin alone, an admin included.
	for _, r := range routes[4:] {
		expectError(t, e.call(r.method, r.path, map[string]any{}, "admin"), http.StatusForbidden, apperr.CodeForbidden)
	}
}

func TestOnlyASuperAdminEditsTheCatalogAndOneDefaultStaysPerAudience(t *testing.T) {
	e := newPlanEnv(t)
	all := listOf[PlanView](t, e.call(http.MethodGet, "/v1/admin/plans", nil, "super"))
	if all.Meta.Total != 5 || len(all.Data) != 5 || all.Data[0].Audience != "landlord" || all.Data[3].Audience != "tenant" {
		t.Fatalf("catalog = %v %+v", codesOf(all.Data), all.Meta)
	}

	w := e.call(http.MethodPost, "/v1/admin/plans", map[string]any{
		"code": "new_free", "name": "New free", "audience": "landlord", "price_vnd": 0, "max_rooms": 3,
		"features": []string{"vip_badge"}, "is_default": true,
	}, "super")
	expectStatus(t, w, http.StatusCreated)
	created := decodeData[PlanView](t, w)
	if created.Code != "new_free" || !created.IsDefault || !created.IsActive || *created.Limits.MaxRooms != 3 || created.Limits.MaxBuildings != nil || created.BillingInterval != "month" ||
		!slices.Equal(created.Features, []string{"vip_badge"}) {
		t.Fatalf("created = %+v", created)
	}
	if n := e.count(`SELECT count(*) FROM plans WHERE audience = 'landlord' AND is_default`); n != 1 {
		t.Errorf("one default per audience, got %d", n)
	}
	if m := e.mine("landlord"); m.Plan.Code != "new_free" {
		t.Errorf("the new default applies to landlords without a plan: %+v", m.Plan)
	}
	if n := e.count(`SELECT count(*) FROM plans WHERE audience = 'tenant' AND is_default`); n != 1 {
		t.Errorf("the tenant default is untouched, got %d", n)
	}

	// Edits are partial; a null limit means unlimited; PATCH works like PUT; plans are never deleted.
	old := e.planID("landlord_free").String()
	w = e.call(http.MethodPut, "/v1/admin/plans/"+old, map[string]any{"is_active": false, "max_rooms": nil, "name": "Old free"}, "super")
	expectStatus(t, w, http.StatusOK)
	got := decodeData[PlanView](t, w)
	if got.IsActive || got.Limits.MaxRooms != nil || got.Name != "Old free" || *got.Limits.MaxBuildings != 3 || got.Code != "landlord_free" {
		t.Errorf("updated = %+v", got)
	}
	expectStatus(t, e.call(http.MethodPatch, "/v1/admin/plans/"+old, map[string]any{"display_order": 9}, "super"), http.StatusOK)
	if w := e.call(http.MethodDelete, "/v1/admin/plans/"+old, nil, "super"); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("there is no route to delete a plan, got %d", w.Code)
	}

	// Refusals.
	body := expectError(t, e.call(http.MethodPost, "/v1/admin/plans", map[string]any{"code": "Bad Code", "name": "x", "audience": "landlord", "price_vnd": 0, "features": []string{"nope"}}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["features.0"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	body = expectError(t, e.call(http.MethodPost, "/v1/admin/plans", map[string]any{"code": "Bad Code", "name": "x", "audience": "landlord", "price_vnd": 0}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["code"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	body = expectError(t, e.call(http.MethodPost, "/v1/admin/plans", map[string]any{"code": "landlord_pro", "name": "x", "audience": "landlord", "price_vnd": 0}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["code"]) == 0 {
		t.Errorf("a taken code: %v", body.Error.Fields)
	}
	body = expectError(t, e.call(http.MethodPost, "/v1/admin/plans", map[string]any{}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	for _, f := range []string{"code", "name", "audience", "price_vnd"} {
		if len(body.Error.Fields[f]) == 0 {
			t.Errorf("%s is required: %v", f, body.Error.Fields)
		}
	}
	expectError(t, e.call(http.MethodPost, "/v1/admin/plans", map[string]any{"code": "c1", "name": "x", "audience": "landlord", "price_vnd": -1, "max_images_per_room": 0}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodPut, "/v1/admin/plans/"+old, map[string]any{"max_rooms": -3}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodPut, "/v1/admin/plans/"+uuid.NewString(), map[string]any{"name": "x"}, "super"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodPut, "/v1/admin/plans/nope", map[string]any{"name": "x"}, "super"), http.StatusNotFound, apperr.CodeResourceNotFound)
	body = expectError(t, e.call(http.MethodPut, "/v1/admin/plans/"+old, map[string]any{"code": "landlord_vip"}, "super"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["code"]) == 0 {
		t.Errorf("a taken code on update: %v", body.Error.Fields)
	}
}

func TestExpiredSubscriptionsFallBackToTheDefaultPlan(t *testing.T) {
	e := newPlanEnv(t)
	ctx := context.Background()
	vip := e.planID("landlord_vip")
	expectStatus(t, e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": vip}, "admin"), http.StatusCreated)
	if m := e.mine("landlord"); m.Plan.Code != "landlord_vip" {
		t.Fatalf("on vip: %+v", m.Plan)
	}

	// The end passes: even before the daily job runs the lapsed plan no longer counts.
	if _, err := e.pool.Exec(ctx, `UPDATE subscriptions SET starts_at = now() - interval '2 days', ends_at = now() - interval '1 minute' WHERE status = 'active'`); err != nil {
		t.Fatal(err)
	}
	m := e.mine("landlord")
	if m.Plan.Code != "landlord_free" || m.Subscription != nil {
		t.Errorf("a lapsed plan is ignored: %+v", m)
	}
	n, err := e.subs.ExpireDue(ctx)
	if err != nil || n != 1 {
		t.Fatalf("expired %d, %v", n, err)
	}
	if status := e.count(`SELECT count(*) FROM subscriptions WHERE status = 'expired'`); status != 1 {
		t.Errorf("expired rows = %d", status)
	}
	if n, err := e.subs.ExpireDue(ctx); err != nil || n != 0 {
		t.Errorf("a second run does nothing: %d, %v", n, err)
	}
	// The user can take the plan again: the lapsed one no longer holds the slot.
	expectStatus(t, e.call(http.MethodPut, "/v1/admin/users/"+e.landlord.String()+"/subscription", map[string]any{"plan_id": vip}, "admin"), http.StatusCreated)
}
