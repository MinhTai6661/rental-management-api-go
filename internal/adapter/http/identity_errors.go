package httpadapter

import (
	"errors"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/apperr"
)

// MapIdentityError turns the errors of the identity use cases into the API's errors. Anything it does not know is
// returned as it is (httpx.Fail treats it as an internal error, or as busy for a cancelled query).
func MapIdentityError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrEmailTaken):
		return apperr.Invalid(apperr.Field{Name: "email", Rule: apperr.RuleUnique})
	case errors.Is(err, domain.ErrWardNotFound):
		return apperr.Invalid(apperr.Field{Name: "ward_code", Rule: apperr.RuleExists})
	case errors.Is(err, domain.ErrRoleNotHeld):
		return apperr.Invalid(apperr.Field{Name: "role", Rule: apperr.RuleRoleNotHeld})
	case errors.Is(err, domain.ErrCurrentPasswordWrong):
		return apperr.Invalid(apperr.Field{Name: "current_password", Rule: apperr.RulePasswordIncorrect})
	case errors.Is(err, domain.ErrNewPasswordSame):
		return apperr.Invalid(apperr.Field{Name: "new_password", Rule: apperr.RulePasswordSame})
	case errors.Is(err, domain.ErrInvalidCredentials):
		return apperr.InvalidCredentials()
	case errors.Is(err, domain.ErrAccountInactive):
		return apperr.AccountInactive()
	case errors.Is(err, domain.ErrEmailUnverified):
		return apperr.EmailUnverified()
	case errors.Is(err, domain.ErrForbidden):
		return apperr.Forbidden()
	case errors.Is(err, domain.ErrUnauthenticated):
		return apperr.Unauthenticated()
	case errors.Is(err, domain.ErrRefreshInvalid):
		return apperr.RefreshInvalid()
	case errors.Is(err, domain.ErrVerificationInvalid):
		return apperr.VerificationInvalid()
	case errors.Is(err, shared.ErrUnavailable):
		return apperr.Busy(err)
	}
	return err
}
