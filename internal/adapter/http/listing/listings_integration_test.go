//go:build integration

package listinghttp_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
)

func TestALandlordWritesSendsAndWithdrawsAListing(t *testing.T) {
	e := newListingEnv(t)
	room := e.room(e.landlA, "P101", 3_000_000)

	// A draft: nobody is told.
	w := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room, "title": "Phòng gần chợ", "description": "Thoáng mát"}, "landlord-a")
	expectStatus(t, w, http.StatusCreated)
	l := decodeData[ListingView](t, w)
	if l.Status != "draft" || l.Title != "Phòng gần chợ" || *l.Description != "Thoáng mát" || l.RoomID != room || l.Room.Name != "P101" ||
		l.Room.PriceVND != 3_000_000 || l.Room.Cover == nil || l.Room.Cover.ThumbURL != "http://files.test/c_t.webp" ||
		l.SubmittedAt != nil || l.Landlord != nil || l.Reviewer != nil {
		t.Fatalf("listing = %+v", l)
	}
	if len(e.notices.kinds()) != 0 {
		t.Error("a draft tells nobody")
	}
	path := "/v1/my/listings/" + l.ID.String()

	// A partial update: only the fields sent change, and null clears the description.
	w = e.call(http.MethodPut, path, map[string]any{"description": nil}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[ListingView](t, w); got.Description != nil || got.Title != "Phòng gần chợ" {
		t.Errorf("after a partial update: %+v", got)
	}
	expectStatus(t, e.call(http.MethodPatch, path, map[string]any{"title": "Tiêu đề mới"}, "landlord-a"), http.StatusOK)

	// Sending it tells the admins (and only once).
	w = e.call(http.MethodPost, path+"/submit", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[ListingView](t, w); got.Status != "pending" || got.SubmittedAt == nil {
		t.Fatalf("submitted = %+v", got)
	}
	if kinds := e.notices.kinds(); len(kinds) != 1 || kinds[0] != "listing_submitted" || !e.notices.sent[0].ToAdmins || e.notices.sent[0].Params["room"] != "P101" {
		t.Errorf("notices = %v", e.notices.sent)
	}
	expectError(t, e.call(http.MethodPost, path+"/submit", nil, "landlord-a"), http.StatusConflict, apperr.CodeListingTransition)

	// A waiting listing can be deleted (withdrawn); a live one has to be taken down first.
	expectStatus(t, e.call(http.MethodDelete, path, nil, "landlord-a"), http.StatusNoContent)
	expectError(t, e.call(http.MethodGet, path, nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)

	live := e.listed(room, e.landlA, "Live", "approved")
	livePath := "/v1/my/listings/" + live.String()
	expectError(t, e.call(http.MethodDelete, livePath, nil, "landlord-a"), http.StatusConflict, apperr.CodeListingUnpublish)
	w = e.call(http.MethodPost, livePath+"/unpublish", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[ListingView](t, w); got.Status != "unpublished" || got.UnpublishedAt == nil {
		t.Errorf("unpublished = %+v", got)
	}
	expectError(t, e.call(http.MethodPost, livePath+"/unpublish", nil, "landlord-a"), http.StatusConflict, apperr.CodeListingTransition)
	expectStatus(t, e.call(http.MethodDelete, livePath, nil, "landlord-a"), http.StatusNoContent)
}

func TestEditingAWaitingOrLiveListingReturnsItToDraft(t *testing.T) {
	for _, status := range []string{"pending", "approved"} {
		e := newListingEnv(t)
		room := e.room(e.landlA, "P101", 1)
		id := e.listed(room, e.landlA, "Old", status)
		path := "/v1/my/listings/" + id.String()

		// Saving without a change leaves it alone.
		expectStatus(t, e.call(http.MethodPut, path, map[string]any{"title": "Old"}, "landlord-a"), http.StatusOK)
		if e.status(id) != status {
			t.Fatalf("%s: unchanged save moved it to %s", status, e.status(id))
		}
		w := e.call(http.MethodPut, path, map[string]any{"title": "New"}, "landlord-a")
		expectStatus(t, w, http.StatusOK)
		if got := decodeData[ListingView](t, w); got.Status != "draft" || got.SubmittedAt != nil || got.Title != "New" {
			t.Errorf("%s: %+v", status, got)
		}
		if len(e.notices.kinds()) != 0 {
			t.Errorf("%s: the admins are not told", status)
		}
	}
	// A refused listing keeps its status when it is fixed, and is sent again.
	e := newListingEnv(t)
	id := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "Old", "rejected")
	path := "/v1/my/listings/" + id.String()
	expectStatus(t, e.call(http.MethodPut, path, map[string]any{"title": "Fixed"}, "landlord-a"), http.StatusOK)
	if e.status(id) != "rejected" {
		t.Error("a refused listing stays refused until it is sent again")
	}
	expectStatus(t, e.call(http.MethodPost, path+"/submit", nil, "landlord-a"), http.StatusOK)
}

func TestOneOpenListingPerRoomAndOnlyOwnLiveRooms(t *testing.T) {
	e := newListingEnv(t)
	room := e.room(e.landlA, "P101", 1)
	e.listed(room, e.landlA, "Open", "pending")

	w := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room, "title": "Second"}, "landlord-a")
	body := expectError(t, w, http.StatusConflict, apperr.CodeListingRoomListed)
	if !strings.Contains(body.Error.Message, "P101") {
		t.Errorf("the message names the room: %q", body.Error.Message)
	}
	// Another landlord's room and an unknown room look the same.
	other := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room, "title": "x"}, "landlord-b")
	expectError(t, other, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if p := listOf[ListingView](t, e.call(http.MethodGet, "/v1/my/listings", nil, "landlord-b")); len(p.Data) != 0 || p.Meta.Total != 0 {
		t.Errorf("another landlord's list is empty: %+v", p)
	}
	unknown := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": uuid.New(), "title": "x"}, "landlord-a")
	if f := expectError(t, unknown, http.StatusUnprocessableEntity, apperr.CodeValidationFailed).Error.Fields["room_id"]; len(f) != 1 {
		t.Errorf("fields = %v", f)
	}
	// A deleted room cannot be listed.
	gone := e.room(e.landlA, "P102", 1)
	e.exec(`UPDATE rooms SET deleted_at = now() WHERE id = $1`, gone)
	expectError(t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": gone, "title": "x"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	// Sending a draft for a room that is held by another open listing is refused too.
	draft := e.listed(room, e.landlA, "Draft", "draft")
	expectError(t, e.call(http.MethodPost, "/v1/my/listings/"+draft.String()+"/submit", nil, "landlord-a"), http.StatusConflict, apperr.CodeListingRoomListed)
}

func TestCreateAndUpdateValidation(t *testing.T) {
	e := newListingEnv(t)
	room := e.room(e.landlA, "P101", 1)
	id := e.listed(e.room(e.landlA, "P102", 1), e.landlA, "T", "draft")

	cases := map[string]struct {
		body   map[string]any
		fields []string
	}{
		"nothing":        {map[string]any{}, []string{"room_id", "title"}},
		"a long title":   {map[string]any{"room_id": room, "title": strings.Repeat("a", 151)}, []string{"title"}},
		"long text":      {map[string]any{"room_id": room, "title": "t", "description": strings.Repeat("a", 5001)}, []string{"description"}},
		"a bad room id":  {map[string]any{"room_id": "nope", "title": "t"}, nil},
		"an empty title": {map[string]any{"room_id": room, "title": ""}, []string{"title"}},
	}
	for name, c := range cases {
		w := e.call(http.MethodPost, "/v1/my/listings", c.body, "landlord-a")
		if c.fields == nil {
			expectError(t, w, http.StatusBadRequest, apperr.CodeRequestMalformed)
			continue
		}
		body := expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		for _, f := range c.fields {
			if len(body.Error.Fields[f]) == 0 {
				t.Errorf("%s: no error on %s: %v", name, f, body.Error.Fields)
			}
		}
	}
	update := func(body map[string]any) {
		t.Helper()
		expectError(t, e.call(http.MethodPut, "/v1/my/listings/"+id.String(), body, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
	update(map[string]any{"title": ""})
	update(map[string]any{"title": strings.Repeat("a", 151)})
	update(map[string]any{"description": strings.Repeat("a", 5001)})
	// The room cannot be changed: the field is ignored.
	other := e.room(e.landlA, "P103", 1)
	expectStatus(t, e.call(http.MethodPut, "/v1/my/listings/"+id.String(), map[string]any{"room_id": other, "title": "T"}, "landlord-a"), http.StatusOK)
	var roomID uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT room_id FROM listings WHERE id = $1`, id).Scan(&roomID); err != nil || roomID == other {
		t.Errorf("the room of a listing never changes: %v %v", roomID, err)
	}
}

func TestOwnListsAreScopedFilteredPagedAndCounted(t *testing.T) {
	e := newListingEnv(t)
	cheap := e.listed(e.room(e.landlA, "P101", 1_000_000), e.landlA, "Gần chợ", "approved")
	dear := e.listed(e.room(e.landlA, "P102", 5_000_000), e.landlA, "Có ban công", "pending")
	draft := e.listed(e.room(e.landlA, "P103", 3_000_000), e.landlA, "Nháp", "draft")
	e.listed(e.room(e.landlB, "X1", 1), e.landlB, "Other's", "approved")
	ids := func(query string) []uuid.UUID {
		t.Helper()
		p := listOf[ListingView](t, e.call(http.MethodGet, "/v1/my/listings"+query, nil, "landlord-a"))
		out := make([]uuid.UUID, len(p.Data))
		for i, l := range p.Data {
			out[i] = l.ID
		}
		return out
	}
	same := func(name string, got []uuid.UUID, want ...uuid.UUID) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", name, got, want)
				return
			}
		}
	}
	same("newest first, only mine", ids(""), draft, dear, cheap)
	same("a status", ids("?status=pending"), dear)
	same("rent high to low", ids("?sort=-price"), dear, draft, cheap)
	same("text", ids("?search=ban%20c%C3%B4ng"), dear)
	same("rent range", ids("?price_from=2000000&price_to=4000000"), draft)
	same("oldest first", ids("?sort=created_at"), cheap, dear, draft)

	p := listOf[ListingView](t, e.call(http.MethodGet, "/v1/my/listings?per_page=2&page=2", nil, "landlord-a"))
	if p.Meta.Page != 2 || p.Meta.PerPage != 2 || p.Meta.Total != 3 || len(p.Data) != 1 {
		t.Errorf("meta = %+v", p.Meta)
	}
	if p.Meta.Counts["approved"] != 1 || p.Meta.Counts["pending"] != 1 || p.Meta.Counts["draft"] != 1 || len(p.Meta.Counts) != 3 {
		t.Errorf("counts = %v", p.Meta.Counts)
	}
	// Bad values are reported together.
	body := expectError(t, e.call(http.MethodGet, "/v1/my/listings?status=bogus&sort=sideways&price_from=9&price_to=1&per_page=0", nil, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	for _, f := range []string{"per_page"} {
		if len(body.Error.Fields[f]) == 0 {
			t.Errorf("no error on %s: %v", f, body.Error.Fields)
		}
	}
	body = expectError(t, e.call(http.MethodGet, "/v1/my/listings?status=bogus&sort=sideways&price_from=9&price_to=1", nil, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	for _, f := range []string{"status", "sort", "price_from"} {
		if len(body.Error.Fields[f]) == 0 {
			t.Errorf("no error on %s: %v", f, body.Error.Fields)
		}
	}
	// The detail has the room's costs and pictures.
	e.exec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) SELECT room_id, 'Điện', 'electricity', 3500, 'per_kwh', 1 FROM listings WHERE id = $1`, cheap)
	d := decodeData[ListingDetailView](t, e.call(http.MethodGet, "/v1/my/listings/"+cheap.String(), nil, "landlord-a"))
	if len(d.Room.Charges) != 1 || d.Room.Charges[0].Name != "Điện" || d.Room.Pictures == nil {
		t.Errorf("detail room = %+v", d.Room)
	}
}

func TestListingsAreTheLandlordsOwn(t *testing.T) {
	e := newListingEnv(t)
	id := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "T", "draft")
	path := "/v1/my/listings/" + id.String()

	// Another landlord sees nothing and can do nothing: 404, never 403.
	for _, c := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodPut, ""}, {http.MethodDelete, ""}, {http.MethodPost, "/submit"}, {http.MethodPost, "/unpublish"},
	} {
		expectError(t, e.call(c.method, path+c.suffix, map[string]any{"title": "x"}, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	expectError(t, e.call(http.MethodGet, "/v1/my/listings/not-a-uuid", nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
	if e.status(id) != "draft" {
		t.Error("nothing happened to the listing")
	}
	// Roles and permissions.
	expectError(t, e.call(http.MethodGet, "/v1/my/listings", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
	for _, bearer := range []string{"tenant-t", "landlord-a-as-tenant", "admin", "tenant-nothing"} {
		expectError(t, e.call(http.MethodGet, "/v1/my/listings", nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
		expectError(t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"title": "x"}, bearer), http.StatusForbidden, apperr.CodeForbidden)
	}
}

func TestCreatingAndSendingListingsIsRateLimited(t *testing.T) {
	e := newListingEnv(t)
	e.limit.allowed = 2
	room := e.room(e.landlA, "P101", 1)
	// The creations and the submissions have their own counters (30 an hour in production; 2 here).
	for i := 0; i < 2; i++ {
		expectStatus(t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room, "title": "x"}, "landlord-a"), http.StatusCreated)
	}
	expectError(t, e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": room, "title": "x"}, "landlord-a"), http.StatusTooManyRequests, apperr.CodeRequestRateLimited)
	id := e.listed(e.room(e.landlA, "P102", 1), e.landlA, "T", "draft")
	expectStatus(t, e.call(http.MethodPost, "/v1/my/listings/"+id.String()+"/submit", nil, "landlord-a"), http.StatusOK)
}
