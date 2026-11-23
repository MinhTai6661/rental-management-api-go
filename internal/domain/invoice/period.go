package invoice

import "time"

// maxPeriods is a safety stop so a very old contract with no end date cannot loop for ever.
const maxPeriods = 600

// DateLayout is how a calendar date is written.
const DateLayout = "2006-01-02"

// Date builds a calendar date.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ParseDate reads a calendar date written as 2026-11-23.
func ParseDate(s string) (time.Time, error) { return time.Parse(DateLayout, s) }

// Today is the calendar date of now in the business time zone, as a date.
func Today(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return Date(y, m, d)
}

// AddMonths is the same day of a later month, never spilling into the month after (31 Jan + 1 month = 28 Feb).
func AddMonths(start time.Time, months int) time.Time {
	total := start.Year()*12 + int(start.Month()) - 1 + months
	year, month := total/12, time.Month(total%12+1)
	day := min(start.Day(), time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day())
	return Date(year, month, day)
}

// Periods splits a stay into its billing periods that have started (up to today), oldest first.
//
// A stay billed by the day with an end date is billed once for all its days. Any other stay (by the month, or by the
// day without an end date) is billed per calendar month counted from the start date; by the day, a month's units are
// its days.
func Periods(start time.Time, end *time.Time, billedByDay bool, today time.Time) []Period {
	if billedByDay && end != nil {
		if start.After(today) {
			return []Period{}
		}
		return []Period{{Index: 0, Start: start, End: *end, Units: max(1, DaysOf(start, *end))}}
	}
	periods := []Period{}
	for index := 0; index < maxPeriods; index++ {
		periodStart := AddMonths(start, index)
		if periodStart.After(today) || (end != nil && !periodStart.Before(*end)) {
			break
		}
		nextStart := AddMonths(start, index+1)
		periodEnd := nextStart.AddDate(0, 0, -1)
		if end != nil && periodEnd.After(*end) {
			periodEnd = *end
		}
		units := 1
		if billedByDay {
			units = max(1, DaysOf(periodStart, nextStart))
		}
		periods = append(periods, Period{Index: index, Start: periodStart, End: periodEnd, Units: units})
	}
	return periods
}

// PeriodsOf is Periods for a contract.
func PeriodsOf(c ContractFacts, today time.Time) []Period {
	return Periods(c.StartsOn, c.EndsOn, c.BilledByDay, today)
}

// ResolvePeriod picks the period a bill is for out of the started periods: the one starting on the given date, or
// without a choice the oldest period that has no bill yet (else the last). billed is the set of period start dates
// that have a bill.
func ResolvePeriod(c ContractFacts, periods []Period, requested *time.Time, billed map[time.Time]bool) (Period, error) {
	if len(periods) == 0 {
		reason := ReasonNothingToBill
		if c.BilledByDay && c.EndsOn == nil {
			reason = ReasonDayNeedsEnd
		}
		return Period{}, violation(FieldPeriodStart, reason, nil)
	}
	if requested != nil {
		want := Date(requested.Year(), requested.Month(), requested.Day())
		for _, p := range periods {
			if p.Start.Equal(want) {
				return p, nil
			}
		}
		return Period{}, violation(FieldPeriodStart, ReasonInvalidPeriod, nil)
	}
	for _, p := range periods {
		if !billed[p.Start] {
			return p, nil
		}
	}
	return periods[len(periods)-1], nil
}

// DefaultName is what a bill is called when nobody names it: its month and its room ("04/2026 - P101").
func DefaultName(periodStart time.Time, room string) string {
	return periodStart.Format("01/2006") + " - " + room
}

// MonthKey is how bills are counted per month ("2026-04").
func MonthKey(d time.Time) string { return d.Format("2006-01") }

// EndOfDay is the last instant of the calendar date d in the business time zone.
func EndOfDay(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 999_999_000, loc)
}

// StartOfDay is the first instant of the calendar date d in the business time zone.
func StartOfDay(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}
