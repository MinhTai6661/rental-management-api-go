//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/security"
	"rental-management/pkg/authz"
)

// The admin work is tested through the real composition code: the use cases on Postgres, the notification inbox and
// the property use cases behind the bridges, and the HTTP routes with the permissions of the role catalog.

// newAccount creates an account holding the roles and returns it with a bearer that acts in the first role.
func (e *bridgeEnv) newAccount(email string, roles ...string) (uuid.UUID, string) {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.t.Context(), `INSERT INTO users (email, full_name, phone_number) VALUES ($1, 'Nguyen Van A', '0900000000') RETURNING id`, email).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	for _, r := range roles {
		if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
			e.t.Fatal(err)
		}
	}
	e.auth[email] = principalOf(id, roles[0])
	return id, email
}

func (e *bridgeEnv) rolesOf(id uuid.UUID) map[string]bool {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT role_code FROM user_roles WHERE user_id = $1`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			e.t.Fatal(err)
		}
		out[r] = true
	}
	return out
}

type accountRow struct {
	ID    uuid.UUID `json:"id"`
	Roles []string  `json:"roles"`
}

type accountList struct {
	Data []accountRow `json:"data"`
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
}

type bulkBody struct {
	Done   []uuid.UUID `json:"done"`
	Failed []struct {
		ID      uuid.UUID `json:"id"`
		Message string    `json:"message"`
	} `json:"failed"`
}

func TestAdminUserManagement(t *testing.T) {
	e := newBridgeEnv(t)
	victim, vBearer := e.newAccount("victim@example.vn", authz.RoleTenant)
	other, _ := e.newAccount("other-admin@example.vn", authz.RoleAdmin)
	staffOnly := func(bearer string) accountList {
		w := e.call(http.MethodGet, "/v1/admin/users?per_page=100", nil, bearer)
		testenv.ExpectStatus(t, w, http.StatusOK)
		return decodeFull[accountList](t, w)
	}

	adminSees := staffOnly("admin")
	for _, a := range adminSees.Data {
		if a.ID == e.admin || a.ID == e.super || a.ID == other {
			t.Fatalf("an admin sees neither themselves nor staff: %+v", a)
		}
	}
	if got := staffOnly("super"); got.Meta.Total != adminSees.Meta.Total+2 { // the admin and the other admin, not itself
		t.Errorf("a super admin also sees the admins: %d vs %d", got.Meta.Total, adminSees.Meta.Total)
	}
	if got := decodeFull[accountList](t, e.call(http.MethodGet, "/v1/admin/users?search=VICTIM%25", nil, "admin")); got.Meta.Total != 0 {
		t.Errorf("a percent sign is not a wildcard: %d", got.Meta.Total)
	}
	if got := decodeFull[accountList](t, e.call(http.MethodGet, "/v1/admin/users?search=victim&role_name=tenant", nil, "admin")); len(got.Data) != 1 || got.Data[0].ID != victim {
		t.Errorf("search by name or email and by role: %+v", got)
	}
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/admin/users?role_name=wizard", nil, "admin"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/users", nil, "tenant"), http.StatusForbidden)

	// Admins cannot touch staff; nobody touches a super admin; a super admin removes an admin.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+other.String(), nil, "admin"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+e.super.String(), nil, "super"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+e.admin.String(), nil, "admin"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+uuid.NewString(), nil, "admin"), http.StatusNotFound)

	// Deleting closes the sessions and the account can no longer be found.
	session := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO user_sessions (id, user_id, active_role, created_at, last_used_at) VALUES ($1, $2, 'tenant', now(), now())`, session, victim); err != nil {
		t.Fatal(err)
	}
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/admin/users/"+victim.String(), nil, "admin"), http.StatusNoContent)
	var reason *string
	if err := e.pool.QueryRow(t.Context(), `SELECT revoked_reason FROM user_sessions WHERE id = $1`, session).Scan(&reason); err != nil || reason == nil || *reason != "account_deleted" {
		t.Fatalf("the session must be closed: %v %v", reason, err)
	}
	_ = vBearer
	if got := decodeFull[accountList](t, e.call(http.MethodGet, "/v1/admin/users?search=victim", nil, "admin")); len(got.Data) != 0 {
		t.Errorf("a deleted account is not listed: %+v", got)
	}

	// Bulk: one allowed, one refused, one unknown (skipped without a word).
	t1, _ := e.newAccount("bulk1@example.vn", authz.RoleTenant)
	w := e.call(http.MethodPost, "/v1/admin/users/bulk-delete", map[string]any{"ids": []string{t1.String(), other.String(), uuid.NewString()}}, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	res := testenv.DecodeData[bulkBody](t, w)
	if len(res.Done) != 1 || res.Done[0] != t1 || len(res.Failed) != 1 || res.Failed[0].ID != other || res.Failed[0].Message == "" {
		t.Fatalf("bulk: %+v", res)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/users/bulk-delete", map[string]any{"ids": []string{}}, "admin"), http.StatusUnprocessableEntity, "validation.failed")
}

func TestAdminRoles(t *testing.T) {
	e := newBridgeEnv(t)
	target, _ := e.newAccount("target@example.vn", authz.RoleTenant)
	put := func(bearer string, id uuid.UUID, roles any) *httpResult {
		return &httpResult{e.call(http.MethodPut, "/v1/admin/users/"+id.String()+"/roles", map[string]any{"roles": roles}, bearer)}
	}

	testenv.ExpectError(t, put("admin", target, []string{}).w, http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectStatus(t, put("admin", target, []string{"admin"}).w, http.StatusForbidden)
	testenv.ExpectStatus(t, put("super", target, []string{"super_admin"}).w, http.StatusForbidden)
	testenv.ExpectStatus(t, put("admin", e.admin, []string{"tenant"}).w, http.StatusForbidden) // not your own roles
	testenv.ExpectStatus(t, put("admin", e.super, []string{"tenant"}).w, http.StatusForbidden)
	body := testenv.ExpectError(t, put("super", target, []string{"admin", "tenant"}).w, http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["roles"]) == 0 {
		t.Fatalf("a staff role never mixes: %+v", body)
	}

	// Becoming a landlord this way sets the landlord up like an approved application.
	testenv.ExpectStatus(t, put("admin", target, []string{"landlord", "tenant"}).w, http.StatusOK)
	if r := e.rolesOf(target); !r["landlord"] || !r["tenant"] {
		t.Fatalf("roles: %v", r)
	}
	var profiles int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM landlord_profiles WHERE user_id = $1`, target).Scan(&profiles); err != nil || profiles != 1 {
		t.Fatalf("landlord profile: %d %v", profiles, err)
	}

	// The roles an admin may give are listed.
	w := e.call(http.MethodGet, "/v1/admin/roles", nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[[]string](t, w); len(got) != 3 || got[0] != "landlord" {
		t.Fatalf("an admin gives landlord, manager and tenant: %v", got)
	}
	if got := testenv.DecodeData[[]string](t, e.call(http.MethodGet, "/v1/admin/roles", nil, "super")); len(got) != 5 {
		t.Fatalf("a super admin sees all: %v", got)
	}
}

// httpResult only names a recorder so the helper above reads well.
type httpResult struct{ w *httptest.ResponseRecorder }

func TestLandlordApplicationFlow(t *testing.T) {
	e := newBridgeEnv(t)
	body := map[string]any{"business_name": "Nhà trọ An Bình với một cái tên rất dài để cắt bớt đi cho vừa năm mươi ký tự", "phone": "0901234567", "ward_code": 4, "address_detail": "12 Lê Lợi"}

	w := e.call(http.MethodPost, "/v1/my/landlord-application", body, "tenant")
	testenv.ExpectStatus(t, w, http.StatusCreated)
	app := testenv.DecodeData[struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	}](t, w)
	if app.Status != "pending_review" {
		t.Fatalf("it waits for an admin at once: %+v", app)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/landlord-application", body, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/landlord-application", body, "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/my/landlord-application", map[string]any{"phone": "1"}, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	if got := kinds(e.inboxOf("admin", "").Data); got["landlord_application_received"] != 1 {
		t.Errorf("the admins are told: %v", got)
	}
	if got := kinds(e.inboxOf("super", "").Data); got["landlord_application_received"] != 0 {
		t.Errorf("the super admin is not told: %v", got)
	}

	list := e.call(http.MethodGet, "/v1/admin/landlord-applications", nil, "admin")
	testenv.ExpectStatus(t, list, http.StatusOK)
	counts := decodeFull[struct {
		Data []struct{ ID uuid.UUID } `json:"data"`
		Meta struct {
			Counts map[string]int `json:"counts"`
		} `json:"meta"`
	}](t, list)
	if len(counts.Data) != 1 || counts.Meta.Counts["pending_review"] != 1 {
		t.Fatalf("list: %+v", counts)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/landlord-applications", nil, "tenant"), http.StatusForbidden)

	// A rejection needs a reason; it tells the applicant, who may apply again.
	reject := "/v1/admin/landlord-applications/" + app.ID.String() + "/reject"
	testenv.ExpectError(t, e.call(http.MethodPost, reject, map[string]any{}, "admin"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectStatus(t, e.call(http.MethodPost, reject, map[string]any{"note": "thiếu giấy tờ"}, "admin"), http.StatusNoContent)
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/landlord-applications/"+app.ID.String()+"/approve", nil, "admin"), http.StatusUnprocessableEntity, "validation.failed")
	if got := kinds(e.inboxOf("tenant", "").Data); got["landlord_application_rejected"] != 1 {
		t.Errorf("the applicant is told: %v", got)
	}
	again := testenv.DecodeData[struct {
		ID uuid.UUID `json:"id"`
	}](t, e.call(http.MethodPost, "/v1/my/landlord-application", body, "tenant"))
	// Withdrawing removes only the one that waits.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/my/landlord-application", nil, "tenant"), http.StatusNoContent)
	mine := e.call(http.MethodGet, "/v1/my/landlord-application", nil, "tenant")
	if got := testenv.DecodeData[*struct{ Status string }](t, mine); got == nil || got.Status != "rejected" {
		t.Fatalf("the rejected one stays: %+v", got)
	}
	_ = again

	// Approval: role, profile, first building and a notice.
	third := testenv.DecodeData[struct {
		ID uuid.UUID `json:"id"`
	}](t, e.call(http.MethodPost, "/v1/my/landlord-application", body, "tenant"))
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/landlord-applications/"+third.ID.String()+"/approve", nil, "admin"), http.StatusNoContent)
	if r := e.rolesOf(e.tenant); !r["landlord"] || !r["tenant"] {
		t.Fatalf("landlord is added, tenant kept: %v", r)
	}
	var name string
	if err := e.pool.QueryRow(t.Context(), `SELECT name FROM buildings WHERE landlord_id = $1 AND display_order = 1`, e.tenant).Scan(&name); err != nil || len([]rune(name)) > 50 || name == "" {
		t.Fatalf("a first building named after the business: %q %v", name, err)
	}
	if got := kinds(e.inboxOf("tenant", "").Data); got["landlord_application_approved"] != 1 {
		t.Errorf("the applicant is told: %v", got)
	}
	e.auth["tenant-as-landlord"] = principalOf(e.tenant, authz.RoleLandlord)
	prof := e.call(http.MethodGet, "/v1/landlord/profile", nil, "tenant-as-landlord")
	if got := testenv.DecodeData[*struct {
		BusinessName string `json:"business_name"`
	}](t, prof); got == nil || got.BusinessName != body["business_name"] {
		t.Fatalf("profile: %+v", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/landlord/profile", map[string]any{"business_name": "Mới", "description": "Gần chợ"}, "tenant-as-landlord"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/landlord/profile", nil, "tenant"), http.StatusForbidden)
}

func TestRevokeLandlordIsBlockedByRunningContracts(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": today()}, "tenant")
	testenv.ExpectStatus(t, reg, http.StatusCreated)
	c := testenv.DecodeData[contractRow](t, reg)

	type impact struct {
		Rooms           int  `json:"rooms"`
		ActiveContracts int  `json:"active_contracts"`
		Pending         int  `json:"pending_registrations"`
		CanRevoke       bool `json:"can_revoke"`
	}
	path := "/v1/admin/users/" + e.landlord.String()
	got := testenv.DecodeData[impact](t, e.call(http.MethodGet, path+"/landlord-impact", nil, "admin"))
	if got.Rooms != 1 || got.Pending != 1 || got.ActiveContracts != 0 || !got.CanRevoke {
		t.Fatalf("impact before: %+v", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord"), http.StatusOK)
	got = testenv.DecodeData[impact](t, e.call(http.MethodGet, path+"/landlord-impact", nil, "admin"))
	if got.ActiveContracts != 1 || got.CanRevoke {
		t.Fatalf("impact after: %+v", got)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, path+"/revoke-landlord", map[string]any{"note": "x"}, "admin"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectError(t, e.call(http.MethodPut, path+"/roles", map[string]any{"roles": []string{"tenant"}}, "admin"), http.StatusUnprocessableEntity, "validation.failed")
	if !e.rolesOf(e.landlord)["landlord"] {
		t.Fatal("the landlord keeps the role")
	}

	// End the contract: the role can go, the rooms stay, and the landlord is told.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "ended"}, "landlord"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodPost, path+"/revoke-landlord", map[string]any{"note": "vi phạm"}, "admin"), http.StatusNoContent)
	if e.rolesOf(e.landlord)["landlord"] {
		t.Fatal("the role is gone")
	}
	var rooms int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM rooms WHERE landlord_id = $1`, e.landlord).Scan(&rooms); err != nil || rooms != 1 {
		t.Fatalf("the rooms stay: %d %v", rooms, err)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, path+"/landlord-impact", nil, "admin"), http.StatusNotFound)
	if got := kinds(e.inboxOf("landlord-as-tenant", "").Data); got["landlord_revoked"] != 1 {
		t.Errorf("the landlord is told: %v", got)
	}
}

func TestSettingsAreForTheSuperAdmin(t *testing.T) {
	e := newBridgeEnv(t)
	type settings struct {
		Settings []struct {
			Key        string  `json:"key"`
			Value      float64 `json:"value"`
			Overridden bool    `json:"overridden"`
		} `json:"settings"`
		Version string `json:"version"`
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/settings", nil, "admin"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/admin/settings", map[string]any{"settings": []map[string]any{{"key": "invoices.due_days", "value": 10}}}, "admin"), http.StatusForbidden)

	before := testenv.DecodeData[settings](t, e.call(http.MethodGet, "/v1/admin/settings", nil, "super"))
	body := testenv.ExpectError(t, e.call(http.MethodPut, "/v1/admin/settings", map[string]any{"settings": []map[string]any{{"key": "invoices.due_days", "value": 999}, {"key": "nope", "value": 1}}}, "super"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["settings.0.value"]) == 0 || len(body.Error.Fields["settings.1.key"]) == 0 {
		t.Fatalf("each bad entry names its field: %+v", body.Error.Fields)
	}
	after := testenv.DecodeData[settings](t, e.call(http.MethodPut, "/v1/admin/settings", map[string]any{"settings": []map[string]any{{"key": "invoices.due_days", "value": 10}}}, "super"))
	if after.Version == before.Version {
		t.Error("the version changes with the overrides")
	}
	for _, s := range after.Settings {
		if s.Key == "invoices.due_days" && (s.Value != 10 || !s.Overridden) {
			t.Fatalf("override: %+v", s)
		}
	}
	reset := testenv.DecodeData[settings](t, e.call(http.MethodPut, "/v1/admin/settings", map[string]any{"settings": []map[string]any{{"key": "invoices.due_days", "value": nil}}}, "super"))
	if reset.Version != before.Version {
		t.Error("resetting brings the version back")
	}
}

func legalBody(summaryVI string) map[string]any {
	doc := map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{map[string]any{"type": "text", "text": "1. Phạm vi"}}},
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Nội dung", "marks": []any{map[string]any{"type": "bold"}}}}},
	}}
	return map[string]any{
		"content":        map[string]any{"vi": map[string]any{"title": "Điều khoản", "body": doc}, "en": map[string]any{"title": "Terms", "body": doc}},
		"change_summary": map[string]any{"vi": summaryVI, "en": "changed"},
	}
}

func TestLegalDocuments(t *testing.T) {
	e := newBridgeEnv(t)
	type doc struct {
		Version string `json:"version"`
	}
	shown := e.call(http.MethodGet, "/v1/legal/terms", nil, "none")
	testenv.ExpectStatus(t, shown, http.StatusOK)
	if got := testenv.DecodeData[*doc](t, shown); got != nil {
		t.Fatalf("nothing published: %+v", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/legal/cookies", nil, "none"), http.StatusNotFound)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/legal/terms", legalBody("đổi"), "admin"), http.StatusForbidden)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/legal/cookies", legalBody("đổi"), "super"), http.StatusNotFound)

	bad := legalBody("")
	body := testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/legal/terms", bad, "super"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["change_summary.vi"]) == 0 {
		t.Fatalf("the summary is required: %+v", body.Error.Fields)
	}
	script := legalBody("đổi")
	script["content"].(map[string]any)["en"].(map[string]any)["body"] = map[string]any{"type": "doc", "content": []any{map[string]any{"type": "script"}}}
	body = testenv.ExpectError(t, e.call(http.MethodPost, "/v1/admin/legal/terms", script, "super"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["content.en.body"]) == 0 {
		t.Fatalf("the white list refuses it: %+v", body.Error.Fields)
	}

	created := e.call(http.MethodPost, "/v1/admin/legal/terms", legalBody("đổi"), "super")
	testenv.ExpectStatus(t, created, http.StatusCreated)
	v1 := testenv.DecodeData[doc](t, created).Version
	if got := testenv.DecodeData[*doc](t, e.call(http.MethodGet, "/v1/legal/terms", nil, "none")); got == nil || got.Version != v1 {
		t.Fatalf("anybody reads the newest: %+v", got)
	}
	if got := testenv.DecodeData[*doc](t, e.call(http.MethodGet, "/v1/legal/privacy", nil, "none")); got != nil {
		t.Fatalf("privacy was not published: %+v", got)
	}

	// Everybody but staff is asked to accept the new version, and it is what the account shows.
	var accepted *string
	if err := e.pool.QueryRow(t.Context(), `SELECT terms_version FROM users WHERE id = $1`, e.tenant).Scan(&accepted); err != nil || accepted != nil {
		t.Fatalf("the tenant accepted nothing yet: %v %v", accepted, err)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/profile/accept-terms", nil, "tenant"), http.StatusOK)
	if err := e.pool.QueryRow(t.Context(), `SELECT terms_version FROM users WHERE id = $1`, e.tenant).Scan(&accepted); err != nil || accepted == nil || *accepted != v1 {
		t.Fatalf("the accepted version is the one in force: %v %v", accepted, err)
	}

	updates := testenv.DecodeData[[]doc](t, e.call(http.MethodGet, "/v1/profile/legal-updates", nil, "landlord"))
	if len(updates) != 1 {
		t.Fatalf("a person who never accepted sees the newest of each kind: %+v", updates)
	}

	// The admin reads the versions, a page at a time; a document of the other kind is not found.
	vers := e.call(http.MethodGet, "/v1/admin/legal/terms/versions?per_page=10", nil, "super")
	testenv.ExpectStatus(t, vers, http.StatusOK)
	listed := decodeFull[struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct{ Total int } `json:"meta"`
	}](t, vers)
	if listed.Meta.Total != 1 || len(listed.Data) != 1 {
		t.Fatalf("versions: %+v", listed)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/legal/terms/"+listed.Data[0].ID, nil, "super"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/legal/privacy/"+listed.Data[0].ID, nil, "super"), http.StatusNotFound)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/admin/legal/terms/versions?per_page=51", nil, "super"), http.StatusUnprocessableEntity)
}

func TestPersonalDataRights(t *testing.T) {
	e := newBridgeEnv(t)
	hash, err := security.PasswordHasher{}.Hash("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	subject, bearer := e.newAccount("subject@example.vn", authz.RoleTenant)
	if _, err := e.pool.Exec(t.Context(), `UPDATE users SET password_hash = $2, ward_code = 4, address_detail = 'secret street' WHERE id = $1`, subject, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notifications (user_id, kind) VALUES ($1, 'contract_ended')`, subject); err != nil {
		t.Fatal(err)
	}

	export := e.call(http.MethodGet, "/v1/profile/export", nil, bearer)
	testenv.ExpectStatus(t, export, http.StatusOK)
	if export.Header().Get("Content-Disposition") == "" {
		t.Error("the copy is a download")
	}
	acc := testenv.DecodeData[struct {
		Account struct {
			Email string `json:"Email"`
		} `json:"account"`
	}](t, export)
	if acc.Account.Email != "subject@example.vn" {
		t.Fatalf("the export holds the account: %s", export.Body.String())
	}

	check := e.call(http.MethodGet, "/v1/profile/erasure-check", nil, bearer)
	testenv.ExpectStatus(t, check, http.StatusOK)
	if got := testenv.DecodeData[struct {
		Blockers []string `json:"blockers"`
	}](t, check); len(got.Blockers) != 0 {
		t.Fatalf("nothing blocks: %v", got.Blockers)
	}

	erase := func(password string, confirm bool) *httptest.ResponseRecorder {
		return e.call(http.MethodDelete, "/v1/profile", map[string]any{"password": password, "confirm": confirm}, bearer)
	}
	testenv.ExpectError(t, erase("correct-horse-battery", false), http.StatusUnprocessableEntity, "validation.failed")
	body := testenv.ExpectError(t, erase("wrong", true), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["password"]) == 0 {
		t.Fatalf("wrong password: %+v", body.Error.Fields)
	}
	testenv.ExpectError(t, e.call(http.MethodDelete, "/v1/profile", map[string]any{"password": "x", "confirm": true}, "admin"), http.StatusUnprocessableEntity, "validation.failed")

	session := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO user_sessions (id, user_id, active_role, created_at, last_used_at) VALUES ($1, $2, 'tenant', now(), now())`, session, subject); err != nil {
		t.Fatal(err)
	}
	testenv.ExpectStatus(t, erase("correct-horse-battery", true), http.StatusNoContent)

	var (
		name                                string
		email, phone, address, passwordHash *string
		status                              string
		erased, deleted                     bool
		accepts                             bool
		notifications, roles                int
		revoked                             *string
	)
	if err := e.pool.QueryRow(t.Context(), `SELECT full_name, email, phone_number, address_detail, password_hash, status, erased_at IS NOT NULL, deleted_at IS NOT NULL, accepts_invitations FROM users WHERE id = $1`, subject).
		Scan(&name, &email, &phone, &address, &passwordHash, &status, &erased, &deleted, &accepts); err != nil {
		t.Fatal(err)
	}
	if name != "Người dùng đã xóa" || email != nil || phone != nil || address != nil || passwordHash != nil || status != "inactive" || !erased || !deleted || accepts {
		t.Fatalf("the account is blanked: %s %v %v %v %v %s %v %v %v", name, email, phone, address, passwordHash, status, erased, deleted, accepts)
	}
	_ = e.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM notifications WHERE user_id = $1), (SELECT count(*) FROM user_roles WHERE user_id = $1), (SELECT revoked_reason FROM user_sessions WHERE id = $2)`, subject, session).Scan(&notifications, &roles, &revoked)
	if notifications != 0 || roles != 0 || revoked == nil || *revoked != "account_erased" {
		t.Fatalf("notifications=%d roles=%d session=%v", notifications, roles, revoked)
	}
}

func TestErasureIsBlockedByARunningRental(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": today()}, "tenant")
	testenv.ExpectStatus(t, reg, http.StatusCreated)
	check := e.call(http.MethodGet, "/v1/profile/erasure-check", nil, "tenant")
	got := testenv.DecodeData[struct {
		Blockers []string `json:"blockers"`
	}](t, check)
	if len(got.Blockers) != 1 || got.Blockers[0] != "running_rental_as_tenant" {
		t.Fatalf("a registration waiting blocks the tenant: %v", got.Blockers)
	}
	if l := testenv.DecodeData[struct {
		Blockers []string `json:"blockers"`
	}](t, e.call(http.MethodGet, "/v1/profile/erasure-check", nil, "landlord")); len(l.Blockers) != 1 || l.Blockers[0] != "running_contract_as_landlord" {
		t.Fatalf("and the landlord: %v", l.Blockers)
	}
}

// today is the date a registration may start on (the contracts use the Vietnamese calendar day).
func today() string { return time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02") }
