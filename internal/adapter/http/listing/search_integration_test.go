//go:build integration

package listinghttp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
)

// offer puts a room of the landlord on find rooms: an available room with an approved listing.
func (e *listingEnv) offer(landlord uuid.UUID, name string, price int64) uuid.UUID {
	e.t.Helper()
	room := e.room(landlord, name, price)
	e.listed(room, landlord, "Bright "+name, "approved")
	return room
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = name(it)
	}
	return out
}

func TestARenterSearchesTheRoomsOnFindRooms(t *testing.T) {
	e := newListingEnv(t)
	cheap := e.offer(e.landlA, "Cheap", 1_000_000)
	e.offer(e.landlB, "Dear", 5_000_000)
	// None of these are offered: no listing, a waiting listing, a rented room, a deleted room.
	e.room(e.landlA, "Unlisted", 1)
	e.listed(e.room(e.landlA, "Waiting", 1), e.landlA, "W", "pending")
	rented := e.offer(e.landlA, "Rented", 1)
	e.exec(`UPDATE rooms SET status = 'rented' WHERE id = $1`, rented)
	gone := e.offer(e.landlA, "Gone", 1)
	e.exec(`UPDATE rooms SET deleted_at = now() WHERE id = $1`, gone)

	list := func(query string) []string {
		t.Helper()
		p := listOf[OfferView](t, e.call(http.MethodGet, "/v1/vacant-rooms"+query, nil, "tenant-t"))
		return names(p.Data, func(o OfferView) string { return o.Name })
	}
	if got := list(""); strings.Join(got, ",") != "Dear,Cheap" {
		t.Errorf("newest first: %v", got)
	}
	if got := list("?sort=price"); strings.Join(got, ",") != "Cheap,Dear" {
		t.Errorf("price: %v", got)
	}
	if got := list("?price_from=2000000"); strings.Join(got, ",") != "Dear" {
		t.Errorf("price range: %v", got)
	}
	if got := list("?landlord_id=" + e.landlA.String()); strings.Join(got, ",") != "Cheap" {
		t.Errorf("landlord: %v", got)
	}
	if got := list("?ward_code=5"); len(got) != 0 {
		t.Errorf("ward: %v", got)
	}
	// The house is searched by name: a room outside any building goes by its landlord's name.
	if got := list("?house_name=chu%20lan"); strings.Join(got, ",") != "Dear" {
		t.Errorf("house name: %v", got)
	}
	if got := list("?house_name=cheap"); len(got) != 0 {
		t.Errorf("the room's own name is not searched: %v", got)
	}

	p := listOf[OfferView](t, e.call(http.MethodGet, "/v1/vacant-rooms?per_page=1&page=2", nil, "tenant-t"))
	if p.Meta.Page != 2 || p.Meta.PerPage != 1 || p.Meta.Total != 2 || len(p.Data) != 1 || p.Data[0].Name != "Cheap" {
		t.Errorf("meta = %+v", p.Meta)
	}
	o := p.Data[0]
	if o.AddressDetail != secretAddress+", flat 4" || o.Listing.Title != "Bright Cheap" || o.Landlord.FullName != "Chu Hoa" || o.HouseName != "Chu Hoa" ||
		o.Ward == nil || o.Ward.Province.Name != "Ha Noi" || o.Cover == nil || o.PriceVND != 1_000_000 {
		t.Errorf("a renter sees the address: %+v", o)
	}

	// The detail has the costs and pictures; a room that is not offered is a 404.
	e.exec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Điện', 'electricity', 3500, 'per_kwh', 1)`, cheap)
	w := e.call(http.MethodGet, "/v1/vacant-rooms/"+cheap.String(), nil, "tenant-t")
	expectStatus(t, w, http.StatusOK)
	d := decodeData[struct {
		OfferView
		Charges  []struct{ Name string } `json:"charges"`
		Pictures []any                   `json:"pictures"`
	}](t, w)
	if d.Name != "Cheap" || len(d.Charges) != 1 || d.Charges[0].Name != "Điện" || d.Pictures == nil {
		t.Errorf("detail = %+v", d)
	}
	for _, id := range []uuid.UUID{rented, gone} {
		expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms/"+id.String(), nil, "tenant-t"), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms/nope", nil, "tenant-t"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestTheSearchValidatesItsFilters(t *testing.T) {
	e := newListingEnv(t)
	body := expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms?landlord_id=x&price_from=9&price_to=1&area_from=5&area_to=2&sort=cheapest&ward_code=abc", nil, "tenant-t"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	for _, f := range []string{"landlord_id", "price_from", "area_from", "sort", "ward_code"} {
		if len(body.Error.Fields[f]) == 0 {
			t.Errorf("no error on %s: %v", f, body.Error.Fields)
		}
	}
	expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms?house_name="+strings.Repeat("a", 256), nil, "tenant-t"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodGet, "/v1/public/rooms?per_page=1000", nil, ""), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestOnlyRentersUseTheSignedInSearch(t *testing.T) {
	e := newListingEnv(t)
	e.offer(e.landlA, "Cheap", 1)
	expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
	// A landlord acting as a landlord, an admin and an account without the permission are refused.
	for _, bearer := range []string{"landlord-a", "admin", "tenant-nothing"} {
		expectError(t, e.call(http.MethodGet, "/v1/vacant-rooms", nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
		expectError(t, e.call(http.MethodGet, "/v1/landlords/"+e.landlA.String(), nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
	}
	// A landlord acting as a tenant searches like any tenant.
	expectStatus(t, e.call(http.MethodGet, "/v1/vacant-rooms", nil, "landlord-a-as-tenant"), http.StatusOK)
}

// everyPrivateThing are the strings that must never appear in a public answer.
func everyPrivateThing() []string {
	return []string{
		secretEmail, secretPhone, secretAddress, tenantEmail, "@example.vn", "address_detail", `"email"`, `"phone`, "contract",
		"my_registration", "tenant",
	}
}

func assertNothingPrivate(t *testing.T, name string, w *httptest.ResponseRecorder) {
	t.Helper()
	body := w.Body.String()
	for _, secret := range everyPrivateThing() {
		if strings.Contains(body, secret) {
			t.Errorf("%s leaks %q: %s", name, secret, body)
		}
	}
}

func TestGuestsBrowseTheRoomsWithoutAnAccountAndSeeOnlyThePublicSide(t *testing.T) {
	e := newListingEnv(t)
	room := e.offer(e.landlA, "A101", 2_000_000)
	// A tenant has a waiting registration on the room: nothing of it may show.
	e.exec(`INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'pending', 1, current_date, now(), 1)`, room, e.landlA, e.tenantT)
	e.exec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Điện', 'electricity', 3500, 'per_kwh', 1)`, room)

	list := e.call(http.MethodGet, "/v1/public/rooms", nil, "")
	one := e.call(http.MethodGet, "/v1/public/rooms/"+room.String(), nil, "")
	landlord := e.call(http.MethodGet, "/v1/public/landlords/"+e.landlA.String(), nil, "")
	expectStatus(t, list, http.StatusOK)
	expectStatus(t, one, http.StatusOK)
	expectStatus(t, landlord, http.StatusOK)
	assertNothingPrivate(t, "the list", list)
	assertNothingPrivate(t, "the detail", one)
	assertNothingPrivate(t, "the landlord", landlord)

	p := listOf[PublicOfferView](t, list)
	if len(p.Data) != 1 || p.Data[0].ID != room || p.Data[0].Listing.Title != "Bright A101" || p.Data[0].Landlord.FullName != "Chu Hoa" ||
		p.Data[0].Ward == nil || p.Meta.Total != 1 {
		t.Fatalf("public list = %+v", p)
	}
	d := decodeData[struct {
		PublicOfferView
		Charges  []struct{ Name string } `json:"charges"`
		Pictures []any                   `json:"pictures"`
	}](t, one)
	if d.Name != "A101" || d.Description != nil && *d.Description == "" || len(d.Charges) != 1 || d.Pictures == nil {
		t.Errorf("public detail = %+v", d)
	}
	for _, w := range []*httptest.ResponseRecorder{list, one, landlord} {
		if got := w.Header().Get("Cache-Control"); got != "public, max-age=60" {
			t.Errorf("Cache-Control = %q", got)
		}
	}
	// What is not on offer is a 404 for a guest too.
	e.exec(`UPDATE rooms SET status = 'rented' WHERE id = $1`, room)
	expectError(t, e.call(http.MethodGet, "/v1/public/rooms/"+room.String(), nil, ""), http.StatusNotFound, apperr.CodeResourceNotFound)
	if p := listOf[PublicOfferView](t, e.call(http.MethodGet, "/v1/public/rooms", nil, "")); len(p.Data) != 0 {
		t.Errorf("a rented room is gone: %+v", p.Data)
	}
	// Errors are not cached.
	if got := e.call(http.MethodGet, "/v1/public/rooms/"+room.String(), nil, "").Header().Get("Cache-Control"); got != "" {
		t.Errorf("an error carries Cache-Control %q", got)
	}
}

func TestThePublicSearchFiltersLikeTheSignedInOne(t *testing.T) {
	e := newListingEnv(t)
	e.offer(e.landlA, "Cheap", 1_000_000)
	e.offer(e.landlB, "Dear", 5_000_000)
	got := func(query string) []string {
		t.Helper()
		p := listOf[PublicOfferView](t, e.call(http.MethodGet, "/v1/public/rooms"+query, nil, ""))
		return names(p.Data, func(o PublicOfferView) string { return o.Name })
	}
	if v := got("?price_from=0&price_to=2000000"); strings.Join(v, ",") != "Cheap" {
		t.Errorf("price: %v", v)
	}
	if v := got("?house_name=chu%20lan"); strings.Join(v, ",") != "Dear" {
		t.Errorf("house: %v", v)
	}
	if v := got("?sort=-price"); strings.Join(v, ",") != "Dear,Cheap" {
		t.Errorf("sort: %v", v)
	}
	if v := got("?landlord_id=" + uuid.NewString()); len(v) != 0 {
		t.Errorf("unknown landlord: %v", v)
	}
}

func TestThePublicLandlordProfileHasNoWayToContactThem(t *testing.T) {
	e := newListingEnv(t)
	e.offer(e.landlA, "A101", 1)
	e.exec(`INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'Nhà trọ Hoa', 'x', 1)`, e.landlA)
	e.exec(`UPDATE rooms SET building_id = (SELECT id FROM buildings LIMIT 1)`)

	for _, c := range []struct{ path, bearer, rooms string }{
		{"/v1/public/landlords/", "", "/v1/public/rooms"},
		{"/v1/landlords/", "tenant-t", "/v1/vacant-rooms"},
	} {
		w := e.call(http.MethodGet, c.path+e.landlA.String(), nil, c.bearer)
		expectStatus(t, w, http.StatusOK)
		assertNothingPrivate(t, c.path, w)
		l := decodeData[LandlordView](t, w)
		if l.ID != e.landlA || l.FullName != "Chu Hoa" || len(l.Houses) != 1 || l.Houses[0] != "Nhà trọ Hoa" || l.VacantRoomsCount != 1 ||
			l.RoomsURL != c.rooms+"?landlord_id="+e.landlA.String() {
			t.Errorf("%s: %+v", c.path, l)
		}
		// Nobody who is not a landlord has a profile, and an unknown id is a 404.
		expectError(t, e.call(http.MethodGet, c.path+e.tenantT.String(), nil, c.bearer), http.StatusNotFound, apperr.CodeResourceNotFound)
		expectError(t, e.call(http.MethodGet, c.path+e.admin.String(), nil, c.bearer), http.StatusNotFound, apperr.CodeResourceNotFound)
		expectError(t, e.call(http.MethodGet, c.path+uuid.NewString(), nil, c.bearer), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
}

func TestThePublicRoutesAreRateLimitedPerClientAddress(t *testing.T) {
	e := newListingEnv(t)
	e.limit.allowed = 3
	for i := 0; i < 3; i++ {
		expectStatus(t, e.call(http.MethodGet, "/v1/public/rooms", nil, ""), http.StatusOK)
	}
	w := e.call(http.MethodGet, "/v1/public/rooms", nil, "")
	expectError(t, w, http.StatusTooManyRequests, apperr.CodeRequestRateLimited)
	if w.Header().Get("Retry-After") == "" {
		t.Error("a limited answer says when to come back")
	}
	// The three public routes share the counter; the signed in search has none.
	expectError(t, e.call(http.MethodGet, "/v1/public/landlords/"+e.landlA.String(), nil, ""), http.StatusTooManyRequests, apperr.CodeRequestRateLimited)
	expectStatus(t, e.call(http.MethodGet, "/v1/vacant-rooms", nil, "tenant-t"), http.StatusOK)
}
