// Package adminhttp is the HTTP side of the admin use cases: user management, roles, landlord applications and
// profiles, system settings, legal documents and the data subject's rights.
package adminhttp

import (
	"errors"
	"strconv"
	"strings"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
)

// mapError turns the errors of the admin use cases into the API's errors. Anything it does not know is returned as
// it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapError(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound), errors.Is(err, domain.ErrNotLandlord), errors.Is(err, domain.ErrUnknownKind):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrForbidden):
		return apperr.Forbidden()
	case errors.Is(err, domain.ErrRolesEmpty):
		return apperr.Invalid(apperr.Field{Name: "roles", Rule: apperr.RuleMinItems, Params: map[string]string{"min": "1"}})
	case errors.Is(err, domain.ErrUnknownRole):
		return apperr.Invalid(apperr.Field{Name: "roles", Rule: apperr.RuleOneOf})
	case errors.Is(err, domain.ErrStaffRolesMixed):
		return apperr.Invalid(apperr.Field{Name: "roles", Rule: apperr.RuleStaffRolesMixed})
	case errors.Is(err, domain.ErrAlreadyLandlord):
		return apperr.Invalid(apperr.Field{Name: "application", Rule: apperr.RuleAlreadyLandlord})
	case errors.Is(err, domain.ErrNotRenter):
		return apperr.Invalid(apperr.Field{Name: "application", Rule: apperr.RuleNotRenter})
	case errors.Is(err, domain.ErrAlreadyPending):
		return apperr.Invalid(apperr.Field{Name: "application", Rule: apperr.RuleAlreadyPending})
	case errors.Is(err, domain.ErrNotReviewable):
		return apperr.Invalid(apperr.Field{Name: "application", Rule: apperr.RuleNotReviewable})
	case errors.Is(err, domain.ErrStaffCannotErase):
		return apperr.Invalid(apperr.Field{Name: "account", Rule: apperr.RuleStaffCannotErase})
	case errors.Is(err, domain.ErrPasswordWrong):
		return apperr.Invalid(apperr.Field{Name: "password", Rule: apperr.RulePasswordIncorrect})
	case errors.Is(err, domain.ErrNoIDs):
		return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleMinItems, Params: map[string]string{"min": "1"}})
	case errors.Is(err, domain.ErrTooManyIDs):
		return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleMaxItems, Params: map[string]string{"max": strconv.Itoa(domain.MaxBulkIDs)}})
	}
	var running *domain.HasActiveContractsError
	if errors.As(err, &running) {
		return apperr.Invalid(apperr.Field{Name: "roles", Rule: apperr.RuleHasActiveContracts, Params: map[string]string{"count": strconv.Itoa(running.Count)}})
	}
	var blocked *domain.ErasureBlockedError
	if errors.As(err, &blocked) {
		return apperr.Invalid(apperr.Field{Name: "account", Rule: apperr.RuleErasureBlocked, Params: map[string]string{"blockers": strings.Join(blocked.Blockers, ", ")}})
	}
	var invalid *domain.ValidationError
	if errors.As(err, &invalid) {
		fields := make([]apperr.Field, len(invalid.Violations))
		for i, v := range invalid.Violations {
			fields[i] = apperr.Field{Name: v.Name, Rule: v.Rule, Params: v.Params}
		}
		return apperr.Invalid(fields...)
	}
	return err
}
