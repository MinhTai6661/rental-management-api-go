//go:build integration

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/config"
	"rental-management/internal/adapter/http/testenv"
	auditpg "rental-management/internal/adapter/persistence/postgres/audit"
	domain "rental-management/internal/domain/audit"
	audituc "rental-management/internal/usecase/audit"
	"rental-management/pkg/authz"
	"rental-management/pkg/reqctx"
)

// The audit trail is tested through the real composition code: the contexts write through their bridges, the routes
// read it back with the permissions of the role catalog.

type auditLine struct {
	ID     int64  `json:"id"`
	Tier   string `json:"tier"`
	Action string `json:"action"`
	Actor  struct {
		ID   *uuid.UUID `json:"id"`
		Name *string    `json:"name"`
		Role *string    `json:"role"`
	} `json:"actor"`
	ActingFor  *uuid.UUID     `json:"acting_for_id"`
	RoomID     *uuid.UUID     `json:"room_id"`
	Changes    map[string]any `json:"changes"`
	Meta       map[string]any `json:"meta"`
	IP         *string        `json:"ip"`
	UserAgent  *string        `json:"user_agent"`
	RequestID  *string        `json:"request_id"`
	Count      *int           `json:"count"`
	TargetUser *struct {
		ID uuid.UUID `json:"id"`
	} `json:"target_user"`
}

type auditFeed struct {
	Data []auditLine `json:"data"`
	Meta struct {
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
		HasMore *bool `json:"has_more"`
		Total   *int  `json:"total"`
	} `json:"meta"`
}

func (e *bridgeEnv) feed(bearer, path string) auditFeed {
	e.t.Helper()
	w := e.call(http.MethodGet, path, nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return decodeFull[auditFeed](e.t, w)
}

func actionsOf(f auditFeed) []string {
	out := make([]string, len(f.Data))
	for i, l := range f.Data {
		out[i] = l.Action
	}
	return out
}

func TestAuditTrailOfARoomIsForItsLandlord(t *testing.T) {
	e := newBridgeEnv(t)
	r := e.newRoom("P1", "available")
	e.publish("landlord", r.ID) // listing.created, listing.submitted, listing.approved

	hist := e.feed("landlord", "/v1/rooms/"+r.ID.String()+"/history")
	if hist.Meta.HasMore == nil || hist.Meta.Total != nil {
		t.Errorf("a feed says whether more follows and never counts: %+v", hist.Meta)
	}
	got := actionsOf(hist)
	for _, want := range []string{"listing.approved", "listing.submitted", "listing.created"} {
		if !contains(got, want) {
			t.Errorf("history of the room misses %s: %v", want, got)
		}
	}
	for _, l := range hist.Data {
		if l.RoomID == nil || *l.RoomID != r.ID {
			t.Errorf("only this room: %+v", l)
		}
		if l.Tier != "important" {
			t.Errorf("the history holds the important tier only: %+v", l)
		}
	}
	created := lineOf(hist, "listing.created")
	if created.Actor.ID == nil || *created.Actor.ID != e.landlord || created.Actor.Role == nil || *created.Actor.Role != authz.RoleLandlord {
		t.Errorf("who and in which role: %+v", created.Actor)
	}
	approved := lineOf(hist, "listing.approved")
	if approved.ActingFor == nil || *approved.ActingFor != e.landlord || *approved.Actor.Role != authz.RoleAdmin {
		t.Errorf("the admin acted on the landlord's data: %+v", approved)
	}

	// Page 1 of size 1 has more; the last page does not.
	one := e.feed("landlord", "/v1/rooms/"+r.ID.String()+"/history?per_page=1")
	if len(one.Data) != 1 || one.Meta.HasMore == nil || !*one.Meta.HasMore {
		t.Errorf("%+v", one)
	}
	last := e.feed("landlord", "/v1/rooms/"+r.ID.String()+"/history?per_page=1&page=3")
	if len(last.Data) != 1 || *last.Meta.HasMore {
		t.Errorf("%+v", last)
	}

	// Anybody else gets a 404 as for a room that does not exist; a tenant is not allowed at all.
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/rooms/"+r.ID.String()+"/history", nil, "landlord2"), http.StatusNotFound)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/rooms/"+uuid.NewString()+"/history", nil, "landlord"), http.StatusNotFound)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/rooms/"+r.ID.String()+"/history", nil, "tenant"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/rooms/"+r.ID.String()+"/history", nil, "admin"), http.StatusForbidden)
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/rooms/"+r.ID.String()+"/history?per_page=500", nil, "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/contracts/"+uuid.NewString()+"/history", nil, "landlord"), http.StatusNotFound)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/invoices/"+uuid.NewString()+"/history", nil, "landlord"), http.StatusNotFound)
}

func TestAuditAdminViewHidesStaffSettingsAndMoney(t *testing.T) {
	e := newBridgeEnv(t)
	// A setting change by the super admin, a role change by the admin, and a landlord's money entry.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/admin/settings", map[string]any{"settings": []map[string]any{{"key": "invoices.due_days", "value": 10}}}, "super"), http.StatusOK)
	victim, _ := e.newAccount("victim@example.vn", authz.RoleTenant)
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/admin/users/"+victim.String()+"/roles", map[string]any{"roles": []string{authz.RoleTenant, authz.RoleLandlord}}, "admin"), http.StatusOK)
	r := e.newRoom("P1", "available")
	ctx := reqctx.WithActor(reqctx.With(context.Background(), reqctx.Info{IP: "1.1.1.1"}), e.landlord.String(), authz.RoleLandlord, "")
	rec := e.trail.recorder
	if err := rec.Record(ctx, audituc.Event{
		Action: domain.InvoiceUpdated, Refs: domain.References{RoomID: &r.ID},
		Changes: map[string]any{"total_amount_vnd": map[string]any{"from": 100000, "to": 250000}, "note": "x"}, Meta: map[string]any{"price_vnd": 7},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(ctx, audituc.Event{Action: domain.PlanSaved, Actor: &audituc.Actor{ID: e.super}, Meta: map[string]any{"price_vnd": 99000}}); err != nil {
		t.Fatal(err)
	}

	adminSees := e.feed("admin", "/v1/admin/audit-logs?per_page=100")
	if adminSees.Meta.PerPage != 100 || adminSees.Meta.HasMore == nil {
		t.Errorf("%+v", adminSees.Meta)
	}
	for _, l := range adminSees.Data {
		if l.Action == "setting.changed" || (l.Actor.Role != nil && (*l.Actor.Role == "admin" || *l.Actor.Role == "super_admin")) {
			t.Errorf("an admin must not see the staff or the settings: %+v", l)
		}
		if l.Actor.ID != nil && (*l.Actor.ID == e.admin || *l.Actor.ID == e.super) {
			t.Errorf("nor entries of staff people: %+v", l)
		}
	}
	money := lineOf(adminSees, "invoice.updated")
	if money.Changes["note"] != "x" || money.Meta["price_vnd"] != "•••" || money.Changes["total_amount_vnd"].(map[string]any)["to"] != "•••" {
		t.Errorf("the amounts are hidden and the rest is not: %+v", money)
	}

	superSees := e.feed("super", "/v1/admin/audit-logs?per_page=100")
	if !contains(actionsOf(superSees), "setting.changed") || !contains(actionsOf(superSees), "user.roles_changed") {
		t.Errorf("a super admin sees the settings and the staff: %v", actionsOf(superSees))
	}
	if m := lineOf(superSees, "invoice.updated"); m.Meta["price_vnd"] != "•••" {
		t.Errorf("not even a super admin sees the amounts of rent: %+v", m)
	}
	if plan := lineOf(superSees, "plan.saved"); plan.Meta["price_vnd"] != float64(99000) {
		t.Errorf("the platform's own prices stay: %+v", plan)
	}

	// Filters.
	byAction := e.feed("super", "/v1/admin/audit-logs?action=setting.changed")
	if len(byAction.Data) != 1 || byAction.Data[0].Meta["key"] != "invoices.due_days" {
		t.Errorf("%+v", byAction.Data)
	}
	if n := len(e.feed("super", "/v1/admin/audit-logs?actor_id="+e.admin.String()).Data); n != 1 {
		t.Errorf("by actor: %d", n)
	}
	if n := len(e.feed("super", "/v1/admin/audit-logs?target_user_id="+victim.String()).Data); n != 1 {
		t.Errorf("by target: %d", n)
	}
	if n := len(e.feed("super", "/v1/admin/audit-logs?room_id="+r.ID.String()).Data); n != 1 {
		t.Errorf("by room: %d", n)
	}
	if n := len(e.feed("super", "/v1/admin/audit-logs?tier=routine").Data); n != 0 {
		t.Errorf("by tier: %d", n)
	}
	if n := len(e.feed("super", "/v1/admin/audit-logs?from=2000-01-01&to=2000-01-02").Data); n != 0 {
		t.Errorf("by days: %d", n)
	}
	for _, bad := range []string{"action=nope", "tier=weird", "actor_id=x", "from=yesterday", "from=2026-02-02&to=2026-01-01", "actor_role=" + strings.Repeat("x", 30)} {
		testenv.ExpectError(t, e.call(http.MethodGet, "/v1/admin/audit-logs?"+bad, nil, "super"), http.StatusUnprocessableEntity, "validation.failed")
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/audit-logs", nil, "landlord"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/audit-logs", nil, "tenant"), http.StatusForbidden)
}

func TestAuditRequestFactsAreRecorded(t *testing.T) {
	e := newBridgeEnv(t)
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/settings", strings.NewReader(`{"settings":[{"key":"invoices.due_days","value":12}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer super")
	req.Header.Set("User-Agent", "audit-test/1.0")
	req.Header.Set("X-Request-Id", "req-audit-1")
	req.RemoteAddr = "203.0.113.9:4000"
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	testenv.ExpectStatus(t, w, http.StatusOK)

	var role, ua, rid, ip string
	var actor uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT actor_id, actor_role, user_agent, request_id, ip FROM audit_logs WHERE action = 'setting.changed'`).Scan(&actor, &role, &ua, &rid, &ip); err != nil {
		t.Fatal(err)
	}
	if actor != e.super || role != "super_admin" || ua != "audit-test/1.0" || rid != "req-audit-1" || ip != "203.0.113.9" {
		t.Errorf("%v %q %q %q %q", actor, role, ua, rid, ip)
	}

	// The same change shows up in the history of the settings with who made it.
	hist := testenv.DecodeData[[]struct {
		Key       string   `json:"key"`
		From      *float64 `json:"from"`
		To        *float64 `json:"to"`
		ChangedBy *struct {
			ID uuid.UUID `json:"id"`
		} `json:"changed_by"`
	}](t, e.call(http.MethodGet, "/v1/admin/settings/history", nil, "super"))
	if len(hist) != 1 || hist[0].Key != "invoices.due_days" || hist[0].To == nil || *hist[0].To != 12 || hist[0].ChangedBy == nil || hist[0].ChangedBy.ID != e.super {
		t.Errorf("%+v", hist)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/settings/history", nil, "admin"), http.StatusForbidden)
}

func TestAuditMyActivityShowsOthersOnlyAsARole(t *testing.T) {
	e := newBridgeEnv(t)
	login := reqctx.With(context.Background(), reqctx.Info{IP: "9.9.9.9", UserAgent: "phone"})
	session := uuid.New()
	// A recorder whose clock stands still, so the burst below falls in one second whatever the speed of the machine.
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	still := audituc.NewRecorder(auditpg.NewRepository(e.pool), audituc.ClockFunc(func() time.Time { return at }), slog.New(slog.DiscardHandler))
	for i := 0; i < 3; i++ { // a burst: three identical entries in one second merge
		if err := still.Record(login, audituc.Event{
			Action: domain.AuthLogin, Actor: &audituc.Actor{ID: e.tenant, Role: authz.RoleTenant, SessionID: &session}, Refs: domain.References{TargetUserID: &e.tenant},
		}); err != nil {
			t.Fatal(err)
		}
	}
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/admin/users/"+e.tenant.String()+"/roles", map[string]any{"roles": []string{authz.RoleTenant, authz.RoleLandlord}}, "admin"), http.StatusOK)

	mine := e.feed("tenant", "/v1/my/activity")
	if mine.Meta.PerPage != 20 || len(mine.Data) < 2 {
		t.Fatalf("%+v", mine)
	}
	byAdmin := lineOf(mine, "user.roles_changed")
	if byAdmin.Actor.ID != nil || byAdmin.Actor.Name != nil || byAdmin.Actor.Role == nil || *byAdmin.Actor.Role != authz.RoleAdmin ||
		byAdmin.IP != nil || byAdmin.Changes != nil || byAdmin.Meta != nil || byAdmin.TargetUser != nil {
		t.Errorf("what an admin did to my account shows only as a role: %+v", byAdmin)
	}
	own := lineOf(mine, "auth.login")
	if own.Actor.ID == nil || own.IP == nil || *own.IP != "9.9.9.9" || own.Count == nil || *own.Count != 3 || own.Tier != "routine" {
		t.Errorf("my own entries are whole, a burst counts once: %+v", own)
	}
	// Somebody else's activity never shows.
	for _, l := range e.feed("landlord2", "/v1/my/activity").Data {
		t.Errorf("another person's entry: %+v", l)
	}
}

func TestAuditScrubKeepsTheTrailButNotTheDevice(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := reqctx.With(context.Background(), reqctx.Info{IP: "9.9.9.9", UserAgent: "phone", RequestID: "r"})
	session := uuid.New()
	if err := e.trail.recorder.Record(ctx, audituc.Event{Action: domain.AuthLogin, Actor: &audituc.Actor{ID: e.tenant, Role: "tenant", SessionID: &session}}); err != nil {
		t.Fatal(err)
	}
	if err := e.trail.recorder.Record(ctx, audituc.Event{Action: domain.ContractRegistered, Actor: &audituc.Actor{ID: e.tenant, Role: "tenant", SessionID: &session}}); err != nil {
		t.Fatal(err)
	}
	if err := (adminAudit{r: e.trail.recorder}).ScrubPersonalData(ctx, e.tenant); err != nil {
		t.Fatal(err)
	}
	var left, kept int
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_logs WHERE ip IS NOT NULL OR user_agent IS NOT NULL OR session_id IS NOT NULL)
		+ (SELECT count(*) FROM audit_activity WHERE ip IS NOT NULL OR user_agent IS NOT NULL OR session_id IS NOT NULL)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_logs WHERE actor_id = $1) + (SELECT count(*) FROM audit_activity WHERE actor_id = $1)`, e.tenant).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if left != 0 || kept != 2 {
		t.Errorf("the address, device and session go, the entries stay: %d left, %d kept", left, kept)
	}
}

func TestAuditAnImportantEntryFailsTheActionARoutineOneDoesNot(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	if err := e.trail.recorder.Record(ctx, audituc.Event{Action: domain.Action("nope.nope")}); err == nil {
		t.Error("an action outside the catalog is refused")
	}
	// A routine entry that cannot be written (its table is gone) is swallowed; an important one is not.
	if _, err := e.pool.Exec(ctx, `DROP TABLE audit_activity`); err != nil {
		t.Fatal(err)
	}
	if err := e.trail.recorder.Record(ctx, audituc.Event{Action: domain.AuthLogin}); err != nil {
		t.Errorf("a routine entry never fails the request: %v", err)
	}
	if err := e.trail.recorder.Record(ctx, audituc.Event{Action: domain.RoomCreated}); err != nil {
		t.Errorf("the important tier is still fine: %v", err)
	}
}

func TestAuditMaintainMakesAndDropsPartitions(t *testing.T) {
	e := newBridgeEnv(t)
	trail := newAudit(config.Config{AuditPartitionsAhead: 3, AuditImportantRetentionMonths: 24, AuditRoutineRetentionDays: 90}, slog.New(slog.DiscardHandler), e.pool)
	summary, err := maintainAudit(t.Context(), trail)
	if err != nil || !strings.Contains(summary, "created 0 partitions") {
		t.Fatalf("the migration already made them: %q %v", summary, err)
	}
	// An old month past the retention goes whole, with its rows.
	if _, err := e.pool.Exec(t.Context(), `CREATE TABLE audit_activity_2020_01 PARTITION OF audit_activity FOR VALUES FROM ('2020-01-01T00:00:00Z') TO ('2020-02-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO audit_activity (created_at, action) VALUES ('2020-01-15T00:00:00Z', 'auth.login')`); err != nil {
		t.Fatal(err)
	}
	summary, err = maintainAudit(t.Context(), trail)
	if err != nil || !strings.Contains(summary, "dropped 1") {
		t.Fatalf("%q %v", summary, err)
	}
	var left int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_activity WHERE created_at < '2021-01-01'`).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d %v", left, err)
	}
}

func lineOf(f auditFeed, action string) auditLine {
	for _, l := range f.Data {
		if l.Action == action {
			return l
		}
	}
	return auditLine{}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
