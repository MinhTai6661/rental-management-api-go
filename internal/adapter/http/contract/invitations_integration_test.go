//go:build integration

package contracthttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	contracthttp "rental-management/internal/adapter/http/contract"
)

type (
	InvitationView = contracthttp.InvitationView
	issuedView     = contracthttp.IssuedInvitationView
)

// invite sends an offer as the landlord and returns the response (the caller checks the status).
func (e *contractEnv) invite(bearer string, body map[string]any) *testResponse {
	return &testResponse{e.call(http.MethodPost, "/v1/contract-invitations", body, bearer)}
}

func (e *contractEnv) offer(room uuid.UUID, extra map[string]any) issuedView {
	e.t.Helper()
	body := map[string]any{"room_id": room, "starts_on": dateStr(today())}
	for k, v := range extra {
		body[k] = v
	}
	w := e.invite("landlord-a", body)
	expectStatus(e.t, w.ResponseRecorder, http.StatusCreated)
	return decodeData[issuedView](e.t, w.ResponseRecorder)
}

func tokenOf(link string) string { return link[strings.Index(link, "token=")+len("token="):] }

func (e *contractEnv) invitationRow(id uuid.UUID) (hash string, sealed *string, status string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT token_hash, link_token_sealed, status FROM contract_invitations WHERE id = $1`, id).Scan(&hash, &sealed, &status); err != nil {
		e.t.Fatal(err)
	}
	return hash, sealed, status
}

func (e *contractEnv) invitationCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM contract_invitations`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestCreatingAnInvitationShowsTheLinkOnceAndStoresOnlyTheHash(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")

	got := e.offer(room, map[string]any{"occupant_count": 2, "deposit_vnd": 2_400_000, "note": "Phòng tầng 2", "contract_status": "pending"})
	token := tokenOf(got.Link)
	if !strings.HasPrefix(got.Link, "http://web.test/contract-invitation?token=") || len(token) != 64 {
		t.Fatalf("link = %q", got.Link)
	}
	inv := got.Invitation
	if inv.Status != "pending" || inv.Expired || inv.Audience != "public" || inv.Recipient != nil || inv.RecipientEmail != nil ||
		inv.PriceVND != 3_000_000 || *inv.DepositVND != 2_400_000 || inv.OccupantCount != 2 || inv.ContractStatus != "pending" ||
		*inv.Note != "Phòng tầng 2" || !inv.LinkAvailable || inv.StartsOn != dateStr(today()) || inv.EndsOn != nil || inv.Room.Name != "P101" {
		t.Errorf("invitation = %+v", inv)
	}
	if got.Delivery.App || got.Delivery.Email || len(e.notices.sent) != 0 || len(e.mails.sent) != 0 {
		t.Error("an offer to anybody is sent nowhere")
	}
	if time.Until(inv.ExpiresAt) < 6*24*time.Hour || time.Until(inv.ExpiresAt) > 8*24*time.Hour {
		t.Errorf("expires %v", inv.ExpiresAt)
	}

	// The table keeps the hash and an encrypted copy, never the token.
	hash, sealed, _ := e.invitationRow(inv.ID)
	if hash == token || strings.Contains(hash, token) || sealed == nil || strings.Contains(*sealed, token) {
		t.Errorf("stored hash=%q sealed=%v", hash, sealed)
	}

	// Neither the list nor the detail ever carry the link.
	for _, path := range []string{"/v1/contract-invitations", "/v1/contract-invitations/" + inv.ID.String()} {
		w := e.call(http.MethodGet, path, nil, "landlord-a")
		expectStatus(t, w, http.StatusOK)
		if strings.Contains(w.Body.String(), token) {
			t.Errorf("%s shows the secret", path)
		}
	}
	w := e.call(http.MethodGet, "/v1/contract-invitations", nil, "landlord-a")
	var list struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Meta.Total != 1 || len(list.Data) != 1 || list.Data[0]["link_available"] != true {
		t.Errorf("list = %s", w.Body.String())
	}
	if _, has := list.Data[0]["link"]; has {
		t.Error("the list has a link member")
	}
}

func TestInvitationRoutesAreForLandlordsOnly(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	made := e.offer(room, nil).Invitation.ID.String()
	base := "/v1/contract-invitations"
	calls := []struct{ method, path string }{
		{http.MethodGet, base}, {http.MethodPost, base}, {http.MethodGet, base + "/" + made}, {http.MethodGet, base + "/" + made + "/link"},
		{http.MethodPost, base + "/" + made + "/regenerate"}, {http.MethodDelete, base + "/" + made},
	}
	body := map[string]any{"room_id": room, "starts_on": dateStr(today())}
	for _, bearer := range []string{"tenant-t", "admin", "tenant-nothing"} {
		for _, c := range calls {
			if w := e.call(c.method, c.path, body, bearer); w.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d", bearer, c.method, c.path, w.Code)
			}
		}
	}
	for _, c := range calls {
		if w := e.call(c.method, c.path, body, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d", c.method, c.path, w.Code)
		}
	}
	if status := e.invitationCount(); status != 1 {
		t.Errorf("nothing was made by the refused calls: %d", status)
	}
}

func TestInvitationCreateValidation(t *testing.T) {
	e := newContractEnv(t, nil)
	mine := e.room(e.landlA, "P101")
	theirs := e.room(e.landlB, "Q1")
	day := dateStr(today())

	w := e.invite("landlord-a", map[string]any{"room_id": theirs, "starts_on": day})
	expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
	if len(w.fields()["room_id"]) == 0 {
		t.Errorf("another landlord's room reads like no room: %v", w.fields())
	}
	if w := e.invite("landlord-a", map[string]any{"room_id": uuid.New(), "starts_on": day}); len(w.fields()["room_id"]) == 0 {
		t.Errorf("no such room: %v", w.fields())
	}

	tags := e.invite("landlord-a", map[string]any{
		"room_id": mine, "starts_on": day, "occupant_count": 51, "note": strings.Repeat("x", 1001), "email": "not an email",
	})
	expectError(t, tags.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
	for _, f := range []string{"occupant_count", "note", "email"} {
		if len(tags.fields()[f]) == 0 {
			t.Errorf("field %s is not reported: %v", f, tags.fields())
		}
	}
	bad := e.invite("landlord-a", map[string]any{
		"room_id": mine, "starts_on": "soon", "ends_on": "later", "price_vnd": -1, "deposit_vnd": 2_000_000_000_000, "contract_status": "ended",
	})
	expectError(t, bad.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
	for _, f := range []string{"starts_on", "ends_on", "price_vnd", "deposit_vnd", "contract_status"} {
		if len(bad.fields()[f]) == 0 {
			t.Errorf("field %s is not reported: %v", f, bad.fields())
		}
	}
	empty := e.invite("landlord-a", map[string]any{})
	if f := empty.fields(); len(f["room_id"]) == 0 || len(f["starts_on"]) == 0 {
		t.Errorf("required fields: %v", f)
	}
	endBeforeStart := e.invite("landlord-a", map[string]any{"room_id": mine, "starts_on": day, "ends_on": day})
	if len(endBeforeStart.fields()["ends_on"]) == 0 {
		t.Errorf("ends_on: %v", endBeforeStart.fields())
	}
	// A stay shorter than the room's minimum (a month) is refused on ends_on.
	short := e.invite("landlord-a", map[string]any{"room_id": mine, "starts_on": day, "ends_on": dateStr(today().AddDate(0, 0, 10))})
	if len(short.fields()["ends_on"]) == 0 {
		t.Errorf("minimum period: %v", short.fields())
	}
	if n := e.invitationCount(); n != 0 {
		t.Errorf("%d invitations were stored by refused requests", n)
	}
}

func TestAnActiveOfferOnARoomATenantHoldsIsRefused(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	e.approve("landlord-a", e.register("tenant-t", room, nil).ID)

	w := e.invite("landlord-a", map[string]any{"room_id": room, "starts_on": dateStr(today())})
	expectError(t, w.ResponseRecorder, http.StatusConflict, "contract.room_occupied")
	// An offer that only waits for the landlord's own answer does not take the room, so it is allowed.
	ok := e.invite("landlord-a", map[string]any{"room_id": room, "starts_on": dateStr(today()), "contract_status": "pending"})
	expectStatus(t, ok.ResponseRecorder, http.StatusCreated)
}

func TestInvitationAddressedToAnAccountIsDeliveredToItsInboxAndEmail(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")

	got := e.offer(room, map[string]any{"recipient_id": e.tenantT})
	inv := got.Invitation
	if inv.Audience != "account" || inv.Recipient == nil || inv.Recipient.ID != e.tenantT || inv.Recipient.FullName != "Tenant T" ||
		inv.Recipient.EmailMasked == nil || *inv.Recipient.EmailMasked != "t**@example.vn" {
		t.Errorf("recipient = %+v", inv.Recipient)
	}
	if !got.Delivery.App || !got.Delivery.Email {
		t.Errorf("delivery = %+v", got.Delivery)
	}
	if len(e.notices.sent) != 1 || e.notices.sent[0].UserID != e.tenantT || e.notices.sent[0].Kind != "contract_invitation_received" ||
		e.notices.sent[0].URL != "/contract-invitation?invitation="+inv.ID.String() || strings.Contains(e.notices.sent[0].URL, "token=") {
		t.Errorf("notifications = %+v", e.notices.sent)
	}
	if len(e.mails.sent) != 1 || e.mails.sent[0].To != "t@example.vn" || strings.Contains(e.mails.sent[0].URL, "token=") ||
		!strings.Contains(e.mails.sent[0].URL, "invitation="+inv.ID.String()) || e.mails.sent[0].Landlord != "Landlord A" || e.mails.sent[0].Room != "P101" {
		t.Errorf("mails = %+v", e.mails.sent)
	}
}

func TestInvitationToTheEmailOfAnAccountIsAddressedToTheAccount(t *testing.T) {
	e := newContractEnv(t, nil)
	got := e.offer(e.room(e.landlA, "P101"), map[string]any{"email": "U@Example.VN"})
	if got.Invitation.Audience != "account" || got.Invitation.Recipient == nil || got.Invitation.Recipient.ID != e.tenantU ||
		got.Invitation.RecipientEmail != nil || !got.Delivery.App || !got.Delivery.Email {
		t.Errorf("invitation = %+v delivery %+v", got.Invitation, got.Delivery)
	}
}

func TestInvitationToAnAddressNobodyHoldsIsMailedWithTheTokenLink(t *testing.T) {
	e := newContractEnv(t, nil)
	got := e.offer(e.room(e.landlA, "P101"), map[string]any{"email": "New.Person@Example.vn"})
	if got.Invitation.Audience != "email" || got.Invitation.Recipient != nil || *got.Invitation.RecipientEmail != "new.person@example.vn" {
		t.Errorf("invitation = %+v", got.Invitation)
	}
	if got.Delivery.App || !got.Delivery.Email || len(e.notices.sent) != 0 {
		t.Errorf("delivery = %+v", got.Delivery)
	}
	if len(e.mails.sent) != 1 || e.mails.sent[0].To != "new.person@example.vn" || e.mails.sent[0].URL != got.Link {
		t.Errorf("the mail carries the link: %+v vs %q", e.mails.sent, got.Link)
	}

	// An address that asked for no more invitations is not mailed; the offer still exists and the link works.
	e.invitees.blocked["blocked@example.vn"] = true
	again := e.offer(e.room(e.landlA, "P102"), map[string]any{"email": "blocked@example.vn"})
	if again.Delivery.App || again.Delivery.Email || len(e.mails.sent) != 1 {
		t.Errorf("delivery = %+v, mails = %d", again.Delivery, len(e.mails.sent))
	}
}

func TestInvitationRecipientProblemsAreRefusedAndNothingIsStored(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	cases := map[string]struct {
		body  map[string]any
		field string
		rule  string
	}{
		"an id that is nobody":              {map[string]any{"recipient_id": uuid.New()}, "recipient_id", "recipient_invalid"},
		"the landlord's own email":          {map[string]any{"email": "a@example.vn"}, "email", "own_email"},
		"an id and another account's email": {map[string]any{"recipient_id": e.tenantT, "email": "u@example.vn"}, "email", "recipient_mismatch"},
		"not an email":                      {map[string]any{"email": "not an email"}, "email", "email"},
	}
	for name, tc := range cases {
		body := map[string]any{"room_id": room, "starts_on": dateStr(today())}
		for k, v := range tc.body {
			body[k] = v
		}
		w := e.invite("landlord-a", body)
		expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
		if len(w.fields()[tc.field]) == 0 {
			t.Errorf("%s: fields = %v", name, w.fields())
		}
	}
	if e.invitationCount() != 0 || len(e.notices.sent) != 0 || len(e.mails.sent) != 0 {
		t.Error("nothing was stored or sent")
	}
}

func TestTheLinkIsCopiedAgainRegeneratedAndRevoked(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), nil)
	id := made.Invitation.ID
	path := "/v1/contract-invitations/" + id.String()

	w := e.call(http.MethodGet, path+"/link", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[contracthttp.InvitationLinkView](t, w); got.Link != made.Link || !got.ExpiresAt.Equal(made.Invitation.ExpiresAt) {
		t.Errorf("the same link comes back: %+v vs %q", got, made.Link)
	}

	// Another landlord reaches nothing.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, path}, {http.MethodGet, path + "/link"}, {http.MethodPost, path + "/regenerate"}, {http.MethodDelete, path},
	} {
		expectError(t, e.call(c.method, c.path, nil, "landlord-b"), http.StatusNotFound, "resource.not_found")
	}
	expectError(t, e.call(http.MethodGet, "/v1/contract-invitations/not-a-uuid", nil, "landlord-a"), http.StatusNotFound, "resource.not_found")

	oldHash, _, _ := e.invitationRow(id)
	w = e.call(http.MethodPost, path+"/regenerate", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	renewed := decodeData[contracthttp.RenewedInvitationView](t, w)
	if renewed.Link == made.Link || len(tokenOf(renewed.Link)) != 64 || renewed.Invitation.ID != id {
		t.Errorf("a new link: %q", renewed.Link)
	}
	if newHash, _, _ := e.invitationRow(id); newHash == oldHash {
		t.Error("the old secret is gone")
	}
	w = e.call(http.MethodGet, path+"/link", nil, "landlord-a")
	if got := decodeData[contracthttp.InvitationLinkView](t, w); got.Link != renewed.Link {
		t.Errorf("the new link is what is shown now: %q vs %q", got.Link, renewed.Link)
	}

	expectStatus(t, e.call(http.MethodDelete, path, nil, "landlord-a"), http.StatusNoContent)
	if _, sealed, status := e.invitationRow(id); status != "revoked" || sealed != nil {
		t.Errorf("revoked: status %s, sealed %v", status, sealed)
	}
	expectError(t, e.call(http.MethodGet, path+"/link", nil, "landlord-a"), http.StatusNotFound, "invitation.link_unavailable")
	expectError(t, e.call(http.MethodDelete, path, nil, "landlord-a"), http.StatusConflict, "invitation.not_pending")
	expectError(t, e.call(http.MethodPost, path+"/regenerate", nil, "landlord-a"), http.StatusConflict, "invitation.not_pending")

	w = e.call(http.MethodGet, path, nil, "landlord-a")
	if got := decodeData[InvitationView](t, w); got.Status != "revoked" || got.LinkAvailable || got.AnsweredAt == nil {
		t.Errorf("detail = %+v", got)
	}
}

func TestAnExpiredInvitationReadsExpiredAndCanGetANewLink(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), nil)
	id := made.Invitation.ID
	path := "/v1/contract-invitations/" + id.String()
	if _, err := e.pool.Exec(context.Background(), `UPDATE contract_invitations SET expires_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	got := decodeData[InvitationView](t, e.call(http.MethodGet, path, nil, "landlord-a"))
	if got.Status != "pending" || !got.Expired || got.LinkAvailable {
		t.Errorf("expired: %+v", got)
	}
	expectError(t, e.call(http.MethodGet, path+"/link", nil, "landlord-a"), http.StatusNotFound, "invitation.link_unavailable")
	w := e.call(http.MethodPost, path+"/regenerate", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if fresh := decodeData[contracthttp.RenewedInvitationView](t, w); fresh.Invitation.Expired || !fresh.Invitation.LinkAvailable {
		t.Errorf("renewed: %+v", fresh.Invitation)
	}
}

func TestInvitationListFiltersByStatusAndPages(t *testing.T) {
	e := newContractEnv(t, nil)
	first := e.offer(e.room(e.landlA, "P101"), nil).Invitation.ID
	e.offer(e.room(e.landlA, "P102"), nil)
	expectStatus(t, e.invite("landlord-b", map[string]any{"room_id": e.room(e.landlB, "Q1"), "starts_on": dateStr(today())}).ResponseRecorder, http.StatusCreated)
	expectStatus(t, e.call(http.MethodDelete, "/v1/contract-invitations/"+first.String(), nil, "landlord-a"), http.StatusNoContent)

	type page struct {
		Data []InvitationView `json:"data"`
		Meta struct {
			Total   int `json:"total"`
			Page    int `json:"page"`
			PerPage int `json:"per_page"`
		} `json:"meta"`
	}
	read := func(query, bearer string) page {
		var p page
		w := e.call(http.MethodGet, "/v1/contract-invitations"+query, nil, bearer)
		expectStatus(t, w, http.StatusOK)
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if all := read("", "landlord-a"); all.Meta.Total != 2 || len(all.Data) != 2 || all.Data[0].Status != "pending" || all.Data[1].ID != first {
		t.Errorf("open ones first: %+v", all)
	}
	if p := read("?status=revoked", "landlord-a"); p.Meta.Total != 1 || p.Data[0].ID != first {
		t.Errorf("revoked: %+v", p)
	}
	if p := read("?per_page=1&page=2", "landlord-a"); p.Meta.Total != 2 || len(p.Data) != 1 || p.Data[0].ID != first || p.Meta.Page != 2 {
		t.Errorf("second page: %+v", p)
	}
	if p := read("", "landlord-b"); p.Meta.Total != 1 || len(p.Data) != 1 || p.Data[0].Room.Name != "Q1" {
		t.Errorf("another landlord sees only their own: %+v", p)
	}
	w := e.call(http.MethodGet, "/v1/contract-invitations?status=expired", nil, "landlord-a")
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
}

func TestInvitationRoutesCountPerAccount(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), nil)
	path := "/v1/contract-invitations/" + made.Invitation.ID.String()
	e.call(http.MethodGet, path+"/link", nil, "landlord-a")
	e.call(http.MethodPost, path+"/regenerate", nil, "landlord-a")
	for _, key := range []string{"contract-invitation-create:u:" + e.landlA.String(), "contract-invitation-link:u:" + e.landlA.String()} {
		if !e.limiter.seen(key) {
			t.Errorf("the limiter never counted %q", key)
		}
	}
}
