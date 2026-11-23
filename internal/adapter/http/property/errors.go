package propertyhttp

import (
	"errors"
	"strconv"
	"strings"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/apperr"
)

// mapPropertyError turns the errors of the property use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapPropertyError(err error) error {
	if e := propertyError(err); e != nil {
		return e
	}
	return err
}

// propertyError is the API error of a domain error, or nil when err is not one of them.
func propertyError(err error) *apperr.Error {
	field := func(name, rule string, params map[string]string) *apperr.Error {
		return apperr.Invalid(apperr.Field{Name: name, Rule: rule, Params: params})
	}
	var (
		validation   *domain.ValidationError
		buildingMax  *domain.BuildingLimitError
		roomMax      *domain.RoomLimitError
		mediaMax     *domain.MediaLimitError
		hasRooms     *domain.BuildingHasRoomsError
		priceInUse   *domain.BuildingPriceInUseError
		namesTaken   *domain.RoomNamesTakenError
		tooMany      *domain.RangeTooManyError
		nameTooLong  *domain.RangeNameTooLongError
		dimensions   *domain.ImageDimensionsError
		fileTooLarge *domain.FileTooLargeError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &validation):
		fields := make([]apperr.Field, len(validation.Violations))
		for i, v := range validation.Violations {
			fields[i] = apperr.Field{Name: v.Name, Rule: v.Rule, Params: v.Params}
		}
		return apperr.Invalid(fields...)
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrWardNotFound):
		return field("ward_code", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrBuildingNotFound):
		return field("building_id", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrItemsNotFound):
		return field("ids", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrBuildingNameTaken), errors.Is(err, domain.ErrRoomNameTaken):
		return field("name", apperr.RuleUnique, nil)
	case errors.Is(err, domain.ErrRoomStatusLocked):
		return apperr.Conflict(apperr.CodeRoomStatusLocked)
	case errors.Is(err, domain.ErrRoomHasContract):
		return apperr.Conflict(apperr.CodeRoomHasContract)
	case errors.Is(err, domain.ErrRoomHasListing):
		return apperr.Conflict(apperr.CodeRoomHasListing)
	case errors.As(err, &hasRooms):
		return apperr.Conflict(apperr.CodeBuildingHasRooms).WithParams(map[string]string{"count": strconv.Itoa(hasRooms.Count)})
	case errors.As(err, &buildingMax):
		return apperr.Conflict(apperr.CodeBuildingLimit).WithParams(map[string]string{"limit": strconv.Itoa(buildingMax.Limit)})
	case errors.As(err, &roomMax):
		return apperr.Conflict(apperr.CodeRoomLimit).WithParams(map[string]string{"limit": strconv.Itoa(roomMax.Limit)})
	case errors.As(err, &mediaMax):
		return apperr.Conflict(apperr.CodeMediaLimitReached).WithParams(map[string]string{"max": strconv.Itoa(mediaMax.Max)})
	case errors.As(err, &priceInUse):
		return field("price_vnd", apperr.RuleBuildingPriceInUse, map[string]string{"count": strconv.Itoa(priceInUse.Count)})
	case errors.Is(err, domain.ErrRangeInvalid):
		return field("name_prefix", apperr.RuleRangeInvalid, map[string]string{})
	case errors.As(err, &tooMany):
		return field("name_prefix", apperr.RuleRangeTooMany, map[string]string{"max": strconv.Itoa(tooMany.Max)})
	case errors.As(err, &nameTooLong):
		return field("name_prefix", apperr.RuleRangeNameTooLong, map[string]string{"max": strconv.Itoa(nameTooLong.Max)})
	case errors.As(err, &namesTaken):
		return field("name_prefix", apperr.RuleNamesTaken, map[string]string{"names": strings.Join(namesTaken.Names, ", ")})
	case errors.Is(err, domain.ErrPictureKindInvalid):
		return field("kind", apperr.RuleOneOf, nil)
	case errors.Is(err, domain.ErrArrangementMismatch):
		return field("items", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrArrangementManyCovers):
		return field("items", apperr.RuleSingleCover, nil)
	case errors.Is(err, domain.ErrImageType):
		return field("file", apperr.RuleImageType, nil)
	case errors.Is(err, domain.ErrImageCorrupt):
		return field("file", apperr.RuleImageCorrupt, nil)
	case errors.As(err, &dimensions):
		return field("file", apperr.RuleImageDimensions, map[string]string{"max": strconv.Itoa(dimensions.Max)})
	case errors.As(err, &fileTooLarge):
		return field("file", apperr.RuleFileTooLarge, map[string]string{"max_kb": strconv.Itoa(fileTooLarge.MaxKB)})
	}
	return nil
}
