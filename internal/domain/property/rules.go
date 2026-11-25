package property

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// The business rules of buildings, rooms, charges and pictures as small functions on plain data: no database, no
// clock, no HTTP. A use case loads the data, calls them and acts on the answer.

// ResolveInheritance says which groups of a room follow its building. A room outside any building follows nothing.
// A request that lists the groups (requested != nil) gets exactly the known ones among them, in the fixed order;
// otherwise a group follows the building unless the request carries its own value for it.
func ResolveInheritance(requested *[]Group, carried map[Group]bool, inBuilding bool) []Group {
	if !inBuilding {
		return []Group{}
	}
	out := []Group{}
	for _, g := range Groups() {
		if requested != nil {
			if slices.Contains(*requested, g) {
				out = append(out, g)
			}
		} else if !carried[g] {
			out = append(out, g)
		}
	}
	return out
}

// CarriedGroups says which groups the request brings its own value for. current is nil when the room is created.
// Pictures are never in the body: an update keeps them as they are, so they count as carried while the room owns
// its pictures (a PUT must not silently drop a room's own photos); the same goes for charges left out of the body.
func CarriedGroups(in RoomFields, current *RoomState) map[Group]bool {
	carried := map[Group]bool{
		GroupPrice:        in.PriceVND != nil,
		GroupArea:         in.Area != nil,
		GroupRentalPeriod: in.MinPeriod != nil || in.PeriodUnit != nil,
		GroupDescription:  in.Description != nil && strings.TrimSpace(*in.Description) != "",
		GroupCharges:      in.Charges != nil,
	}
	if current != nil {
		if in.Charges == nil {
			carried[GroupCharges] = !current.InheritsCharges
		}
		carried[GroupMedia] = !current.InheritsMedia
	}
	return carried
}

// EffectiveValues computes the values in force: the building's for every followed group, the request's otherwise.
// b is nil for a room outside any building (groups is then empty).
func EffectiveValues(b *BuildingDefaults, groups []Group, in RoomFields) (RoomValues, []Violation) {
	follows := func(g Group) bool { return b != nil && slices.Contains(groups, g) }
	var v RoomValues
	var fields []Violation

	if b != nil {
		v.WardCode, v.AddressDetail = b.WardCode, b.AddressDetail
	} else {
		v.WardCode = in.WardCode
		if in.AddressDetail != nil {
			v.AddressDetail = strings.TrimSpace(*in.AddressDetail)
		}
		if v.AddressDetail == "" {
			fields = append(fields, Violation{Name: "address_detail", Rule: RuleRequired})
		}
	}

	switch {
	case follows(GroupPrice):
		if b.PriceVND == nil {
			fields = append(fields, Violation{Name: "price_vnd", Rule: RuleBuildingPriceMissing})
		} else {
			v.PriceVND = *b.PriceVND
		}
	case in.PriceVND == nil:
		fields = append(fields, Violation{Name: "price_vnd", Rule: RuleRequired})
	default:
		v.PriceVND = *in.PriceVND
	}

	if follows(GroupArea) {
		v.Area = b.Area
	} else {
		v.Area = in.Area
	}

	v.MinPeriod, v.PeriodUnit = DefaultPeriod, UnitMonth
	switch {
	case follows(GroupRentalPeriod) && b.MinPeriod != nil && b.PeriodUnit != nil:
		v.MinPeriod, v.PeriodUnit = *b.MinPeriod, *b.PeriodUnit
	default:
		// Own value, or the default for a building that sets none; one half of the pair is completed.
		if in.MinPeriod != nil {
			v.MinPeriod = *in.MinPeriod
		}
		if in.PeriodUnit != nil {
			v.PeriodUnit = *in.PeriodUnit
		}
	}

	if follows(GroupDescription) {
		v.Description = b.Description
	} else if in.Description != nil && strings.TrimSpace(*in.Description) != "" {
		d := strings.TrimSpace(*in.Description)
		v.Description = &d
	}
	return v, fields
}

// RoomNames builds the names of a run of rooms: prefix + number from..to, zero padded to padding digits. It
// returns ErrRangeInvalid, *RangeTooManyError or *RangeNameTooLongError when the run cannot be made.
func RoomNames(prefix string, from, to, padding int) ([]string, error) {
	prefix = strings.TrimSpace(prefix)
	if from < 0 || to < from {
		return nil, ErrRangeInvalid
	}
	if to-from+1 > MaxRoomsPerRun {
		return nil, &RangeTooManyError{Max: MaxRoomsPerRun}
	}
	names := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		name := prefix + fmt.Sprintf("%0*d", padding, n)
		if utf8.RuneCountInString(name) > MaxRoomNameLength {
			return nil, &RangeNameTooLongError{Max: MaxRoomNameLength}
		}
		names = append(names, name)
	}
	return names, nil
}

// Reorder applies a drag and drop: the moved ids swap into the slots the moved ids held, in the order the request
// gives them; the other rows stay where they are. Positions count 1..n, and only rows whose stored order differs
// from their new position are returned (so duplicates and gaps get renumbered too). current is in
// (display_order, id) order.
func Reorder(current []OrderedID, moved []uuid.UUID) []Position {
	inCurrent := make(map[uuid.UUID]bool, len(current))
	for _, row := range current {
		inCurrent[row.ID] = true
	}
	var queue []uuid.UUID
	isMoved := map[uuid.UUID]bool{}
	for _, id := range moved {
		if inCurrent[id] && !isMoved[id] {
			isMoved[id] = true
			queue = append(queue, id)
		}
	}

	var out []Position
	for i, row := range current {
		id := row.ID
		if isMoved[id] {
			id, queue = queue[0], queue[1:]
		}
		if want := i + 1; id != row.ID || row.Order != want {
			out = append(out, Position{ID: id, Order: want})
		}
	}
	return out
}

// CheckStatusChange returns ErrRoomStatusLocked when a running contract refuses moving a room to status to.
func CheckStatusChange(to Status, b Blocker) error {
	if to != StatusRented && b.ActiveContract {
		return ErrRoomStatusLocked
	}
	return nil
}

// CheckDelete returns the error that refuses deleting a room (ErrRoomHasContract, ErrRoomHasListing), or nil.
func CheckDelete(b Blocker) error {
	switch {
	case b.ActiveContract:
		return ErrRoomHasContract
	case b.OpenListing:
		return ErrRoomHasListing
	}
	return nil
}

// Propagation says which groups of a building changed in a way that must reach the rooms following them: a
// removed price or rental period is not pushed (the rooms keep the value they hold), the others are.
func Propagation(before, after BuildingDefaults) map[Group]bool {
	out := map[Group]bool{}
	if !equalPtr(before.PriceVND, after.PriceVND) && after.PriceVND != nil {
		out[GroupPrice] = true
	}
	if !equalPtr(before.Area, after.Area) {
		out[GroupArea] = true
	}
	if (!equalPtr(before.MinPeriod, after.MinPeriod) || !equalPtr(before.PeriodUnit, after.PeriodUnit)) &&
		after.MinPeriod != nil && after.PeriodUnit != nil {
		out[GroupRentalPeriod] = true
	}
	if !equalPtr(before.Description, after.Description) {
		out[GroupDescription] = true
	}
	return out
}

// AddressChanged reports whether the address moved; it is pushed to every room of the building.
func AddressChanged(before, after BuildingDefaults) bool {
	return !equalPtr(before.WardCode, after.WardCode) || before.AddressDetail != after.AddressDetail
}

// EqualPtr compares two optional values: both absent, or both present and equal.
func EqualPtr[T comparable](a, b *T) bool { return equalPtr(a, b) }

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// PlanChargeSync diffs a submitted list against the stored charges: an item with the id of a stored charge updates
// it; any other item (no id, a foreign id, an id listed twice) creates one; stored charges not listed are deleted.
func PlanChargeSync(existing []ChargeRow, submitted []ChargeInput) ([]ChargeUpdate, []ChargeCreate, []uuid.UUID) {
	known := make(map[uuid.UUID]bool, len(existing))
	for _, row := range existing {
		known[row.ID] = true
	}
	kept := map[uuid.UUID]bool{}
	var updates []ChargeUpdate
	var creates []ChargeCreate
	for i, item := range submitted {
		if item.ID != nil && known[*item.ID] && !kept[*item.ID] {
			kept[*item.ID] = true
			updates = append(updates, ChargeUpdate{ID: *item.ID, Input: item, Order: i + 1})
			continue
		}
		creates = append(creates, ChargeCreate{Input: item, Order: i + 1})
	}
	var deletes []uuid.UUID
	for _, row := range existing {
		if !kept[row.ID] {
			deletes = append(deletes, row.ID)
		}
	}
	return updates, creates, deletes
}

// PlanFollow diffs the charges a room copied from its building against the building's templates: a copy whose
// template is still there is updated in place (it keeps its id), a copy of a removed template and the room's own
// charges are deleted, and a template with no copy is inserted.
func PlanFollow(templates, copies []ChargeRow) (update map[uuid.UUID]uuid.UUID, insert []uuid.UUID, deletes []uuid.UUID) {
	present := make(map[uuid.UUID]bool, len(templates))
	for _, t := range templates {
		present[t.ID] = true
	}
	update = map[uuid.UUID]uuid.UUID{}
	copied := map[uuid.UUID]bool{}
	for _, c := range copies {
		if c.TemplateID != nil && present[*c.TemplateID] && !copied[*c.TemplateID] {
			update[c.ID] = *c.TemplateID
			copied[*c.TemplateID] = true
			continue
		}
		deletes = append(deletes, c.ID)
	}
	for _, t := range templates {
		if !copied[t.ID] {
			insert = append(insert, t.ID)
		}
	}
	return update, insert, deletes
}

// PickCover chooses the cover of a room or building: the picture of kind cover, else the first in order.
func PickCover(media []MediaRow) *MediaRow {
	var best *MediaRow
	for i := range media {
		m := &media[i]
		switch {
		case best == nil:
			best = m
		case m.Kind == KindCover && best.Kind != KindCover:
			best = m
		case m.Kind == best.Kind && (m.DisplayOrder < best.DisplayOrder ||
			(m.DisplayOrder == best.DisplayOrder && strings.Compare(m.ID.String(), best.ID.String()) < 0)):
			best = m
		}
	}
	return best
}

// CheckArrangement validates a new arrangement of pictures: it must list exactly the current ones, once each
// (ErrArrangementMismatch), with at most one cover (ErrArrangementManyCovers).
func CheckArrangement(current []uuid.UUID, items []ArrangeItem) error {
	if len(items) != len(current) {
		return ErrArrangementMismatch
	}
	have := make(map[uuid.UUID]bool, len(current))
	for _, id := range current {
		have[id] = true
	}
	seen := map[uuid.UUID]bool{}
	covers := 0
	for _, it := range items {
		if !have[it.ID] || seen[it.ID] {
			return ErrArrangementMismatch
		}
		seen[it.ID] = true
		if it.Kind == KindCover {
			covers++
		}
	}
	if covers > 1 {
		return ErrArrangementManyCovers
	}
	return nil
}

// LimitReached reports whether adding n items on top of count would pass the limit (nil = no limit).
func LimitReached(limit *int, count, adding int) bool {
	return limit != nil && count+adding-1 >= *limit
}

// EscapeLike makes a user's text safe inside a LIKE pattern ("\" is the escape character).
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// RoundArea rounds a floor area to the two decimals the column keeps.
func RoundArea(v float64) float64 {
	n, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	return n
}

// MediaKeys builds the storage keys of a picture and its thumbnail from the date and 16 random bytes. The name is
// unguessable and never reused, so the files can be cached for ever.
func MediaKeys(now time.Time, random [16]byte, ext string) (file, thumb string) {
	base := fmt.Sprintf("%s%04d/%02d/%x", MediaKeyPrefix, now.Year(), int(now.Month()), random[:])
	return base + "." + ext, base + "_thumb.webp"
}
