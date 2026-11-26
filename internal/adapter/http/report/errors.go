// Package reporthttp is the HTTP adapter of the report context: the reports a person wrote or has to answer, and the
// conversation under each.
package reporthttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/report"
)

// mapReportError turns the errors of the report use cases into the API's errors. Anything it does not know is returned
// as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapReportError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrForbidden):
		return apperr.Forbidden()
	case errors.Is(err, domain.ErrReadOnly):
		return apperr.E(apperr.KindForbidden, apperr.CodeReportReadOnly)
	case errors.Is(err, domain.ErrRoomRequired):
		return apperr.Invalid(apperr.Field{Name: "room_id", Rule: apperr.RuleRequired})
	case errors.Is(err, domain.ErrRoomNotYours):
		return apperr.Invalid(apperr.Field{Name: "room_id", Rule: apperr.RuleRoomNotYours})
	}
	return err
}
