package property

import (
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/apperr"
)

// Temporary glue while the service still speaks apperr: the pure rules live in the domain package.

type (
	// Group, Status and the rule inputs are the domain's.
	Group            = domain.Group
	Status           = domain.Status
	Blocker          = domain.Blocker
	StatusChange     = domain.StatusChange
	chargeRow        = domain.ChargeRow
	chargeInput      = domain.ChargeInput
	chargeCreate     = domain.ChargeCreate
	buildingDefaults = domain.BuildingDefaults
	roomInput        = domain.RoomFields
	roomState        = domain.RoomState
	roomValues       = domain.RoomValues
	orderedID        = domain.OrderedID
	mediaRow         = domain.MediaRow
	arrangeItem      = domain.ArrangeItem
	MediaOwner       = domain.MediaOwner
)

const (
	GroupPrice        = domain.GroupPrice
	GroupArea         = domain.GroupArea
	GroupRentalPeriod = domain.GroupRentalPeriod
	GroupDescription  = domain.GroupDescription
	GroupCharges      = domain.GroupCharges
	GroupMedia        = domain.GroupMedia

	StatusAvailable   = domain.StatusAvailable
	StatusRented      = domain.StatusRented
	StatusMaintenance = domain.StatusMaintenance

	UnitMonth = domain.UnitMonth
	UnitDay   = domain.UnitDay

	KindPhoto = domain.KindPhoto
	KindCover = domain.KindCover

	mediaPrefix = domain.MediaKeyPrefix

	maxRoomsPerRun    = domain.MaxRoomsPerRun
	maxRoomNameLength = domain.MaxRoomNameLength
)

var (
	Groups             = domain.Groups
	RoomMedia          = domain.RoomMedia
	BuildingMedia      = domain.BuildingMedia
	resolveInheritance = domain.ResolveInheritance
	carriedGroups      = domain.CarriedGroups
	reorder            = domain.Reorder
	propagation        = domain.Propagation
	addressChanged     = domain.AddressChanged
	planChargeSync     = domain.PlanChargeSync
	pickCover          = domain.PickCover
	limitReached       = domain.LimitReached
	escapeLike         = domain.EscapeLike
	roundArea          = domain.RoundArea
)

// Failures of roomNames, in the words of the API rules.
const (
	rangeInvalid     = apperr.RuleRangeInvalid
	rangeTooMany     = apperr.RuleRangeTooMany
	rangeNameTooLong = apperr.RuleRangeNameTooLong
)

func toFields(v []domain.Violation) []apperr.Field {
	out := make([]apperr.Field, len(v))
	for i, x := range v {
		out[i] = apperr.Field{Name: x.Name, Rule: x.Rule, Params: x.Params}
	}
	return out
}

func effectiveValues(b *buildingDefaults, groups []Group, in roomInput) (roomValues, []apperr.Field) {
	v, violations := domain.EffectiveValues(b, groups, in)
	return v, toFields(violations)
}

func roomNames(prefix string, from, to, padding int) ([]string, string) {
	names, err := domain.RoomNames(prefix, from, to, padding)
	var many *domain.RangeTooManyError
	var long *domain.RangeNameTooLongError
	switch {
	case err == nil:
		return names, ""
	case errors.Is(err, domain.ErrRangeInvalid):
		return nil, rangeInvalid
	case errors.As(err, &many):
		return nil, rangeTooMany
	case errors.As(err, &long):
		return nil, rangeNameTooLong
	}
	return nil, rangeInvalid
}

func checkStatusChange(to Status, b Blocker) string {
	if domain.CheckStatusChange(to, b) != nil {
		return apperr.CodeRoomStatusLocked
	}
	return ""
}

func checkDelete(b Blocker) string {
	err := domain.CheckDelete(b)
	switch {
	case errors.Is(err, domain.ErrRoomHasContract):
		return apperr.CodeRoomHasContract
	case errors.Is(err, domain.ErrRoomHasListing):
		return apperr.CodeRoomHasListing
	}
	return ""
}

func checkArrangement(current []uuid.UUID, items []arrangeItem) string {
	err := domain.CheckArrangement(current, items)
	switch {
	case errors.Is(err, domain.ErrArrangementMismatch):
		return apperr.RuleExists
	case errors.Is(err, domain.ErrArrangementManyCovers):
		return apperr.RuleSingleCover
	}
	return ""
}

func equalPtr[T comparable](a, b *T) bool { return domain.EqualPtr(a, b) }

// ownerIDs gives the two owner columns of the media queries: the one that matches is set, the other is nil.
func ownerIDs(o MediaOwner) (room, building *uuid.UUID) {
	id := o.ID()
	if o.IsRoom() {
		return &id, nil
	}
	return nil, &id
}
