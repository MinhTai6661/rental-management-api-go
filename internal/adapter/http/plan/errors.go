package planhttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/plan"
)

// mapPlanError turns the errors of the plan use cases into the API's errors. Anything it does not know is returned as
// it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapPlanError(err error) error {
	if e := planError(err); e != nil {
		return e
	}
	return err
}

// planError is the API error of a domain error, or nil when err is not one of them.
func planError(err error) *apperr.Error {
	field := func(name, rule string) *apperr.Error {
		return apperr.Invalid(apperr.Field{Name: name, Rule: rule})
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrPlanNotFound), errors.Is(err, domain.ErrSubscriptionNotFound), errors.Is(err, domain.ErrAccountNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrPlanUnknown):
		return field("plan_id", apperr.RuleExists)
	case errors.Is(err, domain.ErrWrongAudience):
		return apperr.Conflict(apperr.CodePlanWrongAudience)
	case errors.Is(err, domain.ErrPlanInactive):
		return apperr.Conflict(apperr.CodePlanInactive)
	case errors.Is(err, domain.ErrAlreadyOnPlan):
		return apperr.Conflict(apperr.CodePlanAlreadyOn)
	case errors.Is(err, domain.ErrNotPending):
		return apperr.Conflict(apperr.CodePlanNotPending)
	case errors.Is(err, domain.ErrCodeTaken):
		return field("code", apperr.RuleUnique)
	case errors.Is(err, domain.ErrInvalidCode):
		return field("code", apperr.RuleFormat)
	case errors.Is(err, domain.ErrEndInPast):
		return field("ends_at", apperr.RuleDateNotPast)
	}
	return nil
}
