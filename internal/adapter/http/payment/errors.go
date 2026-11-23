package paymenthttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/payment"
	usecase "rental-management/internal/usecase/payment"
)

// mapPaymentError turns the errors of the payment use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapPaymentError(err error) error {
	if e := paymentError(err); e != nil {
		return e
	}
	return err
}

// violationRules maps the reason of a broken payment rule to the validation rule key the client sees.
var violationRules = map[string]string{
	domain.ReasonRequired:         apperr.RuleRequired,
	domain.ReasonTooLong:          apperr.RuleMaxString,
	domain.ReasonAmountTooLow:     apperr.RuleMinNumber,
	domain.ReasonAmountTooHigh:    apperr.RuleMaxNumber,
	domain.ReasonExceedsRemaining: apperr.RuleAmountExceedsRemaining,
	domain.ReasonUnknownBank:      apperr.RuleOneOf,
	domain.ReasonBadAccountNumber: apperr.RuleAccountFormat,
	domain.ReasonBadPhone:         apperr.RulePhoneFormat,
	domain.ReasonTooManyProofs:    apperr.RuleMaxItems,
	domain.ReasonDateInFuture:     apperr.RuleDateNotFuture,
}

// imageRules maps the reason a proof picture is refused to the validation rule key.
func imageRule(err error) (string, bool) {
	switch {
	case errors.Is(err, domain.ErrImageType):
		return apperr.RuleImageType, true
	case errors.Is(err, domain.ErrImageCorrupt):
		return apperr.RuleImageCorrupt, true
	case errors.Is(err, domain.ErrImageDimensions):
		return apperr.RuleImageDimensions, true
	case errors.Is(err, domain.ErrImageTooLarge):
		return apperr.RuleFileTooLarge, true
	}
	return "", false
}

// paymentError is the API error of a domain error, or nil when err is not one of them.
func paymentError(err error) *apperr.Error {
	var violations domain.Violations
	var proof *usecase.ProofError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnknownMethod):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrNotPayable):
		return apperr.Conflict(apperr.CodePaymentNotPayable)
	case errors.Is(err, domain.ErrAlreadyReviewed):
		return apperr.Conflict(apperr.CodePaymentReviewed)
	case errors.Is(err, domain.ErrMethodNotAccepted):
		return apperr.Invalid(apperr.Field{Name: "payment_method", Rule: apperr.RuleMethodNotAccepted})
	case errors.As(err, &proof):
		if rule, ok := imageRule(proof.Err); ok {
			return apperr.Invalid(apperr.Field{Name: "proofs." + itoa(proof.Index), Rule: rule})
		}
	case errors.As(err, &violations):
		fields := make([]apperr.Field, 0, len(violations))
		for _, v := range violations {
			rule, ok := violationRules[v.Reason]
			if !ok {
				return nil
			}
			fields = append(fields, apperr.Field{Name: v.Field, Rule: rule, Params: v.Params})
		}
		return apperr.Invalid(fields...)
	}
	return nil
}
