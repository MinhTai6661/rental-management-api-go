//go:build integration

package propertyhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/apperr"
)

func charge(name, kind string, amount int64, unit string) map[string]any {
	return map[string]any{"name": name, "kind": kind, "amount_vnd": amount, "unit": unit}
}

func TestBuildingCRUDRoundTrip(t *testing.T) {
	e := newPropertyEnv(t, nil)

	created := e.building("landlord-a", "  Nhà trọ A ", map[string]any{
		"ward_code": e.ward, "description": "Quiet", "price_vnd": 2_500_000, "area_square_meters": 18.456,
		"minimum_rental_period": 3, "rental_period_unit": "month",
		"charges": []map[string]any{charge("Điện", "electricity", 3500, "per_kwh"), charge("Nước", "water", 20000, "per_cubic_meter")},
	})
	if created.Name != "Nhà trọ A" || created.DisplayOrder != 1 || created.RoomsCount != 0 {
		t.Errorf("unexpected building %+v", created.BuildingSummaryView)
	}
	if created.Ward == nil || created.Ward.Name != "Ba Dinh" || created.Ward.Province.Name != "Ha Noi" {
		t.Errorf("ward %+v", created.Ward)
	}
	if *created.AreaSquareMeters != 18.46 || *created.MinimumRentalPeriod != 3 || *created.PriceVND != 2_500_000 {
		t.Errorf("values %+v", created.BuildingSummaryView)
	}
	if len(created.Charges) != 2 || created.Charges[0].Name != "Điện" || created.Charges[1].DisplayOrder != 2 || created.Media == nil {
		t.Errorf("charges %+v media %v", created.Charges, created.Media)
	}

	w := e.call(http.MethodGet, "/v1/buildings/"+created.ID.String(), nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[BuildingDetail](t, w); got.ID != created.ID {
		t.Errorf("get returned %v", got.ID)
	}

	// PUT replaces the fields; the charges list is left out, so they stay.
	w = e.call(http.MethodPut, "/v1/buildings/"+created.ID.String(), map[string]any{
		"name": "Nhà trọ A2", "address_detail": "99 Tran Phu", "price_vnd": 2_700_000,
	}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	updated := decodeData[BuildingDetail](t, w)
	if updated.Name != "Nhà trọ A2" || updated.AddressDetail != "99 Tran Phu" || *updated.PriceVND != 2_700_000 {
		t.Errorf("updated %+v", updated.BuildingSummaryView)
	}
	if updated.Ward != nil || updated.AreaSquareMeters != nil || updated.MinimumRentalPeriod != nil || updated.Description != nil {
		t.Errorf("fields left out of a PUT are cleared: %+v", updated.BuildingSummaryView)
	}
	if len(updated.Charges) != 2 {
		t.Errorf("charges left out of the body are kept, got %d", len(updated.Charges))
	}

	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+created.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	expectError(t, e.call(http.MethodGet, "/v1/buildings/"+created.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestBuildingsAreOwnerOnly(t *testing.T) {
	e := newPropertyEnv(t, nil)
	mine := e.building("landlord-a", "Mine", nil)
	id := mine.ID.String()

	for _, bearer := range []string{"tenant", "admin"} {
		for _, call := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/v1/buildings", nil},
			{http.MethodPost, "/v1/buildings", map[string]any{"name": "X", "address_detail": "y"}},
			{http.MethodGet, "/v1/buildings/" + id, nil},
			{http.MethodPut, "/v1/buildings/" + id, map[string]any{"name": "X", "address_detail": "y"}},
			{http.MethodDelete, "/v1/buildings/" + id, nil},
			{http.MethodPut, "/v1/buildings/order", map[string]any{"ids": []string{id}}},
			{http.MethodPost, "/v1/buildings/bulk-delete", map[string]any{"ids": []string{id}}},
			{http.MethodPost, "/v1/buildings/" + id + "/copy", map[string]any{"name": "Copy"}},
		} {
			expectError(t, e.call(call.method, call.path, call.body, bearer), http.StatusForbidden, apperr.CodeForbidden)
		}
	}
	expectError(t, e.call(http.MethodGet, "/v1/buildings", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)

	// Another landlord cannot tell the building exists.
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/buildings/" + id, nil},
		{http.MethodPut, "/v1/buildings/" + id, map[string]any{"name": "X", "address_detail": "y"}},
		{http.MethodDelete, "/v1/buildings/" + id, nil},
		{http.MethodPost, "/v1/buildings/" + id + "/copy", map[string]any{"name": "Copy"}},
		{http.MethodGet, "/v1/buildings/not-a-uuid", nil},
	} {
		expectError(t, e.call(call.method, call.path, call.body, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	w := e.call(http.MethodGet, "/v1/buildings", nil, "landlord-b")
	if got := decodeData[[]BuildingSummary](t, w); len(got) != 0 {
		t.Errorf("landlord b sees %d buildings", len(got))
	}
	// And my building is untouched.
	if got := e.building("landlord-b", "Mine", nil); got.ID == mine.ID {
		t.Error("names are per landlord")
	}
}

func TestBuildingValidation(t *testing.T) {
	e := newPropertyEnv(t, nil)
	e.building("landlord-a", "Alpha", nil)

	cases := map[string]struct {
		body  map[string]any
		field string
		rule  string
	}{
		"duplicate name in another case": {map[string]any{"name": "ALPHA", "address_detail": "x"}, "name", apperr.RuleUnique},
		"blank name":                     {map[string]any{"name": "   ", "address_detail": "x"}, "name", apperr.RuleRequired},
		"missing address":                {map[string]any{"name": "B"}, "address_detail", apperr.RuleRequired},
		"unknown ward":                   {map[string]any{"name": "B", "address_detail": "x", "ward_code": 999}, "ward_code", apperr.RuleExists},
		"period without unit":            {map[string]any{"name": "B", "address_detail": "x", "minimum_rental_period": 2}, "minimum_rental_period", apperr.RuleRentalPeriodPair},
		"bad unit":                       {map[string]any{"name": "B", "address_detail": "x", "minimum_rental_period": 2, "rental_period_unit": "year"}, "rental_period_unit", apperr.RuleOneOf},
		"negative price":                 {map[string]any{"name": "B", "address_detail": "x", "price_vnd": -1}, "price_vnd", apperr.RuleMinNumber},
		"long name":                      {map[string]any{"name": string(make([]byte, 51)), "address_detail": "x"}, "name", ""},
		"charge without a name":          {map[string]any{"name": "B", "address_detail": "x", "charges": []map[string]any{charge("  ", "water", 1, "per_period")}}, "charges.0.name", apperr.RuleRequired},
		"charge with a bad unit":         {map[string]any{"name": "B", "address_detail": "x", "charges": []map[string]any{charge("A", "water", 1, "per_week")}}, "charges.0.unit", apperr.RuleOneOf},
		"charge without an amount":       {map[string]any{"name": "B", "address_detail": "x", "charges": []map[string]any{{"name": "A", "kind": "water", "unit": "per_period"}}}, "charges.0.amount_vnd", apperr.RuleRequired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := e.call(http.MethodPost, "/v1/buildings", tc.body, "landlord-a")
			body := expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
			if _, ok := body.Error.Fields[tc.field]; !ok {
				t.Errorf("want a message on %q, got %v", tc.field, body.Error.Fields)
			}
		})
	}

	// A deleted building frees its name.
	b := e.building("landlord-a", "Beta", nil)
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+b.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	e.building("landlord-a", "beta", nil)

	// Renaming to the own name (another case) is fine, to another's is not.
	w := e.call(http.MethodPut, "/v1/buildings/"+e.building("landlord-a", "Gamma", nil).ID.String(),
		map[string]any{"name": "alpha", "address_detail": "x"}, "landlord-a")
	expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestBuildingChargesSync(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "A", map[string]any{
		"charges": []map[string]any{charge("Power", "electricity", 3500, "per_kwh"), charge("Water", "water", 20000, "per_cubic_meter")},
	})
	power, water := b.Charges[0], b.Charges[1]
	put := func(charges any) BuildingDetail {
		w := e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), map[string]any{"name": "A", "address_detail": "x", "charges": charges}, "landlord-a")
		expectStatus(t, w, http.StatusOK)
		return decodeData[BuildingDetail](t, w)
	}

	// Known id updates in place, a new item is created, the unlisted one disappears; positions follow the list.
	got := put([]map[string]any{
		{"name": "Brand new", "kind": "service", "amount_vnd": 1, "unit": "one_time"},
		{"id": power.ID, "name": "Electricity", "kind": "electricity", "amount_vnd": 4000, "unit": "per_kwh", "note": "  metered "},
	})
	if len(got.Charges) != 2 || got.Charges[0].Name != "Brand new" || got.Charges[0].DisplayOrder != 1 {
		t.Fatalf("charges %+v", got.Charges)
	}
	if got.Charges[1].ID != power.ID || got.Charges[1].Name != "Electricity" || got.Charges[1].AmountVND != 4000 ||
		got.Charges[1].Note == nil || *got.Charges[1].Note != "metered" {
		t.Errorf("the known id must keep its row: %+v", got.Charges[1])
	}
	if slices.ContainsFunc(got.Charges, func(c ChargeView) bool { return c.ID == water.ID }) {
		t.Error("an unlisted charge is deleted")
	}

	// A foreign id creates instead of touching another owner's charge.
	other := e.building("landlord-b", "B", map[string]any{"charges": []map[string]any{charge("Mine", "water", 1, "per_period")}})
	got = put([]map[string]any{{"id": other.Charges[0].ID, "name": "Stolen", "kind": "water", "amount_vnd": 2, "unit": "per_period"}})
	if len(got.Charges) != 1 || got.Charges[0].ID == other.Charges[0].ID {
		t.Errorf("foreign id must create: %+v", got.Charges)
	}
	if after := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+other.ID.String(), nil, "landlord-b")); after.Charges[0].Name != "Mine" {
		t.Error("the other landlord's charge was changed")
	}

	if got = put([]map[string]any{}); len(got.Charges) != 0 {
		t.Errorf("an empty list removes every charge, got %d", len(got.Charges))
	}
}

func TestBuildingListFiltersSortAndPages(t *testing.T) {
	e := newPropertyEnv(t, nil)
	e.building("landlord-a", "Charlie", map[string]any{"ward_code": e.ward})
	e.building("landlord-a", "alpha", nil)
	e.building("landlord-a", "Bravo 50%", nil)
	e.building("landlord-b", "Zulu", nil)

	list := func(query string) listBody {
		w := e.call(http.MethodGet, "/v1/buildings"+query, nil, "landlord-a")
		expectStatus(t, w, http.StatusOK)
		var body listBody
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	names := func(b listBody) []string {
		var out []string
		for _, x := range b.Data {
			out = append(out, x.Name)
		}
		return out
	}

	all := list("")
	if all.Meta.Total != 3 || all.Meta.Page != 1 || all.Meta.PerPage != 20 || !slices.Equal(names(all), []string{"Charlie", "alpha", "Bravo 50%"}) {
		t.Errorf("default list %v %+v", names(all), all.Meta)
	}
	if got := names(list("?sort=name")); !slices.Equal(got, []string{"alpha", "Bravo 50%", "Charlie"}) {
		t.Errorf("sort=name %v", got)
	}
	if got := names(list("?sort=-name")); !slices.Equal(got, []string{"Charlie", "Bravo 50%", "alpha"}) {
		t.Errorf("sort=-name %v", got)
	}
	if got := names(list("?name=ALP")); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("name filter %v", got)
	}
	if got := names(list("?name=%25")); !slices.Equal(got, []string{"Bravo 50%"}) {
		t.Errorf("a percent sign is text, not a wildcard: %v", got)
	}
	if got := names(list("?ward_code=4")); !slices.Equal(got, []string{"Charlie"}) {
		t.Errorf("ward filter %v", got)
	}
	if got := names(list("?province_code=1")); !slices.Equal(got, []string{"Charlie"}) {
		t.Errorf("province filter %v", got)
	}
	page := list("?per_page=2&page=2")
	if page.Meta.Total != 3 || len(page.Data) != 1 || page.Data[0].Name != "Bravo 50%" {
		t.Errorf("page 2 %v %+v", names(page), page.Meta)
	}
	for _, q := range []string{"?sort=colour", "?ward_code=abc", "?per_page=0", "?page=x"} {
		expectError(t, e.call(http.MethodGet, "/v1/buildings"+q, nil, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
}

func TestBuildingDeleteIsBlockedByRooms(t *testing.T) {
	e := newPropertyEnv(t, nil)
	full := e.building("landlord-a", "Full", nil)
	empty := e.building("landlord-a", "Empty", nil)
	room := e.room(e.landlA, full.ID, "P1", false)

	body := expectError(t, e.call(http.MethodDelete, "/v1/buildings/"+full.ID.String(), nil, "landlord-a"), http.StatusConflict, apperr.CodeBuildingHasRooms)
	if body.Error.Message != "This building still has 1 rooms. Move or delete them first." {
		t.Errorf("message %q", body.Error.Message)
	}

	w := e.call(http.MethodPost, "/v1/buildings/bulk-delete", map[string]any{"ids": []uuid.UUID{full.ID, empty.ID}}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	result := decodeData[bulkResultView](t, w)
	if !slices.Equal(result.Done, []uuid.UUID{empty.ID}) || len(result.Failed) != 1 || result.Failed[0].ID != full.ID ||
		result.Failed[0].Name != "Full" || result.Failed[0].Code != apperr.CodeBuildingHasRooms || result.Failed[0].Message == "" {
		t.Errorf("bulk result %+v", result)
	}
	if list := decodeData[[]BuildingSummary](t, e.call(http.MethodGet, "/v1/buildings", nil, "landlord-a")); len(list) != 1 || list[0].RoomsCount != 1 {
		t.Errorf("only the full building stays: %+v", list)
	}

	// Soft-deleting the room frees the building.
	if _, err := e.pool.Exec(context.Background(), `UPDATE rooms SET deleted_at = now() WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+full.ID.String(), nil, "landlord-a"), http.StatusNoContent)

	// An id that is not mine fails the whole request.
	foreign := e.building("landlord-b", "Foreign", nil)
	mine := e.building("landlord-a", "Mine", nil)
	body = expectError(t, e.call(http.MethodPost, "/v1/buildings/bulk-delete", map[string]any{"ids": []uuid.UUID{mine.ID, foreign.ID}}, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if _, ok := body.Error.Fields["ids"]; !ok {
		t.Errorf("fields %v", body.Error.Fields)
	}
	for name, ids := range map[string]any{"none": []uuid.UUID{}, "duplicate": []uuid.UUID{mine.ID, mine.ID}, "too many": make([]uuid.UUID, 101)} {
		t.Run(name, func(t *testing.T) {
			expectError(t, e.call(http.MethodPost, "/v1/buildings/bulk-delete", map[string]any{"ids": ids}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		})
	}
	if got := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+mine.ID.String(), nil, "landlord-a")); got.ID != mine.ID {
		t.Error("a refused bulk request deletes nothing")
	}
}

func TestBuildingDeleteRemovesItsPictures(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "Pics", nil)
	ctx := context.Background()
	for _, key := range []string{"property/2026/11/a.webp", "property/2026/11/a_thumb.jpg"} {
		if err := e.files.Put(ctx, key, []byte("x"), "image/webp"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO media (building_id, file_key, thumb_key, content_type, size_bytes, width, height, display_order)
		VALUES ($1, 'property/2026/11/a.webp', 'property/2026/11/a_thumb.jpg', 'image/webp', 1, 10, 10, 1)`, b.ID); err != nil {
		t.Fatal(err)
	}
	got := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+b.ID.String(), nil, "landlord-a"))
	if len(got.Media) != 1 || got.Media[0].URL != "http://files.test/v1/media/property/2026/11/a.webp" {
		t.Errorf("media %+v", got.Media)
	}

	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+b.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("files must be deleted with the building, left %v", keys)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM media`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("media rows %d %v", rows, err)
	}
}

func TestBuildingReorder(t *testing.T) {
	e := newPropertyEnv(t, nil)
	a := e.building("landlord-a", "A", nil)
	b := e.building("landlord-a", "B", nil)
	c := e.building("landlord-a", "C", nil)
	other := e.building("landlord-b", "Other", nil)

	order := func() []uuid.UUID {
		var ids []uuid.UUID
		for _, x := range decodeData[[]BuildingSummary](t, e.call(http.MethodGet, "/v1/buildings", nil, "landlord-a")) {
			ids = append(ids, x.ID)
		}
		return ids
	}
	expectStatus(t, e.call(http.MethodPut, "/v1/buildings/order", map[string]any{"ids": []uuid.UUID{c.ID, a.ID, b.ID}}, "landlord-a"), http.StatusNoContent)
	if got := order(); !slices.Equal(got, []uuid.UUID{c.ID, a.ID, b.ID}) {
		t.Errorf("order %v", got)
	}
	// Only the moved buildings swap places.
	expectStatus(t, e.call(http.MethodPut, "/v1/buildings/order", map[string]any{"ids": []uuid.UUID{b.ID, c.ID}}, "landlord-a"), http.StatusNoContent)
	if got := order(); !slices.Equal(got, []uuid.UUID{b.ID, a.ID, c.ID}) {
		t.Errorf("after the swap %v", got)
	}
	body := expectError(t, e.call(http.MethodPut, "/v1/buildings/order", map[string]any{"ids": []uuid.UUID{a.ID, other.ID}}, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if _, ok := body.Error.Fields["ids"]; !ok {
		t.Errorf("fields %v", body.Error.Fields)
	}
	// A new building goes last, even after the first one was deleted.
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+c.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	d := e.building("landlord-a", "D", nil)
	if d.DisplayOrder <= 3 {
		t.Errorf("a new building must not reuse a deleted position, got %d", d.DisplayOrder)
	}
}

func TestBuildingPlanLimit(t *testing.T) {
	limit := 1
	e := newPropertyEnv(t, fakePlans{buildings: &limit})
	first := e.building("landlord-a", "First", nil)
	body := expectError(t, e.call(http.MethodPost, "/v1/buildings", map[string]any{"name": "Second", "address_detail": "x"}, "landlord-a"),
		http.StatusConflict, apperr.CodeBuildingLimit)
	if body.Error.Message != "Your plan allows 1 buildings." {
		t.Errorf("message %q", body.Error.Message)
	}
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+first.ID.String()+"/copy", map[string]any{"name": "Copy"}, "landlord-a"),
		http.StatusConflict, apperr.CodeBuildingLimit)
	// The limit is per landlord.
	e.building("landlord-b", "Mine", nil)
	// Deleting frees a slot.
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+first.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	e.building("landlord-a", "Second", nil)
}

func TestBuildingPriceCannotBeRemovedWhileRoomsFollowIt(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "A", map[string]any{"price_vnd": 2_000_000})
	e.room(e.landlA, b.ID, "P1", true)
	e.room(e.landlA, b.ID, "P2", false)

	body := expectError(t, e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), map[string]any{"name": "A", "address_detail": "x"}, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if msgs := body.Error.Fields["price_vnd"]; len(msgs) != 1 || msgs[0] != "1 rooms follow this rent." {
		t.Errorf("fields %v", body.Error.Fields)
	}
	// Changing the price is fine.
	expectStatus(t, e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), map[string]any{"name": "A", "address_detail": "x", "price_vnd": 2_200_000}, "landlord-a"), http.StatusOK)
}

func TestCopyBuilding(t *testing.T) {
	e := newPropertyEnv(t, nil)
	src := e.building("landlord-a", "Source", map[string]any{
		"ward_code": e.ward, "description": "Quiet", "price_vnd": 2_500_000, "area_square_meters": 20,
		"minimum_rental_period": 2, "rental_period_unit": "day",
		"charges": []map[string]any{charge("Power", "electricity", 3500, "per_kwh")},
	})
	e.room(e.landlA, src.ID, "P1", false)

	w := e.call(http.MethodPost, "/v1/buildings/"+src.ID.String()+"/copy", map[string]any{"name": "Source copy"}, "landlord-a")
	expectStatus(t, w, http.StatusCreated)
	result := decodeData[CopyResult](t, w)
	cp := result.Building
	if result.RoomsCopied != 0 || cp.ID == src.ID || cp.Name != "Source copy" || cp.DisplayOrder != 2 || cp.RoomsCount != 0 {
		t.Errorf("copy %+v rooms_copied %d", cp.BuildingSummaryView, result.RoomsCopied)
	}
	if cp.Ward == nil || cp.AddressDetail != "12 Le Loi" || *cp.PriceVND != 2_500_000 || *cp.AreaSquareMeters != 20 ||
		*cp.MinimumRentalPeriod != 2 || *cp.RentalPeriodUnit != "day" || *cp.Description != "Quiet" {
		t.Errorf("details not copied: %+v", cp.BuildingSummaryView)
	}
	if len(cp.Charges) != 1 || cp.Charges[0].Name != "Power" || cp.Charges[0].ID == src.Charges[0].ID {
		t.Errorf("charges %+v", cp.Charges)
	}

	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+src.ID.String()+"/copy", map[string]any{"name": "source COPY"}, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+src.ID.String()+"/copy", map[string]any{}, "landlord-a"),
		http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestEnsureFirstBuildingOnlyOnce(t *testing.T) {
	e := newPropertyEnv(t, nil)
	ctx := context.Background()
	first := usecase.FirstBuilding{Name: "Nhà trọ đầu tiên", WardCode: &e.ward, AddressDetail: "12 Le Loi"}

	created, err := e.buildings.EnsureFirstBuilding(ctx, e.landlA, first)
	if err != nil || !created {
		t.Fatalf("created %v err %v", created, err)
	}
	if created, err = e.buildings.EnsureFirstBuilding(ctx, e.landlA, first); err != nil || created {
		t.Fatalf("a second call must do nothing: %v %v", created, err)
	}
	list := decodeData[[]BuildingSummary](t, e.call(http.MethodGet, "/v1/buildings", nil, "landlord-a"))
	if len(list) != 1 || list[0].Name != "Nhà trọ đầu tiên" || list[0].DisplayOrder != 1 || list[0].Ward == nil {
		t.Fatalf("list %+v", list)
	}

	// Deleting it does not earn a new one.
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+list[0].ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if created, err = e.buildings.EnsureFirstBuilding(ctx, e.landlA, first); err != nil || created {
		t.Fatalf("after a delete: %v %v", created, err)
	}
	// Another landlord is unaffected.
	if created, err = e.buildings.EnsureFirstBuilding(ctx, e.landlB, first); err != nil || !created {
		t.Fatalf("landlord b: %v %v", created, err)
	}
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	e := newPropertyEnv(t, nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := e.seeder.SeedDemo(ctx, e.landlA); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
	}
	list := decodeData[[]BuildingSummary](t, e.call(http.MethodGet, "/v1/buildings", nil, "landlord-a"))
	if len(list) != 1 || list[0].Name != "Nhà trọ Demo" || *list[0].PriceVND != 2_500_000 {
		t.Fatalf("list %+v", list)
	}
	detail := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+list[0].ID.String(), nil, "landlord-a"))
	if len(detail.Charges) != 3 || list[0].RoomsCount != 6 {
		t.Errorf("charges %+v rooms %d", detail.Charges, list[0].RoomsCount)
	}
	rooms := e.listRooms("landlord-a", "")
	if rooms.Meta.Total != 7 || !slices.Equal(roomNamesOf(rooms), []string{"P101", "P102", "P103", "P104", "P105", "P106", "A1"}) {
		t.Fatalf("rooms %v", roomNamesOf(rooms))
	}
	p101, p106, a1 := e.getRoom("landlord-a", rooms.Data[0].ID), e.getRoom("landlord-a", rooms.Data[5].ID), e.getRoom("landlord-a", rooms.Data[6].ID)
	if p101.PriceVND != 2_500_000 || len(p101.Inherits) != 6 || len(p101.Charges) != 3 || p106.PriceVND != 3_000_000 || slices.Contains(p106.Inherits, "price") ||
		a1.Building != nil || a1.Status != "maintenance" || a1.PriceVND != 1_800_000 || a1.AddressDetail != "45 Trần Phú" {
		t.Errorf("rooms %+v %+v %+v", p101.RoomSummaryView, p106.RoomSummaryView, a1.RoomSummaryView)
	}
}

func TestMalformedBodies(t *testing.T) {
	e := newPropertyEnv(t, nil)
	w := e.call(http.MethodPost, "/v1/buildings", "not an object", "landlord-a")
	expectError(t, w, http.StatusBadRequest, apperr.CodeRequestMalformed)
	// Money is whole dong.
	w = e.call(http.MethodPost, "/v1/buildings", map[string]any{"name": "A", "address_detail": "x", "price_vnd": 1.5}, "landlord-a")
	expectError(t, w, http.StatusBadRequest, apperr.CodeRequestMalformed)
}

// followers creates the rooms of a propagation test: one follows everything, one has its own rent and charges, one
// stands in the same building with its own values for everything.
func (e *propertyEnv) followers(b BuildingDetail) (all, own RoomDetail) {
	all = e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "All"})
	own = e.roomAPI("landlord-a", map[string]any{
		"building_id": b.ID, "name": "Own", "price_vnd": 4_000_000, "area_square_meters": 11,
		"minimum_rental_period": 9, "rental_period_unit": "day", "description": "Mine",
		"charges": []map[string]any{charge("Mine", "service", 1, "per_period")},
	})
	return all, own
}

func TestBuildingEditReachesOnlyTheRoomsThatFollow(t *testing.T) {
	e := newPropertyEnv(t, nil)
	rec := &recorder{}
	e.observer.set(rec)
	b := e.fullBuilding("landlord-a", "Nha A")
	all, own := e.followers(b)
	put := func(body map[string]any) BuildingDetail {
		base := map[string]any{"name": "Nha A", "address_detail": "12 Le Loi", "ward_code": e.ward, "description": "Quiet", "price_vnd": 2_500_000,
			"area_square_meters": 20, "minimum_rental_period": 2, "rental_period_unit": "month"}
		for k, v := range body {
			base[k] = v
		}
		w := e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), base, "landlord-a")
		expectStatus(t, w, http.StatusOK)
		return decodeData[BuildingDetail](t, w)
	}

	// Saving the same values reaches nobody.
	put(nil)
	if len(rec.content) != 0 {
		t.Errorf("no change, got %v", rec.content)
	}

	put(map[string]any{"price_vnd": 2_700_000, "description": "Noisy", "area_square_meters": 25, "minimum_rental_period": 3, "rental_period_unit": "day"})
	got := e.getRoom("landlord-a", all.ID)
	if got.PriceVND != 2_700_000 || *got.AreaSquareMeters != 25 || got.MinimumRentalPeriod != 3 || got.RentalPeriodUnit != "day" || *got.Description != "Noisy" {
		t.Errorf("the follower takes the new values: %+v", got)
	}
	mine := e.getRoom("landlord-a", own.ID)
	if mine.PriceVND != 4_000_000 || *mine.AreaSquareMeters != 11 || mine.MinimumRentalPeriod != 9 || *mine.Description != "Mine" {
		t.Errorf("a room with its own values is left alone: %+v", mine)
	}
	if len(rec.content) != 1 || !slices.Equal(rec.content[0], []uuid.UUID{all.ID}) {
		t.Errorf("only the follower is reported, once: %v", rec.content)
	}

	// The address goes to every room of the building, followers or not.
	put(map[string]any{"price_vnd": 2_700_000, "description": "Noisy", "area_square_meters": 25, "minimum_rental_period": 3, "rental_period_unit": "day",
		"address_detail": "99 Tran Phu", "ward_code": nil})
	for _, id := range []uuid.UUID{all.ID, own.ID} {
		if r := e.getRoom("landlord-a", id); r.AddressDetail != "99 Tran Phu" || r.Ward != nil {
			t.Errorf("address of %s: %+v", r.Name, r.RoomSummaryView)
		}
	}

	// A removed area or description reaches the followers as "none"; a removed rental period is not pushed.
	put(map[string]any{"price_vnd": 2_700_000, "address_detail": "99 Tran Phu", "ward_code": nil, "description": nil,
		"area_square_meters": nil, "minimum_rental_period": nil, "rental_period_unit": nil})
	got = e.getRoom("landlord-a", all.ID)
	if got.AreaSquareMeters != nil || got.Description != nil || got.MinimumRentalPeriod != 3 || got.RentalPeriodUnit != "day" {
		t.Errorf("after removing: %+v", got)
	}
	// A building that follows rooms cannot lose the rent.
	expectError(t, e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), map[string]any{"name": "Nha A", "address_detail": "x"}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestBuildingChargesReachTheFollowingRooms(t *testing.T) {
	e := newPropertyEnv(t, nil)
	rec := &recorder{}
	e.observer.set(rec)
	b := e.fullBuilding("landlord-a", "Nha A")
	all, own := e.followers(b)
	power, water := b.Charges[0], b.Charges[1]
	copyOfPower := all.Charges[0]
	put := func(charges []map[string]any) {
		w := e.call(http.MethodPut, "/v1/buildings/"+b.ID.String(), map[string]any{
			"name": "Nha A", "address_detail": "12 Le Loi", "ward_code": e.ward, "description": "Quiet", "price_vnd": 2_500_000,
			"area_square_meters": 20, "minimum_rental_period": 2, "rental_period_unit": "month", "charges": charges}, "landlord-a")
		expectStatus(t, w, http.StatusOK)
	}

	// Power is renamed (its copy keeps its id), Water is dropped, Wifi is new.
	put([]map[string]any{
		{"id": power.ID, "name": "Electricity", "kind": "electricity", "amount_vnd": 4000, "unit": "per_kwh"},
		charge("Wifi", "internet", 100, "per_period"),
	})
	got := e.getRoom("landlord-a", all.ID)
	names := []string{}
	for _, c := range got.Charges {
		names = append(names, c.Name)
		if !c.FollowsBuilding {
			t.Errorf("%s must still follow the building", c.Name)
		}
	}
	if !slices.Equal(names, []string{"Electricity", "Wifi"}) || got.Charges[0].ID != copyOfPower.ID || got.Charges[0].AmountVND != 4000 {
		t.Errorf("follower charges %+v", got.Charges)
	}
	if mine := e.getRoom("landlord-a", own.ID); len(mine.Charges) != 1 || mine.Charges[0].Name != "Mine" || mine.Charges[0].FollowsBuilding {
		t.Errorf("a room with its own charges is left alone: %+v", mine.Charges)
	}
	if len(rec.content) != 1 || !slices.Equal(rec.content[0], []uuid.UUID{all.ID}) {
		t.Errorf("only the follower is reported: %v", rec.content)
	}

	// The same list again: nothing changes, nobody is told.
	rec.content = nil
	now := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+b.ID.String(), nil, "landlord-a"))
	var same []map[string]any
	for _, c := range now.Charges {
		same = append(same, map[string]any{"id": c.ID, "name": c.Name, "kind": c.Kind, "amount_vnd": c.AmountVND, "unit": c.Unit})
	}
	put(same)
	if len(rec.content) != 0 {
		t.Errorf("identical charges: %v", rec.content)
	}
	if now.Charges[0].ID == water.ID {
		t.Error("the dropped template is gone")
	}
	// An empty list removes the copies too.
	put([]map[string]any{})
	if got = e.getRoom("landlord-a", all.ID); len(got.Charges) != 0 {
		t.Errorf("copies of dropped templates go: %+v", got.Charges)
	}
}

func TestCreateRoomsInBuildingRun(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.fullBuilding("landlord-a", "Nha A")
	post := func(bearer string, id uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/buildings/"+id.String()+"/rooms", body, bearer)
	}

	w := post("landlord-a", b.ID, map[string]any{"name_prefix": " P", "from": 101, "to": 105})
	expectStatus(t, w, http.StatusCreated)
	run := decodeData[RunResult](t, w)
	if run.Count != 5 || len(run.IDs) != 5 {
		t.Fatalf("run %+v", run)
	}
	list := e.listRooms("landlord-a", "?building_id="+b.ID.String())
	if !slices.Equal(roomNamesOf(list), []string{"P101", "P102", "P103", "P104", "P105"}) {
		t.Errorf("names %v", roomNamesOf(list))
	}
	first := e.getRoom("landlord-a", run.IDs[0])
	if first.ID != list.Data[0].ID || first.AddressDetail != "12 Le Loi" || first.PriceVND != 2_500_000 || len(first.Inherits) != 6 || len(first.Charges) != 2 || !first.Charges[0].FollowsBuilding {
		t.Errorf("a run room follows the building: %+v", first)
	}
	// Charges are copies per room, not shared.
	if other := e.getRoom("landlord-a", run.IDs[1]); other.Charges[0].ID == first.Charges[0].ID {
		t.Error("each room has its own copies")
	}

	// Own values and padding; own charges stop following.
	w = post("landlord-a", b.ID, map[string]any{"name_prefix": "Q", "from": 8, "to": 10, "padding": 3, "status": "maintenance", "price_vnd": 1_000_000,
		"charges": []map[string]any{charge("Wifi", "internet", 1, "per_period")}})
	expectStatus(t, w, http.StatusCreated)
	q8 := e.getRoom("landlord-a", decodeData[RunResult](t, w).IDs[0])
	if q8.Name != "Q008" || q8.Status != "maintenance" || q8.PriceVND != 1_000_000 || slices.Contains(q8.Inherits, "price") || slices.Contains(q8.Inherits, "charges") ||
		len(q8.Charges) != 1 || q8.Charges[0].FollowsBuilding {
		t.Errorf("own values %+v charges %+v", q8.RoomSummaryView, q8.Charges)
	}

	// One taken name creates none of the run.
	before := e.listRooms("landlord-a", "").Meta.Total
	body := expectError(t, post("landlord-a", b.ID, map[string]any{"name_prefix": "P", "from": 105, "to": 107}), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if msgs := body.Error.Fields["name_prefix"]; len(msgs) != 1 || msgs[0] != "These room names are already used: P105." {
		t.Errorf("fields %v", body.Error.Fields)
	}
	if after := e.listRooms("landlord-a", "").Meta.Total; after != before {
		t.Errorf("a refused run creates nothing: %d -> %d", before, after)
	}
	for name, req := range map[string]map[string]any{
		"too many":   {"name_prefix": "R", "from": 1, "to": 101},
		"backwards":  {"name_prefix": "R", "from": 5, "to": 4},
		"name long":  {"name_prefix": string(make([]byte, 50)), "from": 1, "to": 1},
		"no range":   {"name_prefix": "R"},
		"bad status": {"from": 1, "to": 1, "status": "rented"},
		"huge":       {"from": 1, "to": 1_000_000},
	} {
		t.Run(name, func(t *testing.T) {
			expectError(t, post("landlord-a", b.ID, req), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		})
	}
	if full := e.listRooms("landlord-a", "").Meta.Total; full != before {
		t.Errorf("refused runs create nothing: %d", full)
	}
	// A building without rent: the rooms must bring their own.
	bare := e.building("landlord-a", "Bare", nil)
	body = expectError(t, post("landlord-a", bare.ID, map[string]any{"from": 1, "to": 2}), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if _, ok := body.Error.Fields["price_vnd"]; !ok {
		t.Errorf("fields %v", body.Error.Fields)
	}
	expectStatus(t, post("landlord-a", bare.ID, map[string]any{"from": 1, "to": 2, "price_vnd": 5}), http.StatusCreated)
}

func TestCreateRoomsInBuildingHonoursThePlan(t *testing.T) {
	limit := 3
	e := newPropertyEnv(t, fakePlans{rooms: &limit})
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/rooms", map[string]any{"from": 1, "to": 4}, "landlord-a"), http.StatusConflict, apperr.CodeRoomLimit)
	expectStatus(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/rooms", map[string]any{"from": 1, "to": 3}, "landlord-a"), http.StatusCreated)
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/rooms", map[string]any{"from": 9, "to": 9}, "landlord-a"), http.StatusConflict, apperr.CodeRoomLimit)
	// A copy with rooms counts the rooms it would add.
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/copy", map[string]any{"name": "Copy", "include_rooms": true}, "landlord-a"), http.StatusConflict, apperr.CodeRoomLimit)
	expectStatus(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/copy", map[string]any{"name": "Copy"}, "landlord-a"), http.StatusCreated)
}

func TestCopyBuildingWithRooms(t *testing.T) {
	e := newPropertyEnv(t, nil)
	src := e.fullBuilding("landlord-a", "Source")
	all, own := e.followers(src)
	maintenance := e.roomAPI("landlord-a", map[string]any{"building_id": src.ID, "name": "Fix", "status": "maintenance"})
	rented := e.roomAPI("landlord-a", map[string]any{"building_id": src.ID, "name": "Busy", "status": "rented"})
	deleted := e.roomAPI("landlord-a", map[string]any{"building_id": src.ID, "name": "Gone"})
	expectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+deleted.ID.String(), nil, "landlord-a"), http.StatusNoContent)

	w := e.call(http.MethodPost, "/v1/buildings/"+src.ID.String()+"/copy", map[string]any{"name": "Copy", "include_rooms": true}, "landlord-a")
	expectStatus(t, w, http.StatusCreated)
	result := decodeData[CopyResult](t, w)
	cp := result.Building
	if result.RoomsCopied != 4 || cp.RoomsCount != 4 || cp.AvailableRooms != 3 {
		t.Fatalf("copied %d: %+v", result.RoomsCopied, cp.BuildingSummaryView)
	}
	rooms := e.listRooms("landlord-a", "?building_id="+cp.ID.String())
	byName := map[string]RoomSummary{}
	for _, r := range rooms.Data {
		byName[r.Name] = r
	}
	if len(byName) != 4 || byName["Gone"].ID != uuid.Nil {
		t.Fatalf("names %v", roomNamesOf(rooms))
	}
	if byName["Fix"].Status != "maintenance" || byName["Busy"].Status != "available" || byName["All"].Status != "available" {
		t.Errorf("statuses: Fix %s Busy %s All %s", byName["Fix"].Status, byName["Busy"].Status, byName["All"].Status)
	}
	if byName["All"].ID == all.ID || byName["Fix"].ID == maintenance.ID || byName["Busy"].ID == rented.ID {
		t.Error("copies are new rooms")
	}

	// A follower follows the new building's charges (copies of its templates); a room with its own keeps its values and charges.
	newAll := e.getRoom("landlord-a", byName["All"].ID)
	if len(newAll.Charges) != 2 || !newAll.Charges[0].FollowsBuilding || newAll.Charges[0].ID == all.Charges[0].ID || len(newAll.Inherits) != 6 {
		t.Errorf("follower %+v charges %+v", newAll.RoomSummaryView, newAll.Charges)
	}
	if newAll.Charges[0].Name != cp.Charges[0].Name {
		t.Errorf("charges come from the new building: %+v", newAll.Charges)
	}
	newOwn := e.getRoom("landlord-a", byName["Own"].ID)
	if newOwn.ID == own.ID || newOwn.PriceVND != 4_000_000 || *newOwn.Description != "Mine" || len(newOwn.Charges) != 1 || newOwn.Charges[0].Name != "Mine" || newOwn.Charges[0].FollowsBuilding || newOwn.Charges[0].ID == own.Charges[0].ID {
		t.Errorf("own room %+v charges %+v", newOwn.RoomSummaryView, newOwn.Charges)
	}
	if newOwn.DisplayOrder <= rented.DisplayOrder {
		t.Errorf("copies take later positions: %d", newOwn.DisplayOrder)
	}
	// The copy is independent: editing it does not reach the source's rooms.
	put := map[string]any{"name": "Copy", "address_detail": "12 Le Loi", "price_vnd": 9_000_000}
	expectStatus(t, e.call(http.MethodPut, "/v1/buildings/"+cp.ID.String(), put, "landlord-a"), http.StatusOK)
	if got := e.getRoom("landlord-a", all.ID); got.PriceVND != 2_500_000 {
		t.Errorf("source room changed: %d", got.PriceVND)
	}
	if got := e.getRoom("landlord-a", byName["All"].ID); got.PriceVND != 9_000_000 {
		t.Errorf("copied follower should follow the copy: %d", got.PriceVND)
	}
	// Another landlord's building cannot be copied.
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+src.ID.String()+"/copy", map[string]any{"name": "X", "include_rooms": true}, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
}
