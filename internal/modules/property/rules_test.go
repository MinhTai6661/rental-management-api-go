package property

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"rental-management/pkg/apperr"
)

func ptr[T any](v T) *T { return &v }

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.Must(uuid.NewV7())
	}
	return out
}

func TestResolveInheritance(t *testing.T) {
	all := Groups()
	tests := []struct {
		name       string
		requested  *[]Group
		carried    map[Group]bool
		inBuilding bool
		want       []Group
	}{
		{"only price carried", nil, map[Group]bool{GroupPrice: true}, true,
			[]Group{GroupArea, GroupRentalPeriod, GroupDescription, GroupCharges, GroupMedia}},
		{"nothing carried", nil, nil, true, all},
		{"requested order is normalised", ptr([]Group{GroupMedia, GroupPrice}), nil, true, []Group{GroupPrice, GroupMedia}},
		{"requested empty", ptr([]Group{}), nil, true, []Group{}},
		{"unknown requested group is ignored", ptr([]Group{"colour", GroupArea}), nil, true, []Group{GroupArea}},
		{"not in a building", ptr([]Group{GroupPrice}), nil, false, []Group{}},
		{"not in a building, nothing carried", nil, nil, false, []Group{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveInheritance(tc.requested, tc.carried, tc.inBuilding); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCarriedGroups(t *testing.T) {
	tests := []struct {
		name    string
		in      roomInput
		current *roomState
		want    map[Group]bool
	}{
		{"create with nothing", roomInput{}, nil, map[Group]bool{}},
		{"create with a blank description", roomInput{Description: ptr("  ")}, nil, map[Group]bool{}},
		{"create with a description", roomInput{Description: ptr("Quiet")}, nil, map[Group]bool{GroupDescription: true}},
		{"create with charges", roomInput{Charges: []chargeInput{}}, nil, map[Group]bool{GroupCharges: true}},
		{"unit alone is a rental period", roomInput{PeriodUnit: ptr("day")}, nil, map[Group]bool{GroupRentalPeriod: true}},
		{"update of a room with its own media", roomInput{}, &roomState{InheritsMedia: false},
			map[Group]bool{GroupMedia: true, GroupCharges: true}},
		{"update without charges, room follows charges", roomInput{}, &roomState{InheritsCharges: true, InheritsMedia: true},
			map[Group]bool{}},
		{"update without charges, room has its own", roomInput{}, &roomState{InheritsMedia: true},
			map[Group]bool{GroupCharges: true}},
		{"update with charges while following", roomInput{Charges: []chargeInput{}}, &roomState{InheritsCharges: true, InheritsMedia: true},
			map[Group]bool{GroupCharges: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := carriedGroups(tc.in, tc.current)
			for _, g := range Groups() {
				if got[g] != tc.want[g] {
					t.Errorf("%s: got %v, want %v", g, got[g], tc.want[g])
				}
			}
		})
	}
}

func TestEffectiveValues(t *testing.T) {
	building := &buildingDefaults{
		WardCode: ptr(int32(4)), AddressDetail: "12 Le Loi", PriceVND: ptr(int64(2_500_000)), Area: ptr(20.0),
		MinPeriod: ptr(int32(3)), PeriodUnit: ptr("month"), Description: ptr("Quiet"),
	}

	t.Run("a room following everything takes the building's values", func(t *testing.T) {
		v, fields := effectiveValues(building, Groups(), roomInput{AddressDetail: ptr("typed, ignored")})
		if len(fields) != 0 {
			t.Fatal(fields)
		}
		if v.PriceVND != 2_500_000 || *v.Area != 20 || v.MinPeriod != 3 || *v.Description != "Quiet" || v.AddressDetail != "12 Le Loi" {
			t.Errorf("unexpected values %+v", v)
		}
	})
	t.Run("own price, the rest followed", func(t *testing.T) {
		v, fields := effectiveValues(building, []Group{GroupArea}, roomInput{PriceVND: ptr(int64(3_000_000))})
		if len(fields) != 0 || v.PriceVND != 3_000_000 || *v.Area != 20 || v.MinPeriod != 1 || v.PeriodUnit != UnitMonth {
			t.Errorf("unexpected %+v %v", v, fields)
		}
	})
	t.Run("a followed price the building does not set", func(t *testing.T) {
		_, fields := effectiveValues(&buildingDefaults{AddressDetail: "x"}, []Group{GroupPrice}, roomInput{})
		if len(fields) != 1 || fields[0].Name != "price_vnd" || fields[0].Rule != apperr.RuleBuildingPriceMissing {
			t.Errorf("got %v", fields)
		}
	})
	t.Run("rental period of a building that sets none falls back to the room's own, then the default", func(t *testing.T) {
		bare := &buildingDefaults{AddressDetail: "x", PriceVND: ptr(int64(1))}
		v, _ := effectiveValues(bare, []Group{GroupPrice, GroupRentalPeriod}, roomInput{MinPeriod: ptr(int32(6))})
		if v.MinPeriod != 6 || v.PeriodUnit != UnitMonth {
			t.Errorf("own value kept: %+v", v)
		}
		v, _ = effectiveValues(bare, []Group{GroupPrice, GroupRentalPeriod}, roomInput{})
		if v.MinPeriod != 1 || v.PeriodUnit != UnitMonth {
			t.Errorf("default: %+v", v)
		}
	})
	t.Run("outside a building the address and price are required", func(t *testing.T) {
		_, fields := effectiveValues(nil, nil, roomInput{AddressDetail: ptr("   ")})
		names := []string{}
		for _, f := range fields {
			names = append(names, f.Name+":"+f.Rule)
		}
		slices.Sort(names)
		if want := []string{"address_detail:required", "price_vnd:required"}; !slices.Equal(names, want) {
			t.Errorf("got %v, want %v", names, want)
		}
	})
	t.Run("one half of the period is completed with the default", func(t *testing.T) {
		v, fields := effectiveValues(nil, nil, roomInput{
			AddressDetail: ptr("1 A"), PriceVND: ptr(int64(5)), PeriodUnit: ptr(UnitDay),
		})
		if len(fields) != 0 || v.MinPeriod != 1 || v.PeriodUnit != UnitDay {
			t.Errorf("got %+v %v", v, fields)
		}
	})
}

func TestRoomNames(t *testing.T) {
	tests := []struct {
		name              string
		prefix            string
		from, to, padding int
		want              []string
		failure           string
	}{
		{"simple run", "P", 101, 103, 0, []string{"P101", "P102", "P103"}, ""},
		{"prefix is trimmed", " A-", 5, 5, 0, []string{"A-5"}, ""},
		{"padding", "P", 8, 10, 3, []string{"P008", "P009", "P010"}, ""},
		{"backwards", "P", 5, 4, 0, nil, rangeInvalid},
		{"negative", "P", -1, 3, 0, nil, rangeInvalid},
		{"too many", "P", 1, 101, 0, nil, rangeTooMany},
		{"exactly the maximum", "P", 1, 100, 0, nil, ""},
		{"name too long", strings.Repeat("x", 50), 1, 1, 0, nil, rangeNameTooLong},
		{"vietnamese prefix is trimmed and counts characters", "Phòng ", 1, 2, 0, []string{"Phòng1", "Phòng2"}, ""},
		{"fifty characters of accents fit", strings.Repeat("ế", 49), 1, 1, 0, []string{strings.Repeat("ế", 49) + "1"}, ""},
		{"wider numbers are not cut", "P", 1234, 1234, 2, []string{"P1234"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, failure := roomNames(tc.prefix, tc.from, tc.to, tc.padding)
			if failure != tc.failure {
				t.Fatalf("failure %q, want %q", failure, tc.failure)
			}
			if tc.want != nil && !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if tc.name == "exactly the maximum" && len(got) != 100 {
				t.Errorf("want 100 names, got %d", len(got))
			}
		})
	}
}

func TestReorder(t *testing.T) {
	x := ids(3)
	a, b, c := x[0], x[1], x[2]
	order := func(rows ...position) map[uuid.UUID]int {
		m := map[uuid.UUID]int{}
		for _, r := range rows {
			m[r.ID] = r.Order
		}
		return m
	}
	current := []orderedID{{a, 1}, {b, 2}, {c, 3}}

	t.Run("swap two", func(t *testing.T) {
		got := order(reorder(current, []uuid.UUID{c, a})...)
		if len(got) != 2 || got[c] != 1 || got[a] != 3 {
			t.Errorf("got %v (b must stay at 2)", got)
		}
	})
	t.Run("move all", func(t *testing.T) {
		got := order(reorder(current, []uuid.UUID{c, a, b})...)
		if got[c] != 1 || got[a] != 2 || got[b] != 3 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("nothing moved renumbers the gaps", func(t *testing.T) {
		got := reorder([]orderedID{{a, 1}, {b, 5}}, nil)
		if len(got) != 1 || got[0].ID != b || got[0].Order != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("a single moved id changes nothing", func(t *testing.T) {
		if got := reorder(current, []uuid.UUID{b}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("an id that is not in the list is ignored", func(t *testing.T) {
		if got := reorder(current, []uuid.UUID{uuid.Must(uuid.NewV7())}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
}

func TestStatusAndDeleteRules(t *testing.T) {
	if got := checkStatusChange(StatusAvailable, Blocker{ActiveContract: true}); got != apperr.CodeRoomStatusLocked {
		t.Errorf("got %q", got)
	}
	if got := checkStatusChange(StatusRented, Blocker{ActiveContract: true}); got != "" {
		t.Errorf("staying rented is fine, got %q", got)
	}
	if got := checkStatusChange(StatusMaintenance, Blocker{OpenListing: true}); got != "" {
		t.Errorf("a listing does not lock the status, got %q", got)
	}
	if got := checkDelete(Blocker{ActiveContract: true, OpenListing: true}); got != apperr.CodeRoomHasContract {
		t.Errorf("got %q", got)
	}
	if got := checkDelete(Blocker{OpenListing: true}); got != apperr.CodeRoomHasListing {
		t.Errorf("got %q", got)
	}
	if got := checkDelete(Blocker{}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestPropagation(t *testing.T) {
	base := buildingDefaults{
		WardCode: ptr(int32(1)), AddressDetail: "A", PriceVND: ptr(int64(2_500_000)), Area: ptr(20.0),
		MinPeriod: ptr(int32(1)), PeriodUnit: ptr(UnitMonth), Description: ptr("old"),
	}
	with := func(f func(b *buildingDefaults)) buildingDefaults {
		b := base
		f(&b)
		return b
	}
	tests := []struct {
		name  string
		after buildingDefaults
		want  []Group
	}{
		{"nothing changed", base, nil},
		{"price changed", with(func(b *buildingDefaults) { b.PriceVND = ptr(int64(2_700_000)) }), []Group{GroupPrice}},
		{"price removed is not pushed", with(func(b *buildingDefaults) { b.PriceVND = nil }), nil},
		{"area removed is pushed", with(func(b *buildingDefaults) { b.Area = nil }), []Group{GroupArea}},
		{"period changed", with(func(b *buildingDefaults) { b.MinPeriod = ptr(int32(3)) }), []Group{GroupRentalPeriod}},
		{"period removed is not pushed", with(func(b *buildingDefaults) { b.MinPeriod, b.PeriodUnit = nil, nil }), nil},
		{"description removed is pushed", with(func(b *buildingDefaults) { b.Description = nil }), []Group{GroupDescription}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := propagation(base, tc.after)
			var groups []Group
			for _, g := range Groups() {
				if got[g] {
					groups = append(groups, g)
				}
			}
			if !slices.Equal(groups, tc.want) {
				t.Errorf("got %v, want %v", groups, tc.want)
			}
		})
	}
	if addressChanged(base, base) {
		t.Error("same address")
	}
	if !addressChanged(base, with(func(b *buildingDefaults) { b.AddressDetail = "B" })) ||
		!addressChanged(base, with(func(b *buildingDefaults) { b.WardCode = nil })) {
		t.Error("a moved address must be reported")
	}
}

func TestPlanChargeSync(t *testing.T) {
	x := ids(4)
	existing := []chargeRow{{ID: x[0]}, {ID: x[1]}, {ID: x[2]}}
	item := func(name string, id *uuid.UUID) chargeInput { return chargeInput{ID: id, Name: name} }

	t.Run("update a known id, create a new one, delete the rest", func(t *testing.T) {
		updates, creates, deletes := planChargeSync(existing, []chargeInput{item("new", nil), item("kept", &x[1])})
		if len(updates) != 1 || updates[0].ID != x[1] || updates[0].Order != 2 {
			t.Errorf("updates %v", updates)
		}
		if len(creates) != 1 || creates[0].Input.Name != "new" || creates[0].Order != 1 {
			t.Errorf("creates %v", creates)
		}
		if !slices.Equal(deletes, []uuid.UUID{x[0], x[2]}) {
			t.Errorf("deletes %v", deletes)
		}
	})
	t.Run("an empty list deletes everything", func(t *testing.T) {
		updates, creates, deletes := planChargeSync(existing, []chargeInput{})
		if len(updates) != 0 || len(creates) != 0 || len(deletes) != 3 {
			t.Errorf("%v %v %v", updates, creates, deletes)
		}
	})
	t.Run("a foreign id creates", func(t *testing.T) {
		updates, creates, deletes := planChargeSync(existing, []chargeInput{item("x", &x[3])})
		if len(updates) != 0 || len(creates) != 1 || len(deletes) != 3 {
			t.Errorf("%v %v %v", updates, creates, deletes)
		}
	})
	t.Run("an id listed twice updates once", func(t *testing.T) {
		updates, creates, _ := planChargeSync(existing, []chargeInput{item("a", &x[0]), item("b", &x[0])})
		if len(updates) != 1 || len(creates) != 1 {
			t.Errorf("%v %v", updates, creates)
		}
	})
}

func TestPlanFollow(t *testing.T) {
	x := ids(8)
	power, water, internet := chargeRow{ID: x[0]}, chargeRow{ID: x[1]}, chargeRow{ID: x[2]}
	copyPower := chargeRow{ID: x[3], TemplateID: &power.ID}
	copyOld := chargeRow{ID: x[4], TemplateID: &x[7]} // template removed
	own := chargeRow{ID: x[5]}

	update, insert, deletes := planFollow([]chargeRow{power, water, internet}, []chargeRow{copyPower, copyOld, own})
	if update[copyPower.ID] != power.ID || len(update) != 1 {
		t.Errorf("the copy of a kept template is updated in place: %v", update)
	}
	if !slices.Equal(insert, []uuid.UUID{water.ID, internet.ID}) {
		t.Errorf("insert %v", insert)
	}
	if !slices.Equal(deletes, []uuid.UUID{copyOld.ID, own.ID}) {
		t.Errorf("deletes %v", deletes)
	}
}

func TestPickCover(t *testing.T) {
	x := ids(3)
	if pickCover(nil) != nil {
		t.Error("no pictures, no cover")
	}
	media := []mediaRow{{x[0], KindPhoto, 2}, {x[1], KindPhoto, 1}, {x[2], KindCover, 3}}
	if got := pickCover(media); got.ID != x[2] {
		t.Errorf("the cover kind wins, got %v", got.ID)
	}
	if got := pickCover(media[:2]); got.ID != x[1] {
		t.Errorf("else the lowest order, got %v", got.ID)
	}
}

func TestCheckArrangement(t *testing.T) {
	x := ids(3)
	cur := []uuid.UUID{x[0], x[1]}
	tests := []struct {
		name  string
		items []arrangeItem
		want  string
	}{
		{"same set", []arrangeItem{{x[1], KindCover}, {x[0], KindPhoto}}, ""},
		{"missing", []arrangeItem{{x[0], KindPhoto}}, apperr.RuleExists},
		{"extra", []arrangeItem{{x[0], KindPhoto}, {x[1], KindPhoto}, {x[2], KindPhoto}}, apperr.RuleExists},
		{"foreign", []arrangeItem{{x[0], KindPhoto}, {x[2], KindPhoto}}, apperr.RuleExists},
		{"duplicate", []arrangeItem{{x[0], KindPhoto}, {x[0], KindPhoto}}, apperr.RuleExists},
		{"two covers", []arrangeItem{{x[0], KindCover}, {x[1], KindCover}}, apperr.RuleSingleCover},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkArrangement(cur, tc.items); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimitReached(t *testing.T) {
	tests := []struct {
		name   string
		limit  *int
		count  int
		adding int
		want   bool
	}{
		{"no limit", nil, 1000, 5, false},
		{"room for one more", ptr(3), 2, 1, false},
		{"at the limit", ptr(3), 3, 1, true},
		{"a run that would end at four", ptr(3), 1, 3, true},
		{"a run that exactly fits", ptr(3), 0, 3, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := limitReached(tc.limit, tc.count, tc.adding); got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestEscapeLikeAndRoundArea(t *testing.T) {
	if got := escapeLike(`50%_a\b`); got != `50\%\_a\\b` {
		t.Errorf("got %q", got)
	}
	if got := roundArea(18.456); got != 18.46 {
		t.Errorf("got %v", got)
	}
}
