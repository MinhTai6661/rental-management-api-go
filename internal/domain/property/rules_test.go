package property

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
			if got := ResolveInheritance(tc.requested, tc.carried, tc.inBuilding); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCarriedGroups(t *testing.T) {
	tests := []struct {
		name    string
		in      RoomFields
		current *RoomState
		want    map[Group]bool
	}{
		{"create with nothing", RoomFields{}, nil, map[Group]bool{}},
		{"create with a blank description", RoomFields{Description: ptr("  ")}, nil, map[Group]bool{}},
		{"create with a description", RoomFields{Description: ptr("Quiet")}, nil, map[Group]bool{GroupDescription: true}},
		{"create with charges", RoomFields{Charges: []ChargeInput{}}, nil, map[Group]bool{GroupCharges: true}},
		{"unit alone is a rental period", RoomFields{PeriodUnit: ptr("day")}, nil, map[Group]bool{GroupRentalPeriod: true}},
		{"update of a room with its own media", RoomFields{}, &RoomState{InheritsMedia: false},
			map[Group]bool{GroupMedia: true, GroupCharges: true}},
		{"update without charges, room follows charges", RoomFields{}, &RoomState{InheritsCharges: true, InheritsMedia: true},
			map[Group]bool{}},
		{"update without charges, room has its own", RoomFields{}, &RoomState{InheritsMedia: true},
			map[Group]bool{GroupCharges: true}},
		{"update with charges while following", RoomFields{Charges: []ChargeInput{}}, &RoomState{InheritsCharges: true, InheritsMedia: true},
			map[Group]bool{GroupCharges: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CarriedGroups(tc.in, tc.current)
			for _, g := range Groups() {
				if got[g] != tc.want[g] {
					t.Errorf("%s: got %v, want %v", g, got[g], tc.want[g])
				}
			}
		})
	}
}

func TestEffectiveValues(t *testing.T) {
	building := &BuildingDefaults{
		WardCode: ptr(int32(4)), AddressDetail: "12 Le Loi", PriceVND: ptr(int64(2_500_000)), Area: ptr(20.0),
		MinPeriod: ptr(int32(3)), PeriodUnit: ptr("month"), Description: ptr("Quiet"),
	}

	t.Run("a room following everything takes the building's values", func(t *testing.T) {
		v, fields := EffectiveValues(building, Groups(), RoomFields{AddressDetail: ptr("typed, ignored")})
		if len(fields) != 0 {
			t.Fatal(fields)
		}
		if v.PriceVND != 2_500_000 || *v.Area != 20 || v.MinPeriod != 3 || *v.Description != "Quiet" || v.AddressDetail != "12 Le Loi" {
			t.Errorf("unexpected values %+v", v)
		}
	})
	t.Run("own price, the rest followed", func(t *testing.T) {
		v, fields := EffectiveValues(building, []Group{GroupArea}, RoomFields{PriceVND: ptr(int64(3_000_000))})
		if len(fields) != 0 || v.PriceVND != 3_000_000 || *v.Area != 20 || v.MinPeriod != 1 || v.PeriodUnit != UnitMonth {
			t.Errorf("unexpected %+v %v", v, fields)
		}
	})
	t.Run("a followed price the building does not set", func(t *testing.T) {
		_, fields := EffectiveValues(&BuildingDefaults{AddressDetail: "x"}, []Group{GroupPrice}, RoomFields{})
		if len(fields) != 1 || fields[0].Name != "price_vnd" || fields[0].Rule != RuleBuildingPriceMissing {
			t.Errorf("got %v", fields)
		}
	})
	t.Run("rental period of a building that sets none falls back to the room's own, then the default", func(t *testing.T) {
		bare := &BuildingDefaults{AddressDetail: "x", PriceVND: ptr(int64(1))}
		v, _ := EffectiveValues(bare, []Group{GroupPrice, GroupRentalPeriod}, RoomFields{MinPeriod: ptr(int32(6))})
		if v.MinPeriod != 6 || v.PeriodUnit != UnitMonth {
			t.Errorf("own value kept: %+v", v)
		}
		v, _ = EffectiveValues(bare, []Group{GroupPrice, GroupRentalPeriod}, RoomFields{})
		if v.MinPeriod != 1 || v.PeriodUnit != UnitMonth {
			t.Errorf("default: %+v", v)
		}
	})
	t.Run("outside a building the address and price are required", func(t *testing.T) {
		_, fields := EffectiveValues(nil, nil, RoomFields{AddressDetail: ptr("   ")})
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
		v, fields := EffectiveValues(nil, nil, RoomFields{
			AddressDetail: ptr("1 A"), PriceVND: ptr(int64(5)), PeriodUnit: ptr(UnitDay),
		})
		if len(fields) != 0 || v.MinPeriod != 1 || v.PeriodUnit != UnitDay {
			t.Errorf("got %+v %v", v, fields)
		}
	})
}

// sameFailure compares two errors by value: the typed ones carry the limit that is reported.
func sameFailure(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	if errors.Is(got, want) {
		return true
	}
	var many, wantMany *RangeTooManyError
	var long, wantLong *RangeNameTooLongError
	switch {
	case errors.As(want, &wantMany):
		return errors.As(got, &many) && *many == *wantMany
	case errors.As(want, &wantLong):
		return errors.As(got, &long) && *long == *wantLong
	}
	return false
}

func TestRoomNames(t *testing.T) {
	tests := []struct {
		name              string
		prefix            string
		from, to, padding int
		want              []string
		failure           error
	}{
		{"simple run", "P", 101, 103, 0, []string{"P101", "P102", "P103"}, nil},
		{"prefix is trimmed", " A-", 5, 5, 0, []string{"A-5"}, nil},
		{"padding", "P", 8, 10, 3, []string{"P008", "P009", "P010"}, nil},
		{"backwards", "P", 5, 4, 0, nil, ErrRangeInvalid},
		{"negative", "P", -1, 3, 0, nil, ErrRangeInvalid},
		{"too many", "P", 1, 101, 0, nil, &RangeTooManyError{Max: 100}},
		{"exactly the maximum", "P", 1, 100, 0, nil, nil},
		{"name too long", strings.Repeat("x", 50), 1, 1, 0, nil, &RangeNameTooLongError{Max: 50}},
		{"vietnamese prefix is trimmed and counts characters", "Phòng ", 1, 2, 0, []string{"Phòng1", "Phòng2"}, nil},
		{"fifty characters of accents fit", strings.Repeat("ế", 49), 1, 1, 0, []string{strings.Repeat("ế", 49) + "1"}, nil},
		{"wider numbers are not cut", "P", 1234, 1234, 2, []string{"P1234"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, failure := RoomNames(tc.prefix, tc.from, tc.to, tc.padding)
			if !sameFailure(failure, tc.failure) {
				t.Fatalf("failure %v, want %v", failure, tc.failure)
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
	order := func(rows ...Position) map[uuid.UUID]int {
		m := map[uuid.UUID]int{}
		for _, r := range rows {
			m[r.ID] = r.Order
		}
		return m
	}
	current := []OrderedID{{a, 1}, {b, 2}, {c, 3}}

	t.Run("swap two", func(t *testing.T) {
		got := order(Reorder(current, []uuid.UUID{c, a})...)
		if len(got) != 2 || got[c] != 1 || got[a] != 3 {
			t.Errorf("got %v (b must stay at 2)", got)
		}
	})
	t.Run("move all", func(t *testing.T) {
		got := order(Reorder(current, []uuid.UUID{c, a, b})...)
		if got[c] != 1 || got[a] != 2 || got[b] != 3 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("nothing moved renumbers the gaps", func(t *testing.T) {
		got := Reorder([]OrderedID{{a, 1}, {b, 5}}, nil)
		if len(got) != 1 || got[0].ID != b || got[0].Order != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("a single moved id changes nothing", func(t *testing.T) {
		if got := Reorder(current, []uuid.UUID{b}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("an id that is not in the list is ignored", func(t *testing.T) {
		if got := Reorder(current, []uuid.UUID{uuid.Must(uuid.NewV7())}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
}

func TestStatusAndDeleteRules(t *testing.T) {
	if got := CheckStatusChange(StatusAvailable, Blocker{ActiveContract: true}); !errors.Is(got, ErrRoomStatusLocked) {
		t.Errorf("got %v", got)
	}
	if got := CheckStatusChange(StatusRented, Blocker{ActiveContract: true}); got != nil {
		t.Errorf("staying rented is fine, got %v", got)
	}
	if got := CheckStatusChange(StatusMaintenance, Blocker{OpenListing: true}); got != nil {
		t.Errorf("a listing does not lock the status, got %v", got)
	}
	if got := CheckDelete(Blocker{ActiveContract: true, OpenListing: true}); !errors.Is(got, ErrRoomHasContract) {
		t.Errorf("got %v", got)
	}
	if got := CheckDelete(Blocker{OpenListing: true}); !errors.Is(got, ErrRoomHasListing) {
		t.Errorf("got %v", got)
	}
	if got := CheckDelete(Blocker{}); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestPropagation(t *testing.T) {
	base := BuildingDefaults{
		WardCode: ptr(int32(1)), AddressDetail: "A", PriceVND: ptr(int64(2_500_000)), Area: ptr(20.0),
		MinPeriod: ptr(int32(1)), PeriodUnit: ptr(UnitMonth), Description: ptr("old"),
	}
	with := func(f func(b *BuildingDefaults)) BuildingDefaults {
		b := base
		f(&b)
		return b
	}
	tests := []struct {
		name  string
		after BuildingDefaults
		want  []Group
	}{
		{"nothing changed", base, nil},
		{"price changed", with(func(b *BuildingDefaults) { b.PriceVND = ptr(int64(2_700_000)) }), []Group{GroupPrice}},
		{"price removed is not pushed", with(func(b *BuildingDefaults) { b.PriceVND = nil }), nil},
		{"area removed is pushed", with(func(b *BuildingDefaults) { b.Area = nil }), []Group{GroupArea}},
		{"period changed", with(func(b *BuildingDefaults) { b.MinPeriod = ptr(int32(3)) }), []Group{GroupRentalPeriod}},
		{"period removed is not pushed", with(func(b *BuildingDefaults) { b.MinPeriod, b.PeriodUnit = nil, nil }), nil},
		{"description removed is pushed", with(func(b *BuildingDefaults) { b.Description = nil }), []Group{GroupDescription}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Propagation(base, tc.after)
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
	if AddressChanged(base, base) {
		t.Error("same address")
	}
	if !AddressChanged(base, with(func(b *BuildingDefaults) { b.AddressDetail = "B" })) ||
		!AddressChanged(base, with(func(b *BuildingDefaults) { b.WardCode = nil })) {
		t.Error("a moved address must be reported")
	}
}

func TestPlanChargeSync(t *testing.T) {
	x := ids(4)
	existing := []ChargeRow{{ID: x[0]}, {ID: x[1]}, {ID: x[2]}}
	item := func(name string, id *uuid.UUID) ChargeInput { return ChargeInput{ID: id, Name: name} }

	t.Run("update a known id, create a new one, delete the rest", func(t *testing.T) {
		updates, creates, deletes := PlanChargeSync(existing, []ChargeInput{item("new", nil), item("kept", &x[1])})
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
		updates, creates, deletes := PlanChargeSync(existing, []ChargeInput{})
		if len(updates) != 0 || len(creates) != 0 || len(deletes) != 3 {
			t.Errorf("%v %v %v", updates, creates, deletes)
		}
	})
	t.Run("a foreign id creates", func(t *testing.T) {
		updates, creates, deletes := PlanChargeSync(existing, []ChargeInput{item("x", &x[3])})
		if len(updates) != 0 || len(creates) != 1 || len(deletes) != 3 {
			t.Errorf("%v %v %v", updates, creates, deletes)
		}
	})
	t.Run("an id listed twice updates once", func(t *testing.T) {
		updates, creates, _ := PlanChargeSync(existing, []ChargeInput{item("a", &x[0]), item("b", &x[0])})
		if len(updates) != 1 || len(creates) != 1 {
			t.Errorf("%v %v", updates, creates)
		}
	})
}

func TestPlanFollow(t *testing.T) {
	x := ids(8)
	power, water, internet := ChargeRow{ID: x[0]}, ChargeRow{ID: x[1]}, ChargeRow{ID: x[2]}
	copyPower := ChargeRow{ID: x[3], TemplateID: &power.ID}
	copyOld := ChargeRow{ID: x[4], TemplateID: &x[7]} // template removed
	own := ChargeRow{ID: x[5]}

	update, insert, deletes := PlanFollow([]ChargeRow{power, water, internet}, []ChargeRow{copyPower, copyOld, own})
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
	if PickCover(nil) != nil {
		t.Error("no pictures, no cover")
	}
	media := []MediaRow{{x[0], KindPhoto, 2}, {x[1], KindPhoto, 1}, {x[2], KindCover, 3}}
	if got := PickCover(media); got.ID != x[2] {
		t.Errorf("the cover kind wins, got %v", got.ID)
	}
	if got := PickCover(media[:2]); got.ID != x[1] {
		t.Errorf("else the lowest order, got %v", got.ID)
	}
}

func TestCheckArrangement(t *testing.T) {
	x := ids(3)
	cur := []uuid.UUID{x[0], x[1]}
	tests := []struct {
		name  string
		items []ArrangeItem
		want  error
	}{
		{"same set", []ArrangeItem{{x[1], KindCover}, {x[0], KindPhoto}}, nil},
		{"missing", []ArrangeItem{{x[0], KindPhoto}}, ErrArrangementMismatch},
		{"extra", []ArrangeItem{{x[0], KindPhoto}, {x[1], KindPhoto}, {x[2], KindPhoto}}, ErrArrangementMismatch},
		{"foreign", []ArrangeItem{{x[0], KindPhoto}, {x[2], KindPhoto}}, ErrArrangementMismatch},
		{"duplicate", []ArrangeItem{{x[0], KindPhoto}, {x[0], KindPhoto}}, ErrArrangementMismatch},
		{"two covers", []ArrangeItem{{x[0], KindCover}, {x[1], KindCover}}, ErrArrangementManyCovers},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckArrangement(cur, tc.items); !errors.Is(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
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
			if got := LimitReached(tc.limit, tc.count, tc.adding); got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestEscapeLikeAndRoundArea(t *testing.T) {
	if got := EscapeLike(`50%_a\b`); got != `50\%\_a\\b` {
		t.Errorf("got %q", got)
	}
	if got := RoundArea(18.456); got != 18.46 {
		t.Errorf("got %v", got)
	}
}

func TestMediaKeys(t *testing.T) {
	var random [16]byte
	for i := range random {
		random[i] = byte(i)
	}
	file, thumb := MediaKeys(time.Date(2026, time.March, 5, 10, 0, 0, 0, time.UTC), random, "webp")
	if file != "property/2026/03/000102030405060708090a0b0c0d0e0f.webp" || thumb != "property/2026/03/000102030405060708090a0b0c0d0e0f_thumb.webp" {
		t.Fatalf("got %s %s", file, thumb)
	}
}
