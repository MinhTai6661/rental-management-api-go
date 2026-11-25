package stats

import (
	"errors"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func ptr(t time.Time) *time.Time { return &t }

func TestPercentIsNilWithoutAWhole(t *testing.T) {
	if Percent(5, 0) != nil || Percent(0, 0) != nil || Percent(1, -3) != nil {
		t.Error("no whole, no percentage (never 0)")
	}
	cases := []struct {
		part, whole int64
		want        float64
	}{{0, 10, 0}, {1, 3, 33.3}, {2, 3, 66.7}, {10, 10, 100}, {3, 2, 150}}
	for _, c := range cases {
		if got := Percent(c.part, c.whole); got == nil || *got != c.want {
			t.Errorf("Percent(%d,%d) = %v, want %v", c.part, c.whole, got, c.want)
		}
	}
}

func TestChange(t *testing.T) {
	if Change(10, 0) != nil {
		t.Error("nothing before, no change")
	}
	cases := []struct {
		cur, prev int64
		want      float64
	}{{150, 100, 50}, {50, 100, -50}, {100, 100, 0}, {4, 3, 33.3}}
	for _, c := range cases {
		if got := Change(c.cur, c.prev); got == nil || *got != c.want {
			t.Errorf("Change(%d,%d) = %v, want %v", c.cur, c.prev, got, c.want)
		}
	}
}

func TestDayIsTheCalendarDayInVietnam(t *testing.T) {
	// 18:30 UTC on 30 Nov is 01:30 on 1 Dec in Vietnam.
	if got := Day(time.Date(2026, 11, 30, 18, 30, 0, 0, time.UTC)); !got.Equal(day(2026, 12, 1)) {
		t.Errorf("got %v", got)
	}
}

func TestMonthRange(t *testing.T) {
	r, err := NewMonthRange(day(2025, 11, 20), day(2026, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range r.Months() {
		got = append(got, m.String())
	}
	want := []string{"2025-11", "2025-12", "2026-01", "2026-02"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
	if !r.First().Equal(day(2025, 11, 1)) || !r.Last().Equal(day(2026, 2, 28)) {
		t.Errorf("bounds %v %v", r.First(), r.Last())
	}
	if r.Index("2026-01") != 2 || r.Index("2027-01") != -1 {
		t.Error("index")
	}
}

func TestMonthRangeLimits(t *testing.T) {
	if _, err := NewMonthRange(day(2026, 3, 1), day(2026, 2, 28)); !errors.Is(err, ErrRangeBackwards) {
		t.Errorf("%v", err)
	}
	if _, err := NewMonthRange(day(2026, 3, 31), day(2026, 3, 1)); err != nil {
		t.Errorf("the same month is a range: %v", err)
	}
	// 36 months from 2023-12 to 2026-11 are allowed, 37 are not.
	if r, err := NewMonthRange(day(2023, 12, 1), day(2026, 11, 30)); err != nil || len(r.Months()) != 36 {
		t.Errorf("%v %v", err, len(r.Months()))
	}
	if _, err := NewMonthRange(day(2023, 11, 1), day(2026, 11, 30)); !errors.Is(err, ErrRangeTooLong) {
		t.Errorf("%v", err)
	}
}

func TestDefaultRangeIsTheLastTwelveMonths(t *testing.T) {
	r := DefaultRange(day(2026, 11, 25))
	if len(r.Months()) != 12 || r.Months()[0].String() != "2025-12" || r.Months()[11].String() != "2026-11" {
		t.Errorf("%v", r.Months())
	}
	if !r.First().Equal(day(2025, 12, 1)) {
		t.Error(r.First())
	}
}

func TestDaysCountsADayOnce(t *testing.T) {
	// 5..10 and 8..15 overlap: 5..15 is 11 days; the window 1..12 keeps 5..12 = 8.
	spans := []Span{{From: day(2026, 1, 5), To: ptr(day(2026, 1, 10))}, {From: day(2026, 1, 8), To: ptr(day(2026, 1, 15))}}
	if got := Days(spans, day(2026, 1, 1), day(2026, 1, 31)); got != 11 {
		t.Errorf("got %d, want 11", got)
	}
	if got := Days(spans, day(2026, 1, 1), day(2026, 1, 12)); got != 8 {
		t.Errorf("got %d, want 8", got)
	}
	// An open span runs to the end of the window.
	if got := Days([]Span{{From: day(2026, 1, 20)}}, day(2026, 1, 1), day(2026, 1, 31)); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
	if got := Days(spans, day(2026, 2, 1), day(2026, 2, 28)); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestOccupancyOfARoomStopsAtToday(t *testing.T) {
	room := RoomLife{
		Existed: Span{From: day(2026, 1, 1)},
		Rented:  []Span{{From: day(2026, 1, 11), To: ptr(day(2026, 1, 20))}},
	}
	// January, today is 25 Jan: 25 days available, 10 rented.
	got := OfRoom(room, day(2026, 1, 1), day(2026, 1, 31), day(2026, 1, 25))
	if got.Available != 25 || got.Occupied != 10 {
		t.Errorf("%+v", got)
	}
	if r := got.Rate(); r == nil || *r != 40 {
		t.Errorf("rate %v", r)
	}
	// A month after today has nothing.
	if got := OfRoom(room, day(2026, 2, 1), day(2026, 2, 28), day(2026, 1, 25)); got != (Occupancy{}) || got.Rate() != nil {
		t.Errorf("%+v", got)
	}
}

func TestOccupancyOfARoomThatWasDeletedOrBorn(t *testing.T) {
	// Exists 10..20 Jan only; rented all the time: 11 available, 11 occupied.
	room := RoomLife{Existed: Span{From: day(2026, 1, 10), To: ptr(day(2026, 1, 20))}, Rented: []Span{{From: day(2025, 1, 1)}}}
	got := OfRoom(room, day(2026, 1, 1), day(2026, 1, 31), day(2026, 3, 1))
	if got.Available != 11 || got.Occupied != 11 {
		t.Errorf("%+v", got)
	}
}

func TestOccupancyOfMonthsAddsTheRooms(t *testing.T) {
	a := RoomLife{Existed: Span{From: day(2026, 1, 1)}, Rented: []Span{{From: day(2026, 1, 1)}}}                             // always rented
	b := RoomLife{Existed: Span{From: day(2026, 1, 1)}, Rented: []Span{{From: day(2026, 2, 15), To: ptr(day(2026, 2, 28))}}} // 14 days of Feb
	months := []Month{{2026, 1}, {2026, 2}}
	got := OfMonths([]RoomLife{a, b}, months, day(2026, 2, 28))
	// Jan: 31+31 available, 31 occupied. Feb: 28+28 available, 28+14 occupied.
	if got[0] != (Occupancy{Occupied: 31, Available: 62}) || got[1] != (Occupancy{Occupied: 42, Available: 56}) {
		t.Errorf("%+v", got)
	}
	if r := got[1].Rate(); r == nil || *r != 75 {
		t.Errorf("rate %v", r)
	}
}

func TestParseSort(t *testing.T) {
	if k, err := ParseSort(""); err != nil || k != SortBilled {
		t.Error("default is billed")
	}
	if k, err := ParseSort("occupancy"); err != nil || k != SortOccupancy {
		t.Error("occupancy")
	}
	if _, err := ParseSort("name"); !errors.Is(err, ErrUnknownSort) {
		t.Error("unknown")
	}
}

func TestMonthlyRecurringSpreadsAYearlyPlan(t *testing.T) {
	plans := []PlanFacts{
		{Code: "free", PriceVND: 0, BillingInterval: "month", Subscribers: 40},
		{Code: "pro", PriceVND: 100_000, BillingInterval: "month", Subscribers: 3},       // 300,000
		{Code: "vip-year", PriceVND: 1_200_000, BillingInterval: "year", Subscribers: 2}, // 2,400,000 / 12 = 200,000
	}
	if got := MonthlyRecurring(plans); got != 500_000 {
		t.Errorf("got %d, want 500000", got)
	}
	if got := PaidSubscribers(plans); got != 5 {
		t.Errorf("got %d, want 5 (the free plan is not paid)", got)
	}
}

func TestAContractThatEndedWithoutADateEndedToday(t *testing.T) {
	today := day(2026, 11, 25)
	ended := RentedContract{StartsOn: day(2026, 1, 1), Ended: true}.Span(today)
	if ended.To == nil || !ended.To.Equal(today) {
		t.Errorf("%+v", ended)
	}
	if open := (RentedContract{StartsOn: day(2026, 1, 1)}).Span(today); open.To != nil {
		t.Errorf("an active contract with no end is open: %+v", open)
	}
}
