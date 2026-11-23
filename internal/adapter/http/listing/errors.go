package listinghttp

import (
	"errors"
	"strconv"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/listing"
)

// mapListingError turns the errors of the listing use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapListingError(err error) error {
	if e := listingError(err); e != nil {
		return e
	}
	return err
}

// listingError is the API error of a domain error, or nil when err is not one of them.
func listingError(err error) *apperr.Error {
	field := func(name, rule string) *apperr.Error {
		return apperr.Invalid(apperr.Field{Name: name, Rule: rule})
	}
	var (
		listed *domain.RoomAlreadyListedError
		limit  *domain.LimitError
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrRoomNotFound):
		return field("room_id", apperr.RuleExists)
	case errors.Is(err, domain.ErrItemsNotFound):
		return field("ids", apperr.RuleExists)
	case errors.Is(err, domain.ErrNoteRequired):
		return field("note", apperr.RuleRequired)
	case errors.Is(err, domain.ErrInvalidTransition):
		return apperr.Conflict(apperr.CodeListingTransition)
	case errors.Is(err, domain.ErrUnpublishFirst):
		return apperr.Conflict(apperr.CodeListingUnpublish)
	case errors.As(err, &listed):
		return apperr.Conflict(apperr.CodeListingRoomListed).WithParams(map[string]string{"room": listed.Room})
	case errors.As(err, &limit):
		return apperr.Conflict(apperr.CodeListingLimit).WithParams(map[string]string{"limit": strconv.Itoa(limit.Limit)})
	}
	return nil
}
