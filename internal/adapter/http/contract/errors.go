package contracthttp

import (
	"errors"
	"strconv"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
	invitation "rental-management/internal/domain/invitation"
)

// mapContractError turns the errors of the contract use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapContractError(err error) error {
	if e := contractError(err); e != nil {
		return e
	}
	return err
}

// contractError is the API error of a domain error, or nil when err is not one of them.
func contractError(err error) *apperr.Error {
	field := func(name, rule string, params map[string]string) *apperr.Error {
		return apperr.Invalid(apperr.Field{Name: name, Rule: rule, Params: params})
	}
	var (
		below    *domain.BelowMinimumPeriodError
		limit    *domain.ContractLimitError
		regLimit *domain.RegistrationLimitError
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrRoomNotFound):
		return field("room_id", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrItemsNotFound):
		return field("ids", apperr.RuleExists, nil)
	case errors.Is(err, domain.ErrInvalidTransition):
		return apperr.Conflict(apperr.CodeContractTransition)
	case errors.Is(err, domain.ErrRoomOccupied):
		return apperr.Conflict(apperr.CodeContractRoomOccupied)
	case errors.Is(err, domain.ErrRoomNotVacant):
		return apperr.Conflict(apperr.CodeContractNotVacant)
	case errors.Is(err, domain.ErrRoomNotListed):
		return apperr.Conflict(apperr.CodeContractNotListed)
	case errors.Is(err, domain.ErrAlreadyRegistered):
		return apperr.Conflict(apperr.CodeContractRegistered)
	case errors.Is(err, domain.ErrOwnRoom):
		return apperr.Conflict(apperr.CodeContractOwnRoom)
	case errors.Is(err, domain.ErrCancellationNotAllowed):
		return apperr.Conflict(apperr.CodeContractCancelDenied)
	case errors.Is(err, domain.ErrNotDeletable):
		return apperr.Conflict(apperr.CodeContractNotDeletable)
	case errors.Is(err, domain.ErrStartInPast):
		return field("starts_on", apperr.RuleDateNotPast, nil)
	case errors.Is(err, domain.ErrEndNotAfterStart):
		return field("ends_on", apperr.RuleAfterStartDate, nil)
	case errors.Is(err, domain.ErrOccupantsInvalid):
		return field("occupant_count", apperr.RuleMinNumber, map[string]string{"min": "1"})
	case errors.Is(err, domain.ErrInvitationInvalid):
		return apperr.E(apperr.KindNotFound, apperr.CodeInvitationInvalid)
	case errors.Is(err, domain.ErrInvitationNotPending):
		return apperr.Conflict(apperr.CodeInvitationNotPending)
	case errors.Is(err, domain.ErrInvitationLinkUnavailable):
		return apperr.E(apperr.KindNotFound, apperr.CodeInvitationLinkUnavailable)
	case errors.Is(err, domain.ErrInvitationOwnOffer):
		return apperr.Conflict(apperr.CodeInvitationOwnOffer)
	case errors.Is(err, invitation.ErrRecipientInvalid):
		return field("recipient_id", apperr.RuleRecipientInvalid, nil)
	case errors.Is(err, invitation.ErrOwnEmail):
		return field("email", apperr.RuleOwnEmail, nil)
	case errors.Is(err, invitation.ErrRecipientMismatch):
		return field("email", apperr.RuleRecipientMismatch, nil)
	case errors.As(err, &below):
		rule := apperr.RuleBelowMinMonths
		if below.Unit == domain.UnitDay {
			rule = apperr.RuleBelowMinDays
		}
		return field("ends_on", rule, map[string]string{
			"amount": strconv.Itoa(int(below.Amount)), "date": below.Earliest.Format(domain.DateLayout),
		})
	case errors.As(err, &limit):
		return apperr.Conflict(apperr.CodeContractLimit).WithParams(map[string]string{"limit": strconv.Itoa(limit.Limit)})
	case errors.As(err, &regLimit):
		return apperr.Conflict(apperr.CodeRegistrationLimit).WithParams(map[string]string{"limit": strconv.Itoa(regLimit.Limit)})
	}
	return nil
}
