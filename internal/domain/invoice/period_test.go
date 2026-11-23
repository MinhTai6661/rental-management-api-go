package invoice

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func dayPtr(s string) *time.Time { d := day(s); return &d }

func ds(d time.Time) string { return d.Format(DateLayout) }

func TestPeriodsBillsAMonthlyStayOncePerStartedCalendarMonth(t *testing.T) {
	periods := Periods(day("2026-01-15"), nil, false, day("2026-03-20"))
	if len(periods) != 3 || ds(periods[0].Start) != "2026-01-15" || ds(periods[0].End) != "2026-02-14" || ds(periods[2].Start) != "2026-03-15" {
		t.Fatalf("periods: %+v", periods)
	}
	for i, p := range periods {
		if p.Units != 1 || p.Index != i {
			t.Errorf("period %d: %+v", i, p)
		}
	}
}

func TestPeriodsDoesNotOverflowAMonthCountingFromThe31st(t *testing.T) {
	periods := Periods(day("2026-01-31"), nil, false, day("2026-03-31"))
	if len(periods) != 3 || ds(periods[1].Start) != "2026-02-28" || ds(periods[2].Start) != "2026-03-31" {
		t.Fatalf("periods: %+v", periods)
	}
}

func TestPeriodsReturnsNothingForAStayThatHasNotStarted(t *testing.T) {
	if got := Periods(day("2026-06-01"), nil, false, day("2026-05-01")); len(got) != 0 || got == nil {
		t.Fatalf("want an empty list, got %#v", got)
	}
	if got := Periods(day("2026-06-01"), dayPtr("2026-06-11"), true, day("2026-05-01")); len(got) != 0 {
		t.Fatalf("by the day: %#v", got)
	}
}

func TestPeriodsBillsAStayByTheDayWithAnEndDateOnceForAllItsDays(t *testing.T) {
	periods := Periods(day("2026-05-01"), dayPtr("2026-05-11"), true, day("2026-05-02"))
	if len(periods) != 1 || periods[0].Units != 10 || ds(periods[0].End) != "2026-05-11" {
		t.Fatalf("periods: %+v", periods)
	}
}

func TestPeriodsBillsAStayByTheDayWithoutAnEndDatePerMonthCountingDays(t *testing.T) {
	periods := Periods(day("2026-02-01"), nil, true, day("2026-03-05"))
	if len(periods) != 2 || periods[0].Units != 28 || periods[1].Units != 31 {
		t.Fatalf("periods: %+v", periods)
	}
}

func TestPeriodsCutsTheLastPeriodAtTheEndDate(t *testing.T) {
	periods := Periods(day("2026-01-01"), dayPtr("2026-02-10"), false, day("2026-03-01"))
	if len(periods) != 2 || ds(periods[1].End) != "2026-02-10" {
		t.Fatalf("periods: %+v", periods)
	}
}

func TestPeriodsStopsAtTheSafetyLimit(t *testing.T) {
	if got := Periods(day("1900-01-01"), nil, false, day("2026-01-01")); len(got) != maxPeriods {
		t.Fatalf("got %d periods", len(got))
	}
}

func TestResolvePeriod(t *testing.T) {
	contract := ContractFacts{StartsOn: day("2026-01-15"), EndsOn: dayPtr("2027-01-15")}
	periods := PeriodsOf(contract, day("2026-03-20"))
	billed := map[time.Time]bool{day("2026-01-15"): true}

	cases := []struct {
		name      string
		requested *time.Time
		want      string
		reason    string
	}{
		{"the oldest period without a bill when nothing is asked", nil, "2026-02-15", ""},
		{"the one asked for", dayPtr("2026-03-15"), "2026-03-15", ""},
		{"a billed period is still resolved", dayPtr("2026-01-15"), "2026-01-15", ""},
		{"a date that starts no period", dayPtr("2026-03-16"), "", ReasonInvalidPeriod},
		{"a period that has not started", dayPtr("2026-04-15"), "", ReasonInvalidPeriod},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ResolvePeriod(contract, periods, tc.requested, billed)
			if tc.reason != "" {
				var v *RuleViolation
				if !errors.As(err, &v) || v.Reason != tc.reason || v.Field != FieldPeriodStart {
					t.Fatalf("want %s, got %v", tc.reason, err)
				}
				return
			}
			if err != nil || ds(p.Start) != tc.want {
				t.Fatalf("got %v, %v", p, err)
			}
		})
	}

	all := map[time.Time]bool{}
	for _, p := range periods {
		all[p.Start] = true
	}
	if p, err := ResolvePeriod(contract, periods, nil, all); err != nil || ds(p.Start) != "2026-03-15" {
		t.Errorf("with everything billed the last period is picked, got %v, %v", p, err)
	}
}

func TestResolvePeriodSaysWhyThereIsNothingToBill(t *testing.T) {
	byDayOpen := ContractFacts{StartsOn: day("2026-06-01"), BilledByDay: true}
	byDayClosed := ContractFacts{StartsOn: day("2026-06-01"), EndsOn: dayPtr("2026-06-05"), BilledByDay: true}
	monthly := ContractFacts{StartsOn: day("2026-06-01")}
	for name, tc := range map[string]struct {
		c      ContractFacts
		reason string
	}{
		"a stay by the day needs its end date":   {byDayOpen, ReasonDayNeedsEnd},
		"a stay by the day that has not started": {byDayClosed, ReasonNothingToBill},
		"a monthly stay that has not started":    {monthly, ReasonNothingToBill},
	} {
		_, err := ResolvePeriod(tc.c, PeriodsOf(tc.c, day("2026-05-01")), nil, nil)
		var v *RuleViolation
		if !errors.As(err, &v) || v.Reason != tc.reason || v.Field != FieldPeriodStart {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestDefaultNameAndDates(t *testing.T) {
	if got := DefaultName(day("2026-04-15"), "P101"); got != "04/2026 - P101" {
		t.Errorf("got %q", got)
	}
	loc := time.FixedZone("ICT", 7*3600)
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC) // 01:00 on the 23rd in Vietnam
	if got := ds(Today(now, loc)); got != "2026-11-23" {
		t.Errorf("today: %s", got)
	}
	end := EndOfDay(day("2026-11-23"), loc)
	if end.Hour() != 23 || end.Minute() != 59 || StartOfDay(day("2026-11-23"), loc).Add(24*time.Hour).Sub(end) != time.Microsecond {
		t.Errorf("end of day: %v", end)
	}
	if got := ds(AddMonths(day("2026-01-31"), 1)); got != "2026-02-28" {
		t.Errorf("add months: %s", got)
	}
	if MonthKey(day("2026-04-15")) != "2026-04" {
		t.Error("month key")
	}
}
