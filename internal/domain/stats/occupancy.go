package stats

import (
	"errors"
	"sort"
	"time"
)

// ErrUnknownSort is a sort key that is not one of the three.
var ErrUnknownSort = errors.New("unknown sort key")

// SortKey orders the rooms of the ranking.
type SortKey string

// The sort keys of the ranking of rooms.
const (
	SortBilled    SortKey = "billed"
	SortCollected SortKey = "collected"
	SortOccupancy SortKey = "occupancy"
)

// ParseSort reads a sort key; the empty text is SortBilled.
func ParseSort(s string) (SortKey, error) {
	switch k := SortKey(s); k {
	case "":
		return SortBilled, nil
	case SortBilled, SortCollected, SortOccupancy:
		return k, nil
	}
	return "", ErrUnknownSort
}

// Span is a run of days, both ends included; a nil To has no end.
type Span struct {
	From time.Time
	To   *time.Time
}

// Days counts the days of [from, to] that at least one span covers (days covered twice count once).
func Days(spans []Span, from, to time.Time) int {
	type run struct{ from, to time.Time }
	var runs []run
	for _, s := range spans {
		start, end := s.From, to
		if start.Before(from) {
			start = from
		}
		if s.To != nil && s.To.Before(end) {
			end = *s.To
		}
		if !start.After(end) {
			runs = append(runs, run{start, end})
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].from.Before(runs[j].from) })
	total := 0
	var reached time.Time // the last day counted
	for _, r := range runs {
		start := r.from
		if !reached.IsZero() && !start.After(reached) {
			start = reached.AddDate(0, 0, 1)
		}
		if start.After(r.to) {
			continue
		}
		total += int(r.to.Sub(start).Hours()/24) + 1
		reached = r.to
	}
	return total
}

// RoomLife is when a room existed and when it was rented.
type RoomLife struct {
	Existed Span
	Rented  []Span
}

// Occupancy is how many days of a window rooms were rented out of the days they could have been.
type Occupancy struct {
	Occupied  int
	Available int
}

// Rate is the share of rented days, nil when no day was available.
func (o Occupancy) Rate() *float64 { return Percent(int64(o.Occupied), int64(o.Available)) }

// Add sums two occupancies.
func (o Occupancy) Add(other Occupancy) Occupancy {
	return Occupancy{Occupied: o.Occupied + other.Occupied, Available: o.Available + other.Available}
}

// OfRoom works the occupancy of one room over [from, to], counted up to today at the latest (a day to come is neither
// available nor rented). A window that lies after today has nothing to count.
func OfRoom(room RoomLife, from, to, today time.Time) Occupancy {
	if today.Before(to) {
		to = today
	}
	if to.Before(from) {
		return Occupancy{}
	}
	available := Days([]Span{room.Existed}, from, to)
	occupied := Days(room.Rented, from, to)
	if occupied > available {
		occupied = available
	}
	return Occupancy{Occupied: occupied, Available: available}
}

// OfMonths works the occupancy of every month of the range over all the rooms.
func OfMonths(rooms []RoomLife, months []Month, today time.Time) []Occupancy {
	out := make([]Occupancy, len(months))
	for i, m := range months {
		for _, r := range rooms {
			out[i] = out[i].Add(OfRoom(r, m.First(), m.Last(), today))
		}
	}
	return out
}
