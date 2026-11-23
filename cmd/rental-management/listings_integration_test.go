//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/testenv"
)

// The listing bridges are tested through the real composition code: a listing must hold its room against deletion, a
// change to the room must take the listing off find rooms, find rooms must rank by plan, and a registration needs
// an approved listing.

// publicRooms lists the names of the rooms the guest search shows (a query string may narrow or order it).
func (e *bridgeEnv) publicRooms(query string) []string {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/public/rooms"+query, nil, "tenant")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	var body struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		e.t.Fatal(err)
	}
	names := make([]string, len(body.Data))
	for i, r := range body.Data {
		names[i] = r.Name
	}
	return names
}

func (e *bridgeEnv) listingStatus(id uuid.UUID) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(e.t.Context(), `SELECT status FROM listings WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestAnOpenListingBlocksDeletingTheRoom(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	created := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room.ID, "title": "T"}, "landlord")
	l := testenv.DecodeData[listingRow](t, created)
	del := func() *httptest.ResponseRecorder {
		return e.call(http.MethodDelete, "/v1/rooms/"+room.ID.String(), nil, "landlord")
	}

	// A draft holds nothing; a waiting and a live listing do.
	submit := func() {
		testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/my/listings/"+l.ID.String()+"/submit", nil, "landlord"), http.StatusOK)
	}
	submit()
	testenv.ExpectError(t, del(), http.StatusConflict, apperr.CodeRoomHasListing)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/listings/"+l.ID.String()+"/approve", nil, "admin"), http.StatusOK)
	testenv.ExpectError(t, del(), http.StatusConflict, apperr.CodeRoomHasListing)

	// The bulk route reports the room with the code.
	w := e.call(http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": []uuid.UUID{room.ID}}, "landlord")
	if !strings.Contains(w.Body.String(), apperr.CodeRoomHasListing) {
		t.Errorf("bulk delete = %s", w.Body.String())
	}

	// Taken down, the listing no longer holds the room.
	e.call(http.MethodPost, "/v1/my/listings/"+l.ID.String()+"/unpublish", nil, "landlord")
	testenv.ExpectStatus(t, del(), http.StatusNoContent)
}

func TestEditingARoomSendsItsLiveListingBackToDraftAndOffFindRooms(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	l := e.publish("landlord", room.ID)
	if got := e.publicRooms(""); len(got) != 1 || got[0] != "R1" {
		t.Fatalf("the room is on find rooms: %v", got)
	}

	// A change of the status only leaves the listing alone.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/rooms/"+room.ID.String(), map[string]any{"name": "R1", "address_detail": "12 Le Loi", "price_vnd": 2_000_000, "status": "available"}, "landlord"), http.StatusOK)
	if e.listingStatus(l.ID) != "approved" {
		t.Fatal("saving the room unchanged leaves its listing approved")
	}

	// A new rent is a change of what the listing shows.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/rooms/"+room.ID.String(), map[string]any{"name": "R1", "address_detail": "12 Le Loi", "price_vnd": 9_000_000}, "landlord"), http.StatusOK)
	if got := e.listingStatus(l.ID); got != "draft" {
		t.Fatalf("listing = %s, want draft", got)
	}
	if got := e.publicRooms(""); len(got) != 0 {
		t.Errorf("a draft is not on find rooms: %v", got)
	}
	// The landlord sends it again and an admin approves it: the room is back.
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/my/listings/"+l.ID.String()+"/submit", nil, "landlord"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/listings/"+l.ID.String()+"/approve", nil, "admin"), http.StatusOK)
	if got := e.publicRooms(""); len(got) != 1 {
		t.Errorf("the room is back: %v", got)
	}

	// A waiting listing goes back to a draft too.
	pending := e.newRoom("R2", "available")
	created := testenv.DecodeData[listingRow](t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": pending.ID, "title": "T"}, "landlord"))
	e.call(http.MethodPost, "/v1/my/listings/"+created.ID.String()+"/submit", nil, "landlord")
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/rooms/"+pending.ID.String(), map[string]any{"name": "R2", "address_detail": "12 Le Loi", "price_vnd": 1_000_000}, "landlord"), http.StatusOK)
	if got := e.listingStatus(created.ID); got != "draft" {
		t.Errorf("a waiting listing = %s, want draft", got)
	}
}

func TestARegistrationNeedsAnApprovedListing(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	register := func() *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")}, "tenant")
	}

	testenv.ExpectError(t, register(), http.StatusConflict, apperr.CodeContractNotListed)
	// A listing that only waits is not enough.
	created := testenv.DecodeData[listingRow](t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room.ID, "title": "T"}, "landlord"))
	e.call(http.MethodPost, "/v1/my/listings/"+created.ID.String()+"/submit", nil, "landlord")
	testenv.ExpectError(t, register(), http.StatusConflict, apperr.CodeContractNotListed)
	// Approved: the registration goes through.
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/admin/listings/"+created.ID.String()+"/approve", nil, "admin"), http.StatusOK)
	testenv.ExpectStatus(t, register(), http.StatusCreated)
	// Taken down again, nobody else can register.
	e.call(http.MethodPost, "/v1/admin/listings/"+created.ID.String()+"/unpublish", map[string]any{"note": "wrong pictures"}, "admin")
	if _, err := e.pool.Exec(t.Context(), `UPDATE contracts SET status = 'rejected'`); err != nil {
		t.Fatal(err)
	}
	testenv.ExpectError(t, register(), http.StatusConflict, apperr.CodeContractNotListed)

	// The blank contract of a room marked rented is unchanged: it needs no listing.
	rented := e.newRoom("R2", "rented")
	if got := e.contractsOf(rented.ID); len(got) != 1 || !got[0].Unassigned {
		t.Errorf("a room born rented has its blank contract: %+v", got)
	}
}

func TestFindRoomsRanksLandlordsByTheSearchPriorityOfTheirPlan(t *testing.T) {
	e := newBridgeEnv(t)
	plan := e.seedPlans()
	// The older room belongs to the free landlord; the newer one to the Pro landlord.
	free := e.newRoomFor("landlord", "Free room", "available")
	e.publish("landlord", free.ID)
	time.Sleep(5 * time.Millisecond)
	pro := e.newRoomFor("landlord2", "Pro room", "available")
	e.publish("landlord2", pro.ID)

	if got := e.publicRooms(""); len(got) != 2 || got[0] != "Pro room" {
		t.Fatalf("newest first without plans: %v", got)
	}
	e.assign(e.landlord, plan("landlord_vip"))
	e.assign(e.landlord2, plan("landlord_pro"))
	if got := e.publicRooms(""); len(got) != 2 || got[0] != "Free room" || got[1] != "Pro room" {
		t.Errorf("VIP (100) before Pro (10): %v", got)
	}
	// An explicit sort ignores the plans.
	if got := e.publicRooms("?sort=created_at"); got[0] != "Free room" {
		t.Errorf("oldest first: %v", got)
	}
	if got := e.publicRooms("?sort=-created_at"); got[0] != "Pro room" {
		t.Errorf("newest first: %v", got)
	}
	e.assign(e.landlord, plan("landlord_free"))
	if got := e.publicRooms(""); got[0] != "Pro room" {
		t.Errorf("back on the free plan the Pro landlord leads: %v", got)
	}
}
