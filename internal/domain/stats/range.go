package stats

import (
	"errors"
	"fmt"
	"time"
)

// Zone is the time zone the people of this system read dates in (Vietnam has no daylight saving time). The queries
// group months in the same zone (Asia/Ho_Chi_Minh).
var Zone = time.FixedZone("ICT", 7*60*60)

// MaxMonths is the longest range of months a figure can be asked for.
const MaxMonths = 36

// Errors of a range of months.
var (
	ErrRangeBackwards = errors.New("the range ends before it starts")
	ErrRangeTooLong   = errors.New("the range is longer than the limit")
)

// Day turns an instant into its calendar day in Zone, as midnight UTC, so days compare and subtract without a zone.
func Day(t time.Time) time.Time {
	y, m, d := t.In(Zone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Month is a calendar month.
type Month struct {
	Year  int
	Month time.Month
}

// MonthOf is the month of a day.
func MonthOf(day time.Time) Month { return Month{Year: day.Year(), Month: day.Month()} }

// String is the month as YYYY-MM.
func (m Month) String() string { return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month)) }

// First is the first day of the month (midnight UTC).
func (m Month) First() time.Time { return time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC) }

// Last is the last day of the month (midnight UTC).
func (m Month) Last() time.Time { return m.First().AddDate(0, 1, -1) }

// Next is the month after.
func (m Month) Next() Month { return MonthOf(m.First().AddDate(0, 1, 0)) }

// Previous is the month before.
func (m Month) Previous() Month { return MonthOf(m.First().AddDate(0, -1, 0)) }

// MonthRange is a run of whole calendar months, oldest first.
type MonthRange struct{ months []Month }

// NewMonthRange is every month from the one of from to the one of to (days, see Day). from after to, or more than
// MaxMonths months, is an error.
func NewMonthRange(from, to time.Time) (MonthRange, error) {
	first, last := MonthOf(from), MonthOf(to)
	if first.First().After(last.First()) {
		return MonthRange{}, ErrRangeBackwards
	}
	var months []Month
	for m := first; !m.First().After(last.First()); m = m.Next() {
		if len(months) == MaxMonths {
			return MonthRange{}, ErrRangeTooLong
		}
		months = append(months, m)
	}
	return MonthRange{months: months}, nil
}

// DefaultRange is the 12 months that end with the month of today.
func DefaultRange(today time.Time) MonthRange {
	last := MonthOf(today)
	months := make([]Month, 12)
	for i := 11; i >= 0; i-- {
		months[i] = last
		last = last.Previous()
	}
	return MonthRange{months: months}
}

// Months lists the months, oldest first.
func (r MonthRange) Months() []Month { return r.months }

// First is the first day of the range.
func (r MonthRange) First() time.Time { return r.months[0].First() }

// Last is the last day of the range.
func (r MonthRange) Last() time.Time { return r.months[len(r.months)-1].Last() }

// Index is the position of a YYYY-MM month in the range, or -1.
func (r MonthRange) Index(month string) int {
	for i, m := range r.months {
		if m.String() == month {
			return i
		}
	}
	return -1
}
