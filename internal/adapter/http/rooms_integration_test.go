//go:build integration

package httpadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/apperr"
)

// room creates a room through the API and returns it.
func (e *propertyEnv) roomAPI(bearer string, body map[string]any) RoomDetail {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/rooms", body, bearer)
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[RoomDetail](e.t, w)
}

func (e *propertyEnv) getRoom(bearer string, id uuid.UUID) RoomDetail {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/rooms/"+id.String(), nil, bearer)
	expectStatus(e.t, w, http.StatusOK)
	return decodeData[RoomDetail](e.t, w)
}

func (e *propertyEnv) putRoom(bearer string, id uuid.UUID, body map[string]any) RoomDetail {
	e.t.Helper()
	w := e.call(http.MethodPut, "/v1/rooms/"+id.String(), body, bearer)
	expectStatus(e.t, w, http.StatusOK)
	return decodeData[RoomDetail](e.t, w)
}

// fullBuilding makes a building with every default set and three charges.
func (e *propertyEnv) fullBuilding(bearer, name string) BuildingDetail {
	return e.building(bearer, name, map[string]any{
		"ward_code": e.ward, "description": "Quiet", "price_vnd": 2_500_000, "area_square_meters": 20,
		"minimum_rental_period": 2, "rental_period_unit": "month",
		"charges": []map[string]any{charge("Power", "electricity", 3500, "per_kwh"), charge("Water", "water", 20000, "per_cubic_meter")},
	})
}

type roomList struct {
	Data []RoomSummary `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

func (e *propertyEnv) listRooms(bearer, query string) roomList {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/rooms"+query, nil, bearer)
	expectStatus(e.t, w, http.StatusOK)
	var out roomList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func roomNamesOf(l roomList) []string {
	var out []string
	for _, r := range l.Data {
		out = append(out, r.Name)
	}
	return out
}

func TestRoomOutsideABuildingNeedsItsOwnAddressAndPrice(t *testing.T) {
	e := newPropertyEnv(t, nil)
	for name, tc := range map[string]struct {
		body  map[string]any
		field string
		rule  string
	}{
		"no address":     {map[string]any{"name": "A1", "price_vnd": 1_000_000}, "address_detail", apperr.RuleRequired},
		"blank address":  {map[string]any{"name": "A1", "address_detail": "  ", "price_vnd": 1}, "address_detail", apperr.RuleRequired},
		"no price":       {map[string]any{"name": "A1", "address_detail": "x"}, "price_vnd", apperr.RuleRequired},
		"blank name":     {map[string]any{"name": " ", "address_detail": "x", "price_vnd": 1}, "name", apperr.RuleRequired},
		"unknown ward":   {map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1, "ward_code": 999}, "ward_code", apperr.RuleExists},
		"bad status":     {map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1, "status": "gone"}, "status", apperr.RuleOneOf},
		"bad group":      {map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1, "inherit": []string{"colour"}}, "inherit.0", apperr.RuleOneOf},
		"bad charge":     {map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1, "charges": []map[string]any{charge("", "water", 1, "per_period")}}, "charges.0.name", apperr.RuleRequired},
		"too many items": {map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1, "charges": make([]map[string]any, 31)}, "charges", ""},
	} {
		t.Run(name, func(t *testing.T) {
			body := expectError(t, e.call(http.MethodPost, "/v1/rooms", tc.body, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
			if _, ok := body.Error.Fields[tc.field]; !ok {
				t.Errorf("want a message on %q, got %v", tc.field, body.Error.Fields)
			}
		})
	}
	// Nothing was created.
	if got := e.listRooms("landlord-a", ""); got.Meta.Total != 0 {
		t.Errorf("a refused request creates nothing, total %d", got.Meta.Total)
	}
}

func TestRoomCRUDRoundTrip(t *testing.T) {
	e := newPropertyEnv(t, nil)
	created := e.roomAPI("landlord-a", map[string]any{
		"name": "  A1 ", "ward_code": e.ward, "address_detail": " 12 Le Loi ", "price_vnd": 1_800_000, "area_square_meters": 18.456,
		"minimum_rental_period": 3, "rental_period_unit": "month", "description": "Ground floor",
		"charges": []map[string]any{charge("Điện", "electricity", 3500, "per_kwh")},
	})
	if created.Name != "A1" || created.Status != "available" || created.DisplayOrder != 1 || created.Building != nil || created.AddressDetail != "12 Le Loi" {
		t.Errorf("created %+v", created.RoomSummaryView)
	}
	if created.PriceVND != 1_800_000 || *created.AreaSquareMeters != 18.46 || created.MinimumRentalPeriod != 3 || created.RentalPeriodUnit != "month" ||
		created.Description == nil || *created.Description != "Ground floor" || len(created.Inherits) != 0 {
		t.Errorf("values %+v", created)
	}
	if created.Ward == nil || created.Ward.Name != "Ba Dinh" || created.Cover != nil || created.Media == nil {
		t.Errorf("ward %+v cover %+v media %v", created.Ward, created.Cover, created.Media)
	}
	if len(created.Charges) != 1 || created.Charges[0].Name != "Điện" || created.Charges[0].FollowsBuilding {
		t.Errorf("charges %+v", created.Charges)
	}

	// PUT replaces the scalar fields; the status and the charges, left out, stay.
	updated := e.putRoom("landlord-a", created.ID, map[string]any{"name": "A2", "address_detail": "99 Tran Phu", "price_vnd": 2_000_000})
	if updated.Name != "A2" || updated.PriceVND != 2_000_000 || updated.AddressDetail != "99 Tran Phu" || updated.Status != "available" {
		t.Errorf("updated %+v", updated.RoomSummaryView)
	}
	if updated.Ward != nil || updated.AreaSquareMeters != nil || updated.Description != nil || updated.MinimumRentalPeriod != 1 {
		t.Errorf("fields left out of a PUT are cleared: %+v", updated)
	}
	if len(updated.Charges) != 1 || updated.Charges[0].ID != created.Charges[0].ID {
		t.Errorf("charges left out of the body are kept: %+v", updated.Charges)
	}
	if got := e.putRoom("landlord-a", created.ID, map[string]any{"name": "A2", "address_detail": "x", "price_vnd": 1, "status": "maintenance", "charges": []any{}}); got.Status != "maintenance" || len(got.Charges) != 0 {
		t.Errorf("status %s charges %+v", got.Status, got.Charges)
	}

	expectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+created.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	expectError(t, e.call(http.MethodGet, "/v1/rooms/"+created.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodDelete, "/v1/rooms/"+created.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestRoomsAreOwnerOnly(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1_000_000})
	mine := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	id := mine.ID.String()
	body := map[string]any{"name": "X", "address_detail": "y", "price_vnd": 1}

	for _, bearer := range []string{"tenant", "admin"} {
		for _, call := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/v1/rooms", nil},
			{http.MethodPost, "/v1/rooms", body},
			{http.MethodGet, "/v1/rooms/" + id, nil},
			{http.MethodPut, "/v1/rooms/" + id, body},
			{http.MethodDelete, "/v1/rooms/" + id, nil},
			{http.MethodPut, "/v1/rooms/order", map[string]any{"ids": []string{id}}},
			{http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": []string{id}}},
			{http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []string{id}, "status": "rented"}},
			{http.MethodPost, "/v1/buildings/" + b.ID.String() + "/rooms", map[string]any{"from": 1, "to": 2}},
		} {
			expectError(t, e.call(call.method, call.path, call.body, bearer), http.StatusForbidden, apperr.CodeForbidden)
		}
	}
	expectError(t, e.call(http.MethodGet, "/v1/rooms", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)

	// Another landlord cannot tell the room (or the building) exists.
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/rooms/" + id, nil},
		{http.MethodPut, "/v1/rooms/" + id, body},
		{http.MethodDelete, "/v1/rooms/" + id, nil},
		{http.MethodGet, "/v1/rooms/not-a-uuid", nil},
		{http.MethodPost, "/v1/buildings/" + b.ID.String() + "/rooms", map[string]any{"from": 1, "to": 2}},
	} {
		expectError(t, e.call(call.method, call.path, call.body, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	if got := e.listRooms("landlord-b", ""); got.Meta.Total != 0 || len(got.Data) != 0 {
		t.Errorf("landlord b sees %d rooms", got.Meta.Total)
	}
	// A building of someone else is not a valid home, and a bulk id of someone else fails the request.
	other := e.building("landlord-b", "Theirs", map[string]any{"price_vnd": 1})
	expectError(t, e.call(http.MethodPost, "/v1/rooms", map[string]any{"building_id": other.ID, "name": "X"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": []string{id}}, "landlord-b"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if got := e.getRoom("landlord-a", mine.ID); got.ID != mine.ID {
		t.Error("my room is untouched")
	}
}

func TestRoomFollowsItsBuilding(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.fullBuilding("landlord-a", "Nha A")

	// Nothing of its own: it follows every part and takes the building's address, whatever it typed.
	all := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1", "address_detail": "ignored", "ward_code": 999})
	if all.Building == nil || all.Building.ID != b.ID || all.Building.Name != "Nha A" {
		t.Errorf("building %+v", all.Building)
	}
	if !slices.Equal(all.Inherits, []string{"price", "area", "rental_period", "description", "charges", "media"}) {
		t.Errorf("inherits %v", all.Inherits)
	}
	if all.PriceVND != 2_500_000 || *all.AreaSquareMeters != 20 || all.MinimumRentalPeriod != 2 || all.Description == nil || *all.Description != "Quiet" ||
		all.AddressDetail != "12 Le Loi" || all.Ward == nil || all.Ward.Code != e.ward {
		t.Errorf("values %+v", all)
	}
	if len(all.Charges) != 2 || !all.Charges[0].FollowsBuilding || all.Charges[0].ID == b.Charges[0].ID || all.Charges[0].Name != "Power" {
		t.Errorf("charges are copies of the templates: %+v", all.Charges)
	}

	// Its own rent: price is the one part that does not follow.
	own := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P2", "price_vnd": 3_000_000})
	if own.PriceVND != 3_000_000 || slices.Contains(own.Inherits, "price") || !slices.Contains(own.Inherits, "area") {
		t.Errorf("own price %+v", own.RoomSummaryView)
	}
	// Its own charges: they are kept and the room stops following the building's.
	ownCharges := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P3", "charges": []map[string]any{charge("Wifi", "internet", 1, "per_period")}})
	if len(ownCharges.Charges) != 1 || ownCharges.Charges[0].FollowsBuilding || slices.Contains(ownCharges.Inherits, "charges") {
		t.Errorf("own charges %+v inherits %v", ownCharges.Charges, ownCharges.Inherits)
	}
	// An explicit list wins over what the request carries.
	listed := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P4", "price_vnd": 9, "inherit": []string{"price"}})
	if listed.PriceVND != 2_500_000 || !slices.Equal(listed.Inherits, []string{"price"}) {
		t.Errorf("listed %+v", listed.RoomSummaryView)
	}
	// A building without rent cannot be followed for it.
	bare := e.building("landlord-a", "Bare", nil)
	body := expectError(t, e.call(http.MethodPost, "/v1/rooms", map[string]any{"building_id": bare.ID, "name": "X"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if _, ok := body.Error.Fields["price_vnd"]; !ok {
		t.Errorf("fields %v", body.Error.Fields)
	}
}

func TestRoomUpdateChangesWhatItFollows(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.fullBuilding("landlord-a", "Nha A")
	room := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	templateCopy := room.Charges[0]

	// Own price and an explicit follow list: the room leaves the other parts, but keeps its charges as its own.
	got := e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "price_vnd": 3_300_000, "inherit": []string{"area"}})
	if got.PriceVND != 3_300_000 || !slices.Equal(got.Inherits, []string{"area"}) {
		t.Fatalf("got %+v", got.RoomSummaryView)
	}
	if len(got.Charges) != 2 || got.Charges[0].FollowsBuilding || got.Charges[0].ID != templateCopy.ID {
		t.Errorf("charges stay as the room's own: %+v", got.Charges)
	}
	// PUT without charges keeps them; an empty list clears; an unknown id creates instead of touching another's row.
	if got = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "price_vnd": 1, "inherit": []string{}}); len(got.Charges) != 2 {
		t.Errorf("kept %+v", got.Charges)
	}
	other := e.fullBuilding("landlord-b", "Theirs")
	got = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "price_vnd": 1, "inherit": []string{},
		"charges": []map[string]any{{"id": other.Charges[0].ID, "name": "Mine", "kind": "service", "amount_vnd": 1, "unit": "one_time"},
			{"id": templateCopy.ID, "name": "Renamed", "kind": "water", "amount_vnd": 5, "unit": "per_period"}}})
	if len(got.Charges) != 2 || got.Charges[0].ID == other.Charges[0].ID || got.Charges[1].ID != templateCopy.ID || got.Charges[1].Name != "Renamed" {
		t.Errorf("charges %+v", got.Charges)
	}
	if got = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "price_vnd": 1, "charges": []any{}}); len(got.Charges) != 0 {
		t.Errorf("an empty list clears: %+v", got.Charges)
	}

	// Back to following everything: the building's values and charge copies return.
	got = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "inherit": []string{"price", "area", "rental_period", "description", "charges", "media"}})
	if got.PriceVND != 2_500_000 || len(got.Charges) != 2 || !got.Charges[0].FollowsBuilding || len(got.Inherits) != 6 {
		t.Errorf("following again %+v charges %+v", got.RoomSummaryView, got.Charges)
	}

	// Leaving the building: nothing follows, the room keeps the values it holds and needs its own address.
	got = e.putRoom("landlord-a", room.ID, map[string]any{"name": "P1", "address_detail": "Somewhere", "price_vnd": 700})
	if got.Building != nil || len(got.Inherits) != 0 || got.PriceVND != 700 || got.AddressDetail != "Somewhere" {
		t.Errorf("outside %+v", got.RoomSummaryView)
	}
	if len(got.Charges) != 2 || got.Charges[0].FollowsBuilding {
		t.Errorf("the copies become its own: %+v", got.Charges)
	}
	// Moving into a building takes its values for what the request leaves out; the room's own charges stay (a PUT
	// does not drop what the room owns unless told to follow).
	got = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1"})
	if got.Building == nil || got.PriceVND != 2_500_000 || !slices.Equal(got.Inherits, []string{"price", "area", "rental_period", "description"}) ||
		len(got.Charges) != 2 || got.Charges[0].FollowsBuilding {
		t.Errorf("back inside %+v charges %+v", got.RoomSummaryView, got.Charges)
	}
}

func TestRoomNamesAreUniquePerBuilding(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b1 := e.building("landlord-a", "One", map[string]any{"price_vnd": 1})
	b2 := e.building("landlord-a", "Two", map[string]any{"price_vnd": 1})
	e.roomAPI("landlord-a", map[string]any{"building_id": b1.ID, "name": "P1"})
	e.roomAPI("landlord-a", map[string]any{"building_id": b2.ID, "name": "P1"}) // another building: fine
	solo := map[string]any{"name": "P1", "address_detail": "x", "price_vnd": 1}
	e.roomAPI("landlord-a", solo) // outside buildings: its own group
	e.roomAPI("landlord-b", solo) // another landlord

	for name, body := range map[string]map[string]any{
		"same building":     {"building_id": b1.ID, "name": "P1"},
		"other case":        {"building_id": b1.ID, "name": "p1"},
		"outside buildings": {"name": "p1", "address_detail": "x", "price_vnd": 1},
	} {
		t.Run(name, func(t *testing.T) {
			got := expectError(t, e.call(http.MethodPost, "/v1/rooms", body, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
			if _, ok := got.Error.Fields["name"]; !ok {
				t.Errorf("fields %v", got.Error.Fields)
			}
		})
	}
	// Renaming into a taken name is refused, renaming to oneself in another case is not.
	p2 := e.roomAPI("landlord-a", map[string]any{"building_id": b1.ID, "name": "P2"})
	expectError(t, e.call(http.MethodPut, "/v1/rooms/"+p2.ID.String(), map[string]any{"building_id": b1.ID, "name": "p1"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	e.putRoom("landlord-a", p2.ID, map[string]any{"building_id": b1.ID, "name": "p2"})
	// A deleted room frees its name.
	first := e.listRooms("landlord-a", "?building_id="+b1.ID.String()+"&name=p1").Data[0]
	expectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+first.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	e.roomAPI("landlord-a", map[string]any{"building_id": b1.ID, "name": "P1"})
}

func TestRoomListFiltersSortAndPages(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "Nha", map[string]any{"price_vnd": 2_000_000, "area_square_meters": 30, "ward_code": e.ward})
	solo := func(name string, price int, extra map[string]any) {
		body := map[string]any{"name": name, "address_detail": "x", "price_vnd": price}
		for k, v := range extra {
			body[k] = v
		}
		e.roomAPI("landlord-a", body)
	}
	e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "Charlie"})
	solo("alpha", 1_000_000, map[string]any{"area_square_meters": 10})
	solo("Bravo 50%", 3_000_000, map[string]any{"status": "maintenance", "ward_code": e.ward})
	e.roomAPI("landlord-b", map[string]any{"name": "Zulu", "address_detail": "x", "price_vnd": 1})

	all := e.listRooms("landlord-a", "")
	if all.Meta.Total != 3 || all.Meta.Page != 1 || all.Meta.PerPage != 20 || !slices.Equal(roomNamesOf(all), []string{"Charlie", "alpha", "Bravo 50%"}) {
		t.Errorf("default list %v %+v", roomNamesOf(all), all.Meta)
	}
	if all.Data[0].Building == nil || all.Data[1].Building != nil {
		t.Errorf("buildings %+v %+v", all.Data[0].Building, all.Data[1].Building)
	}
	for query, want := range map[string][]string{
		"?sort=name":                           {"alpha", "Bravo 50%", "Charlie"},
		"?sort=-name":                          {"Charlie", "Bravo 50%", "alpha"},
		"?sort=-price":                         {"Bravo 50%", "Charlie", "alpha"},
		"?sort=price":                          {"alpha", "Charlie", "Bravo 50%"},
		"?sort=area":                           {"alpha", "Charlie", "Bravo 50%"},
		"?sort=-area":                          {"Charlie", "alpha", "Bravo 50%"},
		"?name=ALP":                            {"alpha"},
		"?name=%25":                            {"Bravo 50%"},
		"?status=maintenance":                  {"Bravo 50%"},
		"?building_id=" + b.ID.String():        {"Charlie"},
		"?no_building=true":                    {"alpha", "Bravo 50%"},
		"?price_from=2000000":                  {"Charlie", "Bravo 50%"},
		"?price_to=2000000":                    {"Charlie", "alpha"},
		"?price_from=1500000&price_to=2500000": {"Charlie"},
		"?area_from=20":                        {"Charlie"},
		"?area_to=10":                          {"alpha"},
		"?ward_code=4":                         {"Charlie", "Bravo 50%"},
		"?province_code=1":                     {"Charlie", "Bravo 50%"},
		"?province_code=2":                     nil,
	} {
		if got := roomNamesOf(e.listRooms("landlord-a", query)); !slices.Equal(got, want) {
			t.Errorf("%s: got %v want %v", query, got, want)
		}
	}
	page := e.listRooms("landlord-a", "?per_page=2&page=2")
	if page.Meta.Total != 3 || len(page.Data) != 1 || page.Data[0].Name != "Bravo 50%" {
		t.Errorf("page 2 %v %+v", roomNamesOf(page), page.Meta)
	}
	for _, q := range []string{"?sort=colour", "?status=gone", "?price_from=abc", "?price_from=3&price_to=2", "?area_from=5&area_to=1",
		"?building_id=" + b.ID.String() + "&no_building=true", "?building_id=nope", "?no_building=maybe", "?per_page=0", "?page=x"} {
		expectError(t, e.call(http.MethodGet, "/v1/rooms"+q, nil, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
}

func TestRoomStatusAndDeleteAreGuarded(t *testing.T) {
	e := newPropertyEnv(t, nil)
	rec := &recorder{}
	e.observer.set(rec)
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	busy := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "Busy", "status": "rented"})
	free := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "Free"})
	listed := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "Listed"})
	if len(rec.statuses) != 1 || rec.statuses[0] != (domain.StatusChange{RoomID: busy.ID, To: domain.StatusRented}) {
		t.Errorf("creating a rented room tells the observer: %+v", rec.statuses)
	}
	rec.statuses, rec.content = nil, nil
	e.guards.set(fakeGuards{busy.ID: {ActiveContract: true}, listed.ID: {OpenListing: true}})
	same := func(r RoomDetail, status string) map[string]any {
		return map[string]any{"building_id": b.ID, "name": r.Name, "status": status}
	}

	// A running contract keeps the room rented; staying rented, or any change of a free room, is fine.
	expectError(t, e.call(http.MethodPut, "/v1/rooms/"+busy.ID.String(), same(busy, "available"), "landlord-a"), http.StatusConflict, apperr.CodeRoomStatusLocked)
	expectError(t, e.call(http.MethodPut, "/v1/rooms/"+busy.ID.String(), same(busy, "maintenance"), "landlord-a"), http.StatusConflict, apperr.CodeRoomStatusLocked)
	if got := e.putRoom("landlord-a", busy.ID, same(busy, "rented")); got.Status != "rented" {
		t.Errorf("status %s", got.Status)
	}
	if got := e.putRoom("landlord-a", free.ID, same(free, "rented")); got.Status != "rented" {
		t.Errorf("status %s", got.Status)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != (domain.StatusChange{RoomID: free.ID, From: domain.StatusAvailable, To: domain.StatusRented}) {
		t.Errorf("status events %+v", rec.statuses)
	}

	expectError(t, e.call(http.MethodDelete, "/v1/rooms/"+busy.ID.String(), nil, "landlord-a"), http.StatusConflict, apperr.CodeRoomHasContract)
	expectError(t, e.call(http.MethodDelete, "/v1/rooms/"+listed.ID.String(), nil, "landlord-a"), http.StatusConflict, apperr.CodeRoomHasListing)

	// Bulk: the others still go through and the refused ones are named.
	w := e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{busy.ID, free.ID, listed.ID}, "status": "maintenance"}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	result := decodeData[bulkResultView](t, w)
	if len(result.Failed) != 1 || result.Failed[0].ID != busy.ID || result.Failed[0].Code != apperr.CodeRoomStatusLocked || result.Failed[0].Message == "" || result.Failed[0].Name != "Busy" {
		t.Errorf("failed %+v", result.Failed)
	}
	if !slices.Contains(result.Done, free.ID) || !slices.Contains(result.Done, listed.ID) || len(result.Done) != 2 {
		t.Errorf("done %v", result.Done)
	}
	if got := e.getRoom("landlord-a", free.ID).Status; got != "maintenance" {
		t.Errorf("free is now %s", got)
	}
	// Rooms already there count as done and send no event.
	rec.statuses = nil
	w = e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{free.ID}, "status": "maintenance"}, "landlord-a")
	if result = decodeData[bulkResultView](t, w); len(result.Done) != 1 || len(result.Failed) != 0 || len(rec.statuses) != 0 {
		t.Errorf("no-op %+v events %+v", result, rec.statuses)
	}

	w = e.call(http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": []uuid.UUID{busy.ID, free.ID, listed.ID}}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	result = decodeData[bulkResultView](t, w)
	codes := map[uuid.UUID]string{}
	for _, f := range result.Failed {
		codes[f.ID] = f.Code
	}
	if len(result.Done) != 1 || result.Done[0] != free.ID || codes[busy.ID] != apperr.CodeRoomHasContract || codes[listed.ID] != apperr.CodeRoomHasListing {
		t.Errorf("bulk delete %+v", result)
	}
	for name, ids := range map[string]any{"none": []uuid.UUID{}, "duplicate": []uuid.UUID{busy.ID, busy.ID}, "too many": make([]uuid.UUID, 101)} {
		t.Run(name, func(t *testing.T) {
			expectError(t, e.call(http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": ids}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		})
	}
	expectError(t, e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{busy.ID}, "status": "gone"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestRoomObserverHearsContentChanges(t *testing.T) {
	e := newPropertyEnv(t, nil)
	rec := &recorder{}
	e.observer.set(rec)
	b := e.fullBuilding("landlord-a", "B")
	room := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	body := func(extra map[string]any) map[string]any {
		out := map[string]any{"building_id": b.ID, "name": "P1"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	if len(rec.content) != 0 {
		t.Fatalf("creating tells nobody: %v", rec.content)
	}
	// Saving the same thing changes nothing a listing shows; only a status change moves, which is another event.
	e.putRoom("landlord-a", room.ID, body(nil))
	e.putRoom("landlord-a", room.ID, body(map[string]any{"status": "maintenance"}))
	if len(rec.content) != 0 {
		t.Errorf("no content change, got %v", rec.content)
	}
	e.putRoom("landlord-a", room.ID, body(map[string]any{"price_vnd": 1}))
	if len(rec.content) != 1 || !slices.Equal(rec.content[0], []uuid.UUID{room.ID}) {
		t.Errorf("a price of its own is a content change: %v", rec.content)
	}
	// The same own charges again are not a change; a different list is.
	own := []map[string]any{charge("Wifi", "internet", 1, "per_period")}
	e.putRoom("landlord-a", room.ID, body(map[string]any{"price_vnd": 1, "charges": own}))
	n := len(rec.content)
	got := e.getRoom("landlord-a", room.ID)
	again := []map[string]any{{"id": got.Charges[0].ID, "name": "Wifi", "kind": "internet", "amount_vnd": 1, "unit": "per_period"}}
	e.putRoom("landlord-a", room.ID, body(map[string]any{"price_vnd": 1, "charges": again}))
	if len(rec.content) != n {
		t.Errorf("identical charges: %v", rec.content[n:])
	}
	e.putRoom("landlord-a", room.ID, body(map[string]any{"price_vnd": 1, "charges": []map[string]any{{"id": got.Charges[0].ID, "name": "Wifi", "kind": "internet", "amount_vnd": 2, "unit": "per_period"}}}))
	if len(rec.content) != n+1 {
		t.Errorf("changed charge: %v", rec.content[n:])
	}
}

func TestRoomReorder(t *testing.T) {
	e := newPropertyEnv(t, nil)
	mk := func(bearer, name string) uuid.UUID {
		return e.roomAPI(bearer, map[string]any{"name": name, "address_detail": "x", "price_vnd": 1}).ID
	}
	a, b, c := mk("landlord-a", "A"), mk("landlord-a", "B"), mk("landlord-a", "C")
	foreign := mk("landlord-b", "Z")
	order := func() []uuid.UUID {
		var ids []uuid.UUID
		for _, r := range e.listRooms("landlord-a", "").Data {
			ids = append(ids, r.ID)
		}
		return ids
	}
	expectStatus(t, e.call(http.MethodPut, "/v1/rooms/order", map[string]any{"ids": []uuid.UUID{c, a}}, "landlord-a"), http.StatusNoContent)
	if got := order(); !slices.Equal(got, []uuid.UUID{c, b, a}) {
		t.Errorf("order %v", got)
	}
	body := expectError(t, e.call(http.MethodPut, "/v1/rooms/order", map[string]any{"ids": []uuid.UUID{a, foreign}}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if _, ok := body.Error.Fields["ids"]; !ok {
		t.Errorf("fields %v", body.Error.Fields)
	}
	d := mk("landlord-a", "D")
	expectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+d.String(), nil, "landlord-a"), http.StatusNoContent)
	if next := e.roomAPI("landlord-a", map[string]any{"name": "E", "address_detail": "x", "price_vnd": 1}); next.DisplayOrder <= 4 {
		t.Errorf("a new room must not reuse a deleted position, got %d", next.DisplayOrder)
	}
}

func TestRoomPlanLimit(t *testing.T) {
	limit := 2
	e := newPropertyEnv(t, fakePlans{rooms: &limit})
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P2"})
	body := expectError(t, e.call(http.MethodPost, "/v1/rooms", map[string]any{"building_id": b.ID, "name": "P3"}, "landlord-a"), http.StatusConflict, apperr.CodeRoomLimit)
	if body.Error.Message != "Your plan allows 2 rooms." {
		t.Errorf("message %q", body.Error.Message)
	}
	// The limit is per landlord, and deleting frees a slot.
	e.roomAPI("landlord-b", map[string]any{"name": "Mine", "address_detail": "x", "price_vnd": 1})
	first := e.listRooms("landlord-a", "").Data[0]
	expectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+first.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P3"})
}

func TestRoomFollowsTheBuildingCover(t *testing.T) {
	e := newPropertyEnv(t, nil)
	ctx := context.Background()
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO media (building_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, display_order)
		VALUES ($1, 'cover', 'property/2026/11/b.webp', 'property/2026/11/b_thumb.jpg', 'image/webp', 1, 10, 10, 1)`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE buildings SET cover_key = 'property/2026/11/b.webp', cover_thumb_key = 'property/2026/11/b_thumb.jpg' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	room := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	if room.Cover == nil || room.Cover.URL != "http://files.test/v1/media/property/2026/11/b.webp" || len(room.Media) != 1 || room.Media[0].Kind != "cover" {
		t.Errorf("follows the building's pictures: cover %+v media %+v", room.Cover, room.Media)
	}
	// Leaving the pictures behind: the room shows its own, which are none.
	room = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "inherit": []string{"price"}})
	if room.Cover != nil || len(room.Media) != 0 || slices.Contains(room.Inherits, "media") {
		t.Errorf("own pictures: cover %+v media %+v", room.Cover, room.Media)
	}
	// Following again drops any picture it had of its own and shows the building's cover.
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO media (room_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, display_order)
		VALUES ($1, 'photo', 'property/2026/11/r.webp', 'property/2026/11/r_thumb.jpg', 'image/webp', 1, 10, 10, 1)`, room.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Put(ctx, "property/2026/11/r.webp", []byte("x"), "image/webp"); err != nil {
		t.Fatal(err)
	}
	room = e.putRoom("landlord-a", room.ID, map[string]any{"building_id": b.ID, "name": "P1", "inherit": []string{"price", "media"}})
	if room.Cover == nil || len(room.Media) != 1 || room.Media[0].URL != room.Cover.URL {
		t.Errorf("following again: cover %+v media %+v", room.Cover, room.Media)
	}
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("the dropped picture's file is deleted, left %v", keys)
	}
}
