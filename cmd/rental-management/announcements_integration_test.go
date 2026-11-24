//go:build integration

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/testenv"
)

// The announcements are tested through the real composition code: what an admin or a landlord sends through the API
// must reach the inboxes of exactly the people of the audience, in the right role, once, and leave when recalled.

type announcementRow struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status"`
	Audience       string     `json:"audience"`
	RecipientCount int        `json:"recipient_count"`
	ReadCount      int        `json:"read_count"`
	SentAt         *time.Time `json:"sent_at"`
	Revision       int        `json:"revision"`
	Sender         struct {
		Kind string `json:"kind"`
		Role string `json:"role"`
	} `json:"sender"`
	Targeting *struct {
		Roles []string `json:"roles"`
	} `json:"targeting"`
	CTA *struct {
		URL string `json:"url"`
	} `json:"cta"`
}

func texts(vi, en string) map[string]string { return map[string]string{"vi": vi, "en": en} }

func (e *bridgeEnv) send(bearer, path string, body map[string]any) *announcementRow {
	e.t.Helper()
	w := e.call(http.MethodPost, path, body, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusCreated)
	row := testenv.DecodeData[announcementRow](e.t, w)
	return &row
}

func message(audience string, extra map[string]any) map[string]any {
	m := map[string]any{"audience": audience, "title": texts("Xin chào", "Hello"), "body": texts("Nội dung", "Text")}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// withTenantOfLandlord makes the tenant a running tenant of the landlord (registration, then approval, through the API).
func (e *bridgeEnv) withTenantOfLandlord() {
	e.t.Helper()
	room := e.newRoom("R1", "available")
	e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")}, "tenant")
	testenv.ExpectStatus(e.t, reg, http.StatusCreated)
	c := testenv.DecodeData[contractRow](e.t, reg)
	testenv.ExpectStatus(e.t, e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord"), http.StatusOK)
}

func (e *bridgeEnv) reach(bearer, path string, body map[string]any) int {
	e.t.Helper()
	w := e.call(http.MethodPost, path, body, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[struct {
		N int `json:"recipient_count"`
	}](e.t, w).N
}

func announcements(rows []inboxRow) []inboxRow {
	var out []inboxRow
	for _, r := range rows {
		if r.Kind == "announcement" {
			out = append(out, r)
		}
	}
	return out
}

func TestAdminAnnouncementReachesTenantsAndLeavesWhenRecalled(t *testing.T) {
	e := newBridgeEnv(t)

	// Counting uses the delivery definition: the three accounts with the tenant role (the two landlords are tenants too).
	if n := e.reach("admin", "/v1/admin/announcements/reach", map[string]any{"audience": "tenants"}); n != 3 {
		t.Fatalf("reach: %d", n)
	}
	a := e.send("admin", "/v1/admin/announcements", message("tenants", map[string]any{"cta_label": texts("Mở", "Open"), "cta_url": "/find-rooms"}))
	if a.Status != "active" || a.RecipientCount != 3 || a.SentAt == nil || a.Sender.Kind != "platform" || a.Sender.Role != "admin" || a.CTA == nil {
		t.Fatalf("sent: %+v", a)
	}

	// The tenant (and a landlord acting as a tenant) get it in the tenant inbox, with the texts of both languages; the
	// landlord role inbox does not show a tenant announcement; staff are not reached.
	box := e.inboxOf("tenant", "")
	got := announcements(box.Data)
	if len(got) != 1 || got[0].Params["title_vi"] != "Xin chào" || got[0].Params["body_en"] != "Text" || got[0].Params["announcement_id"] != a.ID.String() {
		t.Fatalf("the tenant's inbox: %+v", box.Data)
	}
	if len(announcements(e.inboxOf("landlord-as-tenant", "").Data)) != 1 {
		t.Error("a landlord acting as a tenant is a tenant of the audience")
	}
	if len(announcements(e.inboxOf("landlord", "").Data)) != 0 {
		t.Error("the landlord role inbox does not show it")
	}
	if len(e.inboxOf("admin", "").Data) != 0 || len(e.inboxOf("super", "").Data) != 0 {
		t.Error("staff are not in a tenants audience")
	}

	// The read count follows the tenants reading it.
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/"+got[0].ID.String()+"/read", nil, "tenant"), http.StatusOK)
	w := e.call(http.MethodGet, "/v1/admin/announcements/"+a.ID.String(), nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if d := testenv.DecodeData[announcementRow](t, w); d.ReadCount != 1 {
		t.Errorf("read count: %d", d.ReadCount)
	}

	// Recall: it leaves every inbox; a second recall is a conflict; nothing can change it any more.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/announcements/"+a.ID.String(), nil, "super"), http.StatusNoContent)
	if len(announcements(e.inboxOf("tenant", "").Data)) != 0 {
		t.Error("the tenant's notification is gone")
	}
	testenv.ExpectError(t, e.call(http.MethodDelete, "/v1/admin/announcements/"+a.ID.String(), nil, "admin"), http.StatusConflict, apperr.CodeAnnouncementRecalled)
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/announcements/"+a.ID.String()+"/publish", nil, "admin"), http.StatusConflict, apperr.CodeAnnouncementRecalled)
	testenv.ExpectError(t, e.call(http.MethodPut, "/v1/admin/announcements/"+a.ID.String(), message("tenants", nil), "admin"), http.StatusConflict, apperr.CodeAnnouncementRecalled)
}

func TestStaffSeniorityDecidesWhoIsReached(t *testing.T) {
	e := newBridgeEnv(t)

	// "Everyone" reaches the non-staff accounts only, whoever sends it.
	if n := e.reach("super", "/v1/admin/announcements/reach", map[string]any{"audience": "everyone"}); n != 3 {
		t.Errorf("everyone by a super admin: %d", n)
	}
	e.send("super", "/v1/admin/announcements", message("everyone", nil))
	if len(e.inboxOf("admin", "").Data) != 0 || len(e.inboxOf("super", "").Data) != 0 {
		t.Error("staff are not reached by everyone")
	}

	// A super admin may name admins; an admin may not name any staff role.
	named := map[string]any{"targeting": map[string]any{"roles": []string{"admin"}}}
	if n := e.reach("super", "/v1/admin/announcements/reach", map[string]any{"audience": "custom", "targeting": named["targeting"]}); n != 1 {
		t.Errorf("a super admin names admins: %d", n)
	}
	e.send("super", "/v1/admin/announcements", message("custom", named))
	if len(announcements(e.inboxOf("admin", "").Data)) != 1 || len(e.inboxOf("super", "").Data) != 0 {
		t.Error("only the admin got it, not the sender")
	}
	w := e.call(http.MethodPost, "/v1/admin/announcements", message("custom", named), "admin")
	body := testenv.ExpectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["targeting.roles.0"]) != 1 {
		t.Errorf("an admin may not name admins: %v", body.Error.Fields)
	}
	if n := e.reach("admin", "/v1/admin/announcements/reach", map[string]any{"audience": "custom", "targeting": named["targeting"]}); n != 0 {
		t.Errorf("an admin's count of admins is zero: %d", n)
	}
}

func TestScheduledAnnouncementWaitsForDispatchDueAndIsDeliveredOnce(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := t.Context()
	later := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	a := e.send("admin", "/v1/admin/announcements", message("tenants", map[string]any{"starts_at": later}))
	if a.Status != "scheduled" || a.SentAt != nil || len(announcements(e.inboxOf("tenant", "").Data)) != 0 {
		t.Fatalf("scheduled: %+v", a)
	}
	if n, err := e.inbox.announcements.DispatchDue(ctx); err != nil || n != 0 {
		t.Fatalf("not due yet: %d %v", n, err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE announcements SET starts_at = now() - interval '1 minute' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := e.inbox.announcements.DispatchDue(ctx); err != nil || n != 1 {
		t.Fatalf("due: %d %v", n, err)
	}
	if n, err := e.inbox.announcements.DispatchDue(ctx); err != nil || n != 0 {
		t.Fatalf("a second run delivers nothing: %d %v", n, err)
	}
	if len(announcements(e.inboxOf("tenant", "").Data)) != 1 {
		t.Error("delivered once")
	}
	w := e.call(http.MethodGet, "/v1/admin/announcements/"+a.ID.String(), nil, "admin")
	if d := testenv.DecodeData[announcementRow](t, w); d.Status != "active" || d.RecipientCount != 3 || d.SentAt == nil {
		t.Errorf("after the dispatch: %+v", d)
	}
}

func TestLandlordAnnouncementsAreForTheirOwnPeople(t *testing.T) {
	e := newBridgeEnv(t)
	e.withTenantOfLandlord()

	if n := e.reach("landlord", "/v1/my/announcements/reach", map[string]any{"audience": "my_tenants"}); n != 1 {
		t.Fatalf("reach: %d", n)
	}
	a := e.send("landlord", "/v1/my/announcements", message("my_tenants", nil))
	if a.RecipientCount != 1 || a.Sender.Kind != "landlord" || a.Sender.Role != "landlord" {
		t.Fatalf("sent: %+v", a)
	}
	if len(announcements(e.inboxOf("tenant", "").Data)) != 1 {
		t.Error("the landlord's tenant got it")
	}
	if len(announcements(e.inboxOf("landlord-as-tenant", "").Data)) != 0 || len(e.inboxOf("landlord2", "").Data) != 0 {
		t.Error("nobody else got it")
	}

	// A landlord with nobody gets a clear refusal, and picking a stranger is nobody too.
	body := testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/announcements", message("my_tenants", nil), "landlord2"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["audience"]) != 1 {
		t.Errorf("no people: %v", body.Error.Fields)
	}
	stranger := message("selected_people", map[string]any{"people_ids": []string{e.landlord2.String()}})
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/announcements", stranger, "landlord"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	// The people picker lists the running tenants only; picking one of them works.
	w := e.call(http.MethodGet, "/v1/my/announcements/people?search=t%40example", nil, "landlord")
	testenv.ExpectStatus(t, w, http.StatusOK)
	people := decodeFull[struct {
		Data []struct {
			ID   uuid.UUID `json:"id"`
			Kind string    `json:"kind"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}](t, w)
	if people.Meta.Total != 1 || len(people.Data) != 1 || people.Data[0].ID != e.tenant || people.Data[0].Kind != "tenant" {
		t.Fatalf("people: %+v", people)
	}
	picked := e.send("landlord", "/v1/my/announcements", message("selected_people", map[string]any{"people_ids": []string{e.tenant.String(), e.landlord2.String()}}))
	if picked.RecipientCount != 1 {
		t.Errorf("only the landlord's tenant of the two: %d", picked.RecipientCount)
	}

	// Ownership: an admin cannot see the landlord's message, a landlord cannot see the platform's or another landlord's.
	platform := e.send("admin", "/v1/admin/announcements", message("tenants", map[string]any{"publish": false}))
	for _, c := range []struct{ bearer, path string }{
		{"admin", "/v1/admin/announcements/" + a.ID.String()},
		{"landlord2", "/v1/my/announcements/" + a.ID.String()},
		{"landlord", "/v1/my/announcements/" + platform.ID.String()},
	} {
		testenv.ExpectStatus(t, e.call(http.MethodGet, c.path, nil, c.bearer), http.StatusNotFound)
	}
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/my/announcements/"+platform.ID.String(), nil, "landlord"), http.StatusNotFound)
	// The landlord cannot use the admin routes and a tenant cannot use either.
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/announcements", nil, "landlord"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/my/announcements", nil, "admin"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/my/announcements", nil, "tenant"), http.StatusForbidden)
	// A landlord may not write to the platform's audiences.
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/announcements", message("everyone", nil), "landlord"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	// The lists are apart.
	for bearer, path := range map[string]string{"landlord": "/v1/my/announcements", "admin": "/v1/admin/announcements"} {
		w := e.call(http.MethodGet, path, nil, bearer)
		testenv.ExpectStatus(t, w, http.StatusOK)
		list := decodeFull[struct {
			Data []announcementRow `json:"data"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}](t, w)
		want := map[string]int{"landlord": 2, "admin": 1}[bearer]
		if list.Meta.Total != want || len(list.Data) != want {
			t.Errorf("%s list: %d", bearer, list.Meta.Total)
		}
	}

	// Recall removes the landlord's notification.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/my/announcements/"+a.ID.String(), nil, "landlord"), http.StatusNoContent)
	if len(announcements(e.inboxOf("tenant", "").Data)) != 1 { // the picked one remains
		t.Error("only the recalled one left the inbox")
	}
}

func TestAnnouncementLifecycleAndValidation(t *testing.T) {
	e := newBridgeEnv(t)

	// A draft reaches nobody until it is published; unpublish takes it back without resending.
	draft := e.send("admin", "/v1/admin/announcements", message("tenants", map[string]any{"publish": false}))
	if draft.Status != "draft" || len(announcements(e.inboxOf("tenant", "").Data)) != 0 {
		t.Fatalf("draft: %+v", draft)
	}
	w := e.call(http.MethodPost, "/v1/admin/announcements/"+draft.ID.String()+"/publish", nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if d := testenv.DecodeData[announcementRow](t, w); d.Status != "active" || d.RecipientCount != 3 {
		t.Fatalf("published: %+v", d)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/announcements/"+draft.ID.String()+"/publish", nil, "admin"), http.StatusOK) // idempotent
	w = e.call(http.MethodPost, "/v1/admin/announcements/"+draft.ID.String()+"/unpublish", nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if d := testenv.DecodeData[announcementRow](t, w); d.Status != "draft" {
		t.Errorf("unpublished: %+v", d)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/announcements/"+draft.ID.String()+"/publish", nil, "admin"), http.StatusOK)
	if len(announcements(e.inboxOf("tenant", "").Data)) != 1 {
		t.Error("published twice, sent once")
	}

	// Update is a full replacement; reshow raises the revision.
	w = e.call(http.MethodPut, "/v1/admin/announcements/"+draft.ID.String(), message("tenants", map[string]any{"reshow": true, "type": "warning"}), "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if d := testenv.DecodeData[announcementRow](t, w); d.Revision != 2 {
		t.Errorf("revision: %+v", d)
	}

	// Validation: each rule answers on its field.
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"title without a body":  {message("tenants", map[string]any{"body": map[string]string{"en": "Text"}}), "body.vi"},
		"no text at all":        {map[string]any{"audience": "tenants"}, "title"},
		"unsafe button address": {message("tenants", map[string]any{"cta_label": texts("Mở", "Open"), "cta_url": "javascript:alert(1)"}), "cta_url"},
		"label without address": {message("tenants", map[string]any{"cta_label": texts("Mở", "Open")}), "cta_url"},
		"no channel":            {message("tenants", map[string]any{"send_notification": false}), "send_notification"},
		"custom with nothing":   {message("custom", nil), "targeting"},
		"unknown audience":      {message("nobody", nil), "audience"},
		"unknown type":          {message("tenants", map[string]any{"type": "loud"}), "type"},
		"bad date":              {message("custom", map[string]any{"targeting": map[string]any{"registered_before": "yesterday"}}), "targeting.registered_before"},
		"ends before start": {message("tenants", map[string]any{
			"starts_at": "2030-01-02T00:00:00Z", "ends_at": "2030-01-01T00:00:00Z"}), "ends_at"},
	} {
		body := testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/announcements", c.body, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		if len(body.Error.Fields[c.field]) == 0 {
			t.Errorf("%s: %v", name, body.Error.Fields)
		}
	}
	// The filters of the list.
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/announcements?status=nope", nil, "admin"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/announcements?from=2026-13-40", nil, "admin"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/announcements?status=active&type=warning&channel=notification&from=2020-01-01&to=2099-01-01", nil, "admin"), http.StatusOK)
}
