package audithttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/audit"
	"rental-management/internal/domain/shared"
)

// mapError turns the errors of the audit use cases into the API's errors. Anything it does not know is returned as it
// is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapError(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrRangeBackwards):
		return apperr.Invalid(apperr.Field{Name: "to", Rule: apperr.RuleAfterOrEqual, Params: map[string]string{"other": "from"}})
	case errors.Is(err, domain.ErrUnknownAction):
		return apperr.Invalid(apperr.Field{Name: "action", Rule: apperr.RuleOneOf})
	}
	return err
}
