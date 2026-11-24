//go:build integration

package contracthttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	contracthttp "rental-management/internal/adapter/http/contract"
	"rental-management/internal/domain/invitation"
	"rental-management/pkg/authz"
)

type offerView = contracthttp.InvitationOfferView

const answerBase = "/v1/contract-invitations"

func (e *contractEnv) answer(action, bearer string, body any) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, answerBase+"/"+action, body, bearer)
}

func tokenBody(link string) map[string]any { return map[string]any{"token": tokenOf(link)} }

func (e *contractEnv) invitationStatus(id uuid.UUID) string {
	e.t.Helper()
	_, _, st := e.invitationRow(id)
	return st
}

func TestAcceptingAPublicLinkMakesTheContractForTheCaller(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	made := e.offer(room, map[string]any{"deposit_vnd": 1_000_000, "occupant_count": 2})
	body := tokenBody(made.Link)

	w := e.answer("preview", "tenant-t", body)
	expectStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "recipient") || strings.Contains(w.Body.String(), "t@example.vn") {
		t.Errorf("a preview says nothing of the recipients: %s", w.Body.String())
	}
	offer := decodeData[offerView](t, w)
	if offer.ID != made.Invitation.ID || offer.Room.Name != "P101" || offer.Room.ID != room || offer.Landlord.ID != e.landlA ||
		offer.Landlord.FullName != "Landlord A" || offer.PriceVND != 3_000_000 || offer.OccupantCount != 2 || offer.Room.Cover == nil ||
		offer.Room.Ward == nil || offer.Room.AddressDetail != "12 Le Loi" {
		t.Errorf("offer = %+v", offer)
	}
	if e.invitationStatus(made.Invitation.ID) != "pending" {
		t.Error("a preview changes nothing")
	}

	e.notices.sent = nil
	w = e.answer("accept", "tenant-t", body)
	expectStatus(t, w, http.StatusCreated)
	k := decodeData[ContractView](t, w)
	if k.Status != "active" || k.Tenant == nil || k.Tenant.ID != e.tenantT || k.PriceVND != 3_000_000 || *k.DepositVND != 1_000_000 ||
		k.OccupantCount != 2 || !k.Confirmed || k.Room.ID != room {
		t.Errorf("contract = %+v", k)
	}
	if e.roomStatus(room) != "rented" {
		t.Errorf("room status = %s", e.roomStatus(room))
	}
	_, sealed, status := e.invitationRow(made.Invitation.ID)
	var contractID *uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT contract_id FROM contract_invitations WHERE id = $1`, made.Invitation.ID).Scan(&contractID); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" || sealed != nil || contractID == nil || *contractID != k.ID {
		t.Errorf("invitation: %s sealed=%v contract=%v", status, sealed, contractID)
	}
	if len(e.notices.sent) != 1 || e.notices.sent[0].UserID != e.landlA || e.notices.sent[0].Kind != "contract_invitation_accepted" {
		t.Errorf("only the landlord is told: %+v", e.notices.sent)
	}
	// The landlord sees the contract; the tenant sees it among their own.
	if got := e.list("landlord-a", "?room_id="+room.String()); got.Meta.Total != 1 {
		t.Errorf("landlord sees %d", got.Meta.Total)
	}
	expectStatus(t, e.call(http.MethodGet, "/v1/me/contracts/"+k.ID.String(), nil, "tenant-t"), http.StatusOK)
	if !e.limiter.seen("contract-invitation-answer:u:" + e.tenantT.String()) {
		t.Error("answers are limited per account")
	}
}

func TestEveryBadLinkReadsTheSame(t *testing.T) {
	e := newContractEnv(t, nil)
	used := e.offer(e.room(e.landlA, "P101"), nil)
	withdrawn := e.offer(e.room(e.landlA, "P102"), nil)
	expiring := e.offer(e.room(e.landlA, "P103"), nil)
	expectStatus(t, e.answer("accept", "tenant-t", tokenBody(used.Link)), http.StatusCreated)
	expectStatus(t, e.call(http.MethodDelete, answerBase+"/"+withdrawn.Invitation.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	// Expiry is read from the clock: move it past the end of the offer.
	e.clock.advance(8 * 24 * time.Hour)

	bodies := []map[string]any{
		tokenBody(used.Link), tokenBody(withdrawn.Link), tokenBody(expiring.Link),
		{"token": strings.Repeat("a", 64)}, {"token": "short"}, {"token": strings.Repeat("a", 5000)},
		{"invitation_id": "not-a-uuid"}, {"invitation_id": uuid.New().String()},
		{"invitation_id": used.Invitation.ID.String()},
	}
	var first string
	for i, body := range bodies {
		for _, action := range []string{"preview", "accept", "decline"} {
			w := e.answer(action, "tenant-u", body)
			expectError(t, w, http.StatusNotFound, "invitation.invalid")
			if i == 0 && action == "preview" {
				first = w.Body.String()
			}
			if w.Body.String() != first {
				t.Errorf("body %d %s differs: %s vs %s", i, action, w.Body.String(), first)
			}
		}
	}
	if e.invitationStatus(expiring.Invitation.ID) != "pending" {
		t.Error("an expired offer is left as it is")
	}
}

func TestAnAnswerNeedsATokenOrAnId(t *testing.T) {
	e := newContractEnv(t, nil)
	for _, body := range []any{map[string]any{}, map[string]any{"token": ""}, `{"token": null}`} {
		w := &testResponse{e.answer("preview", "tenant-t", body)}
		expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
		if len(w.fields()["token"]) != 1 {
			t.Errorf("fields = %v", w.fields())
		}
	}
}

func TestARefusedAcceptLeavesTheInvitationOpen(t *testing.T) {
	one := 1
	e := newContractEnv(t, fixedPlans{contracts: &one})
	ctx := context.Background()

	// The room is taken meanwhile: another tenant registers and the landlord approves.
	taken := e.room(e.landlA, "P101")
	offer := e.offer(taken, nil)
	e.approve("landlord-a", e.register("tenant-u", taken, nil).ID)
	expectError(t, e.answer("accept", "tenant-t", tokenBody(offer.Link)), http.StatusConflict, "contract.room_occupied")
	if e.invitationStatus(offer.Invitation.ID) != "pending" {
		t.Error("room taken: the offer stays open")
	}

	// The landlord's plan allows one running contract and one is running now.
	free := e.room(e.landlA, "P102")
	limited := e.offer(free, nil)
	expectError(t, e.answer("accept", "tenant-t", tokenBody(limited.Link)), http.StatusConflict, "plan.contract_limit_reached")
	if e.invitationStatus(limited.Invitation.ID) != "pending" || e.roomStatus(free) != "available" {
		t.Error("plan limit: the offer stays open and the room stays free")
	}

	// A stay shorter than the room's minimum period (the terms were edited after the offer was made).
	short := e.room(e.landlB, "P201")
	if _, err := e.pool.Exec(ctx, `UPDATE rooms SET minimum_rental_period = 3 WHERE id = $1`, short); err != nil {
		t.Fatal(err)
	}
	ends := dateStr(today().AddDate(0, 4, 0))
	min := decodeInvite(t, e, "landlord-b", map[string]any{"room_id": short, "starts_on": dateStr(today()), "ends_on": ends})
	if _, err := e.pool.Exec(ctx, `UPDATE contract_invitations SET ends_on = starts_on + 10 WHERE id = $1`, min.Invitation.ID); err != nil {
		t.Fatal(err)
	}
	w := &testResponse{e.answer("accept", "tenant-t", tokenBody(min.Link))}
	expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, "validation.failed")
	if len(w.fields()["ends_on"]) != 1 || e.invitationStatus(min.Invitation.ID) != "pending" {
		t.Errorf("minimum period: fields %v", w.fields())
	}
}

func decodeInvite(t *testing.T, e *contractEnv, bearer string, body map[string]any) issuedView {
	t.Helper()
	w := e.invite(bearer, body)
	expectStatus(t, w.ResponseRecorder, http.StatusCreated)
	return decodeData[issuedView](t, w.ResponseRecorder)
}

func TestNobodyAcceptsTheirOwnOffer(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	made := e.offer(room, nil)
	expectError(t, e.answer("accept", "landlord-a-as-tenant", tokenBody(made.Link)), http.StatusConflict, "invitation.own_offer")
	if e.invitationStatus(made.Invitation.ID) != "pending" || len(e.list("landlord-a", "").Data) != 0 {
		t.Error("nothing was made")
	}
}

func TestAnOfferForAPendingContractMakesAWaitingRegistration(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	first := e.offer(room, map[string]any{"contract_status": "pending"})
	second := e.offer(room, map[string]any{"contract_status": "pending"})
	w := e.answer("accept", "tenant-t", tokenBody(first.Link))
	expectStatus(t, w, http.StatusCreated)
	if k := decodeData[ContractView](t, w); k.Status != "pending" || e.roomStatus(room) != "available" {
		t.Errorf("contract = %+v", k)
	}
	expectError(t, e.answer("accept", "tenant-t", tokenBody(second.Link)), http.StatusConflict, "contract.already_registered")
	if e.invitationStatus(second.Invitation.ID) != "pending" {
		t.Error("the refused offer stays open")
	}
	// Another tenant can still take the second offer.
	expectStatus(t, e.answer("accept", "tenant-u", tokenBody(second.Link)), http.StatusCreated)
}

func TestDecliningTellsTheLandlordAndClosesTheOffer(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), nil)
	e.notices.sent = nil
	expectStatus(t, e.answer("decline", "tenant-t", tokenBody(made.Link)), http.StatusNoContent)
	if e.invitationStatus(made.Invitation.ID) != "declined" {
		t.Error("declined")
	}
	if len(e.notices.sent) != 1 || e.notices.sent[0].UserID != e.landlA || e.notices.sent[0].Kind != "contract_invitation_declined" {
		t.Errorf("notifications = %+v", e.notices.sent)
	}
	expectError(t, e.answer("accept", "tenant-t", tokenBody(made.Link)), http.StatusNotFound, "invitation.invalid")
}

func TestARegeneratedLinkReplacesTheOldOne(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), nil)
	w := e.call(http.MethodPost, answerBase+"/"+made.Invitation.ID.String()+"/regenerate", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	fresh := decodeData[contracthttp.RenewedInvitationView](t, w)
	expectError(t, e.answer("preview", "tenant-t", tokenBody(made.Link)), http.StatusNotFound, "invitation.invalid")
	expectStatus(t, e.answer("preview", "tenant-t", tokenBody(fresh.Link)), http.StatusOK)
}

func TestOnlyTheAudienceAnswers(t *testing.T) {
	e := newContractEnv(t, nil)
	named := e.offer(e.room(e.landlA, "P101"), map[string]any{"recipient_id": e.tenantT})
	byID := map[string]any{"invitation_id": named.Invitation.ID}
	for _, body := range []map[string]any{byID, tokenBody(named.Link)} {
		expectError(t, e.answer("preview", "tenant-u", body), http.StatusNotFound, "invitation.invalid")
		expectError(t, e.answer("accept", "tenant-u", body), http.StatusNotFound, "invitation.invalid")
	}
	expectStatus(t, e.answer("preview", "tenant-t", byID), http.StatusOK)
	if got := decodeData[offerView](t, e.answer("preview", "tenant-t", tokenBody(named.Link))); got.ID != named.Invitation.ID {
		t.Errorf("offer = %+v", got)
	}
	// By id the named tenant accepts without ever holding the link.
	expectStatus(t, e.answer("accept", "tenant-t", byID), http.StatusCreated)

	// An offer to an address: only a verified holder of exactly that address.
	holder := e.offer(e.room(e.landlA, "P102"), map[string]any{"email": "holder@example.vn"})
	if holder.Invitation.Audience != "email" {
		t.Fatalf("audience = %s", holder.Invitation.Audience)
	}
	expectError(t, e.answer("preview", "tenant-u", tokenBody(holder.Link)), http.StatusNotFound, "invitation.invalid")
	person := func(verified bool) invitation.Person {
		mail := "Holder@Example.vn"
		return invitation.Person{ID: e.tenantU, FullName: "Tenant U", Email: &mail, EmailVerified: verified, Active: true, AcceptsInvitations: true, Roles: []string{authz.RoleTenant}}
	}
	e.invitees.byID[e.tenantU] = person(false)
	expectError(t, e.answer("preview", "tenant-u", tokenBody(holder.Link)), http.StatusNotFound, "invitation.invalid")
	e.invitees.byID[e.tenantU] = person(true)
	expectStatus(t, e.answer("accept", "tenant-u", tokenBody(holder.Link)), http.StatusCreated)
}

func TestTheInboxListsOnlyTheCallersOpenNamedOffers(t *testing.T) {
	e := newContractEnv(t, nil)
	open := e.offer(e.room(e.landlA, "P101"), map[string]any{"recipient_id": e.tenantT})
	declined := e.offer(e.room(e.landlA, "P102"), map[string]any{"recipient_id": e.tenantT})
	late := e.offer(e.room(e.landlA, "P103"), map[string]any{"recipient_id": e.tenantT})
	e.offer(e.room(e.landlA, "P104"), map[string]any{"recipient_id": e.tenantU})
	e.offer(e.room(e.landlA, "P105"), nil)
	expectStatus(t, e.answer("decline", "tenant-t", map[string]any{"invitation_id": declined.Invitation.ID}), http.StatusNoContent)
	if _, err := e.pool.Exec(context.Background(), `UPDATE contract_invitations SET expires_at = now() - interval '1 hour' WHERE id = $1`, late.Invitation.ID); err != nil {
		t.Fatal(err)
	}

	type inbox struct {
		Data []offerView `json:"data"`
		Meta struct {
			Page, PerPage, Total int
		} `json:"meta"`
	}
	read := func(bearer, query string) inbox {
		w := e.call(http.MethodGet, "/v1/me/contract-invitations"+query, nil, bearer)
		expectStatus(t, w, http.StatusOK)
		var out inbox
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := read("tenant-t", "")
	if got.Meta.Total != 1 || len(got.Data) != 1 || got.Data[0].ID != open.Invitation.ID || got.Data[0].Landlord.FullName != "Landlord A" {
		t.Errorf("inbox = %+v", got)
	}
	if other := read("tenant-u", ""); other.Meta.Total != 1 {
		t.Errorf("the other tenant has their own: %d", other.Meta.Total)
	}
	// Paginated: a page of nothing still reports the total.
	if page := read("tenant-t", "?per_page=1&page=2"); page.Meta.Total != 1 || len(page.Data) != 0 {
		t.Errorf("page 2 = %+v", page)
	}
	expectError(t, e.call(http.MethodGet, "/v1/me/contract-invitations?per_page=0", nil, "tenant-t"), http.StatusUnprocessableEntity, "validation.failed")
}

func TestTheAnswerRoutesAreForTenantsOnly(t *testing.T) {
	e := newContractEnv(t, nil)
	made := e.offer(e.room(e.landlA, "P101"), map[string]any{"recipient_id": e.tenantT})
	body := tokenBody(made.Link)
	for _, bearer := range []string{"landlord-a", "admin", "tenant-nothing"} {
		for _, action := range []string{"preview", "accept", "decline"} {
			if w := e.answer(action, bearer, body); w.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d", bearer, action, w.Code)
			}
		}
		if w := e.call(http.MethodGet, "/v1/me/contract-invitations", nil, bearer); w.Code != http.StatusForbidden {
			t.Errorf("%s inbox = %d", bearer, w.Code)
		}
	}
	for _, action := range []string{"preview", "accept", "decline"} {
		if w := e.answer(action, "", body); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d", action, w.Code)
		}
	}
	if e.invitationStatus(made.Invitation.ID) != "pending" {
		t.Error("refused calls change nothing")
	}
}
