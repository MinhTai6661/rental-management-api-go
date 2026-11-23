package property

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"rental-management/pkg/apperr"
)

// This file holds the business rules of buildings, rooms, charges and pictures as small functions on plain data:
// no database, no clock, no HTTP. The service loads the data, calls them and acts on the answer.

// Group is a part of a room that can follow its building.
type Group string

// The groups, in the fixed order the API lists them.
const (
	GroupPrice        Group = "price"
	GroupArea         Group = "area"
	GroupRentalPeriod Group = "rental_period"
	GroupDescription  Group = "description"
	GroupCharges      Group = "charges"
	GroupMedia        Group = "media"
)

// Groups lists every group in its fixed order.
func Groups() []Group {
	return []Group{GroupPrice, GroupArea, GroupRentalPeriod, GroupDescription, GroupCharges, GroupMedia}
}

// Status is the state of a room.
type Status string

// Room statuses.
const (
	StatusAvailable   Status = "available"
	StatusRented      Status = "rented"
	StatusMaintenance Status = "maintenance"
)

// Rental period units.
const (
	UnitMonth = "month"
	UnitDay   = "day"
)

// Limits of the data.
const (
	maxRoomsPerRun    = 100
	maxRoomNameLength = 50
	defaultPeriod     = 1
)

// chargeRow is a stored charge as the rules see it.
type chargeRow struct {
	ID         uuid.UUID
	TemplateID *uuid.UUID // on a room's copy: the building charge it follows
}

// chargeInput is a charge of a request.
type chargeInput struct {
	ID        *uuid.UUID
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
}

// buildingDefaults is what a building offers its rooms.
type buildingDefaults struct {
	WardCode      *int32
	AddressDetail string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
}

// roomInput is what a room request carries.
type roomInput struct {
	WardCode      *int32
	AddressDetail *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
	Charges       []chargeInput // nil = the request does not mention charges
}

// roomState is what an existing room currently follows.
type roomState struct {
	InheritsCharges bool
	InheritsMedia   bool
}

// roomValues are the values in force for a room after resolving the inheritance.
type roomValues struct {
	WardCode      *int32
	AddressDetail string
	PriceVND      int64
	Area          *float64
	MinPeriod     int32
	PeriodUnit    string
	Description   *string
}

// resolveInheritance says which groups of a room follow its building. A room outside any building follows nothing.
// A request that lists the groups (requested != nil) gets exactly the known ones among them, in the fixed order;
// otherwise a group follows the building unless the request carries its own value for it.
func resolveInheritance(requested *[]Group, carried map[Group]bool, inBuilding bool) []Group {
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

// carriedGroups says which groups the request brings its own value for. current is nil when the room is created.
// Pictures are never in the body: an update keeps them as they are, so they count as carried while the room owns
// its pictures (a PUT must not silently drop a room's own photos); the same goes for charges left out of the body.
func carriedGroups(in roomInput, current *roomState) map[Group]bool {
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

// effectiveValues computes the values in force: the building's for every followed group, the request's otherwise.
// b is nil for a room outside any building (groups is then empty).
func effectiveValues(b *buildingDefaults, groups []Group, in roomInput) (roomValues, []apperr.Field) {
	follows := func(g Group) bool { return b != nil && slices.Contains(groups, g) }
	var v roomValues
	var fields []apperr.Field

	if b != nil {
		v.WardCode, v.AddressDetail = b.WardCode, b.AddressDetail
	} else {
		v.WardCode = in.WardCode
		if in.AddressDetail != nil {
			v.AddressDetail = strings.TrimSpace(*in.AddressDetail)
		}
		if v.AddressDetail == "" {
			fields = append(fields, apperr.Field{Name: "address_detail", Rule: apperr.RuleRequired})
		}
	}

	switch {
	case follows(GroupPrice):
		if b.PriceVND == nil {
			fields = append(fields, apperr.Field{Name: "price_vnd", Rule: apperr.RuleBuildingPriceMissing})
		} else {
			v.PriceVND = *b.PriceVND
		}
	case in.PriceVND == nil:
		fields = append(fields, apperr.Field{Name: "price_vnd", Rule: apperr.RuleRequired})
	default:
		v.PriceVND = *in.PriceVND
	}

	if follows(GroupArea) {
		v.Area = b.Area
	} else {
		v.Area = in.Area
	}

	v.MinPeriod, v.PeriodUnit = defaultPeriod, UnitMonth
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

// Failures of roomNames.
const (
	rangeInvalid     = "range_invalid"
	rangeTooMany     = "range_too_many"
	rangeNameTooLong = "range_name_too_long"
)

// roomNames builds the names of a run of rooms: prefix + number from..to, zero padded to padding digits.
// The second value is the failed rule ("" when all is well).
func roomNames(prefix string, from, to, padding int) ([]string, string) {
	prefix = strings.TrimSpace(prefix)
	if from < 0 || to < from {
		return nil, rangeInvalid
	}
	if to-from+1 > maxRoomsPerRun {
		return nil, rangeTooMany
	}
	names := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		name := prefix + fmt.Sprintf("%0*d", padding, n)
		if utf8.RuneCountInString(name) > maxRoomNameLength {
			return nil, rangeNameTooLong
		}
		names = append(names, name)
	}
	return names, ""
}

// orderedID is a row with its stored position.
type orderedID struct {
	ID    uuid.UUID
	Order int
}

// position is a new place in the list.
type position struct {
	ID    uuid.UUID
	Order int
}

// reorder applies a drag and drop: the moved ids swap into the slots the moved ids held, in the order the request
// gives them; the other rows stay where they are. Positions count 1..n, and only rows whose stored order differs
// from their new position are returned (so duplicates and gaps get renumbered too). current is in
// (display_order, id) order.
func reorder(current []orderedID, moved []uuid.UUID) []position {
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

	var out []position
	for i, row := range current {
		id := row.ID
		if isMoved[id] {
			id, queue = queue[0], queue[1:]
		}
		if want := i + 1; id != row.ID || row.Order != want {
			out = append(out, position{ID: id, Order: want})
		}
	}
	return out
}

// Blocker is what stops a room from being changed or deleted; later modules fill it in.
type Blocker struct {
	ActiveContract bool
	OpenListing    bool
}

// checkStatusChange returns the error code that refuses moving a room to status to, or "".
func checkStatusChange(to Status, b Blocker) string {
	if to != StatusRented && b.ActiveContract {
		return apperr.CodeRoomStatusLocked
	}
	return ""
}

// checkDelete returns the error code that refuses deleting a room, or "".
func checkDelete(b Blocker) string {
	switch {
	case b.ActiveContract:
		return apperr.CodeRoomHasContract
	case b.OpenListing:
		return apperr.CodeRoomHasListing
	}
	return ""
}

// propagation says which groups of a building changed in a way that must reach the rooms following them: a
// removed price or rental period is not pushed (the rooms keep the value they hold), the others are.
func propagation(before, after buildingDefaults) map[Group]bool {
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

// addressChanged reports whether the address moved; it is pushed to every room of the building.
func addressChanged(before, after buildingDefaults) bool {
	return !equalPtr(before.WardCode, after.WardCode) || before.AddressDetail != after.AddressDetail
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// chargeUpdate and chargeCreate are the steps planChargeSync asks for; Order is the 1-based position.
type chargeUpdate struct {
	ID    uuid.UUID
	Input chargeInput
	Order int
}

type chargeCreate struct {
	Input chargeInput
	Order int
}

// planChargeSync diffs a submitted list against the stored charges: an item with the id of a stored charge updates
// it; any other item (no id, a foreign id, an id listed twice) creates one; stored charges not listed are deleted.
func planChargeSync(existing []chargeRow, submitted []chargeInput) ([]chargeUpdate, []chargeCreate, []uuid.UUID) {
	known := make(map[uuid.UUID]bool, len(existing))
	for _, row := range existing {
		known[row.ID] = true
	}
	kept := map[uuid.UUID]bool{}
	var updates []chargeUpdate
	var creates []chargeCreate
	for i, item := range submitted {
		if item.ID != nil && known[*item.ID] && !kept[*item.ID] {
			kept[*item.ID] = true
			updates = append(updates, chargeUpdate{ID: *item.ID, Input: item, Order: i + 1})
			continue
		}
		creates = append(creates, chargeCreate{Input: item, Order: i + 1})
	}
	var deletes []uuid.UUID
	for _, row := range existing {
		if !kept[row.ID] {
			deletes = append(deletes, row.ID)
		}
	}
	return updates, creates, deletes
}

// planFollow diffs the charges a room copied from its building against the building's templates: a copy whose
// template is still there is updated in place (it keeps its id), a copy of a removed template and the room's own
// charges are deleted, and a template with no copy is inserted.
func planFollow(templates, copies []chargeRow) (update map[uuid.UUID]uuid.UUID, insert []uuid.UUID, deletes []uuid.UUID) {
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

// mediaRow is a picture as the rules see it.
type mediaRow struct {
	ID           uuid.UUID
	Kind         string
	DisplayOrder int
}

// Picture kinds.
const (
	KindPhoto = "photo"
	KindCover = "cover"
)

// pickCover chooses the cover of a room or building: the picture of kind cover, else the first in order.
func pickCover(media []mediaRow) *mediaRow {
	var best *mediaRow
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

// arrangeItem is one picture of an arrangement request.
type arrangeItem struct {
	ID   uuid.UUID
	Kind string
}

// checkArrangement validates a new arrangement of pictures: it must list exactly the current ones, once each
// ("exists"), with at most one cover ("single_cover"). "" means valid.
func checkArrangement(current []uuid.UUID, items []arrangeItem) string {
	if len(items) != len(current) {
		return apperr.RuleExists
	}
	have := make(map[uuid.UUID]bool, len(current))
	for _, id := range current {
		have[id] = true
	}
	seen := map[uuid.UUID]bool{}
	covers := 0
	for _, it := range items {
		if !have[it.ID] || seen[it.ID] {
			return apperr.RuleExists
		}
		seen[it.ID] = true
		if it.Kind == KindCover {
			covers++
		}
	}
	if covers > 1 {
		return apperr.RuleSingleCover
	}
	return ""
}

// limitReached reports whether adding n items on top of count would pass the limit (nil = no limit).
func limitReached(limit *int, count, adding int) bool {
	return limit != nil && count+adding-1 >= *limit
}

// escapeLike makes a user's text safe inside a LIKE pattern ("\" is the escape character).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// roundArea rounds a floor area to the two decimals the column keeps.
func roundArea(v float64) float64 {
	n, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	return n
}
