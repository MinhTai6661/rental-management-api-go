package invoicehttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/invoice"
)

// mapInvoiceError turns the errors of the invoice use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapInvoiceError(err error) error {
	if e := invoiceError(err); e != nil {
		return e
	}
	return err
}

// ruleFields maps the reason of a broken billing rule to the validation rule key the client sees.
var ruleFields = map[string]string{
	domain.ReasonUnknownService:           apperr.RuleUnknownService,
	domain.ReasonPreviousReadingRequired:  apperr.RulePreviousReadingRequired,
	domain.ReasonReadingLowerThanPrevious: apperr.RuleReadingLowerThanPrev,
	domain.ReasonNewReadingRequired:       apperr.RuleNewReadingRequired,
	domain.ReasonDayNeedsEnd:              apperr.RuleDayNeedsEnd,
	domain.ReasonNothingToBill:            apperr.RuleNothingToBill,
	domain.ReasonInvalidPeriod:            apperr.RuleInvalidPeriod,
}

// invoiceError is the API error of a domain error, or nil when err is not one of them.
func invoiceError(err error) *apperr.Error {
	var rule *domain.RuleViolation
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrForbidden):
		return apperr.Forbidden()
	case errors.Is(err, domain.ErrContractNotRunning):
		return apperr.Conflict(apperr.CodeInvoiceNotRunning)
	case errors.Is(err, domain.ErrAlreadyPaid):
		return apperr.Conflict(apperr.CodeInvoiceAlreadyPaid)
	case errors.Is(err, domain.ErrSendFirst):
		return apperr.Conflict(apperr.CodeInvoiceSendFirst)
	case errors.Is(err, domain.ErrLocked):
		return apperr.Conflict(apperr.CodeInvoiceLocked)
	case errors.Is(err, domain.ErrReviewPendingFirst):
		return apperr.Conflict(apperr.CodeInvoiceReviewPending)
	case errors.Is(err, domain.ErrLinkNeedsSent):
		return apperr.Conflict(apperr.CodeInvoiceLinkNeedsSent)
	case errors.Is(err, domain.ErrLinkInvalid):
		return apperr.E(apperr.KindNotFound, apperr.CodeInvoiceLinkInvalid)
	case errors.Is(err, domain.ErrPeriodBilled):
		return apperr.Invalid(apperr.Field{Name: domain.FieldPeriodStart, Rule: apperr.RulePeriodBilled})
	case errors.As(err, &rule):
		if key, ok := ruleFields[rule.Reason]; ok {
			return apperr.Invalid(apperr.Field{Name: rule.Field, Rule: key, Params: rule.Params})
		}
	}
	return nil
}
