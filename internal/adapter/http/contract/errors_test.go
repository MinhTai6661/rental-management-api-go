package contracthttp

import (
	"errors"
	"fmt"
	"testing"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
	invitation "rental-management/internal/domain/invitation"
)

func TestContractErrorsMapToTheAPIsKindsCodesAndParams(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		kind   apperr.Kind
		code   string
		field  string
		rule   string
		params map[string]string
	}{
		{"not found", domain.ErrNotFound, apperr.KindNotFound, apperr.CodeResourceNotFound, "", "", nil},
		{"room", domain.ErrRoomNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "room_id", apperr.RuleExists, nil},
		{"ids", domain.ErrItemsNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "ids", apperr.RuleExists, nil},
		{"transition", domain.ErrInvalidTransition, apperr.KindConflict, apperr.CodeContractTransition, "", "", nil},
		{"occupied", domain.ErrRoomOccupied, apperr.KindConflict, apperr.CodeContractRoomOccupied, "", "", nil},
		{"not vacant", domain.ErrRoomNotVacant, apperr.KindConflict, apperr.CodeContractNotVacant, "", "", nil},
		{"not listed", domain.ErrRoomNotListed, apperr.KindConflict, apperr.CodeContractNotListed, "", "", nil},
		{"registered", domain.ErrAlreadyRegistered, apperr.KindConflict, apperr.CodeContractRegistered, "", "", nil},
		{"own room", domain.ErrOwnRoom, apperr.KindConflict, apperr.CodeContractOwnRoom, "", "", nil},
		{"cancellation", domain.ErrCancellationNotAllowed, apperr.KindConflict, apperr.CodeContractCancelDenied, "", "", nil},
		{"not deletable", domain.ErrNotDeletable, apperr.KindConflict, apperr.CodeContractNotDeletable, "", "", nil},
		{"past", domain.ErrStartInPast, apperr.KindValidation, apperr.CodeValidationFailed, "starts_on", apperr.RuleDateNotPast, nil},
		{"end", domain.ErrEndNotAfterStart, apperr.KindValidation, apperr.CodeValidationFailed, "ends_on", apperr.RuleAfterStartDate, nil},
		{"occupants", domain.ErrOccupantsInvalid, apperr.KindValidation, apperr.CodeValidationFailed, "occupant_count", apperr.RuleMinNumber, map[string]string{"min": "1"}},
		{"months", &domain.BelowMinimumPeriodError{Amount: 3, Unit: domain.UnitMonth, Earliest: domain.Date(2027, 2, 23)}, apperr.KindValidation, apperr.CodeValidationFailed, "ends_on", apperr.RuleBelowMinMonths, map[string]string{"amount": "3", "date": "2027-02-23"}},
		{"days", &domain.BelowMinimumPeriodError{Amount: 10, Unit: domain.UnitDay, Earliest: domain.Date(2026, 5, 11)}, apperr.KindValidation, apperr.CodeValidationFailed, "ends_on", apperr.RuleBelowMinDays, map[string]string{"amount": "10", "date": "2026-05-11"}},
		{"bad invitation", domain.ErrInvitationInvalid, apperr.KindNotFound, apperr.CodeInvitationInvalid, "", "", nil},
		{"invitation answered", domain.ErrInvitationNotPending, apperr.KindConflict, apperr.CodeInvitationNotPending, "", "", nil},
		{"link unavailable", domain.ErrInvitationLinkUnavailable, apperr.KindNotFound, apperr.CodeInvitationLinkUnavailable, "", "", nil},
		{"own offer", domain.ErrInvitationOwnOffer, apperr.KindConflict, apperr.CodeInvitationOwnOffer, "", "", nil},
		{"recipient id", invitation.ErrRecipientInvalid, apperr.KindValidation, apperr.CodeValidationFailed, "recipient_id", apperr.RuleRecipientInvalid, nil},
		{"own email", invitation.ErrOwnEmail, apperr.KindValidation, apperr.CodeValidationFailed, "email", apperr.RuleOwnEmail, nil},
		{"recipient mismatch", invitation.ErrRecipientMismatch, apperr.KindValidation, apperr.CodeValidationFailed, "email", apperr.RuleRecipientMismatch, nil},
		{"contract limit", &domain.ContractLimitError{Limit: 4}, apperr.KindConflict, apperr.CodeContractLimit, "", "", map[string]string{"limit": "4"}},
		{"registration limit", &domain.RegistrationLimitError{Limit: 2}, apperr.KindConflict, apperr.CodeRegistrationLimit, "", "", map[string]string{"limit": "2"}},
	}
	for _, tc := range cases {
		// A use case may wrap what it returns; the mapping must still find it.
		got, ok := apperr.As(mapContractError(fmt.Errorf("wrapped: %w", tc.err)))
		if !ok || got.Kind != tc.kind || got.Code != tc.code {
			t.Errorf("%s: got %+v", tc.name, got)
			continue
		}
		if tc.field == "" {
			if fmt.Sprint(got.Params) != fmt.Sprint(tc.params) {
				t.Errorf("%s: params %v, want %v", tc.name, got.Params, tc.params)
			}
			continue
		}
		if len(got.Fields) != 1 || got.Fields[0].Name != tc.field || got.Fields[0].Rule != tc.rule || fmt.Sprint(got.Fields[0].Params) != fmt.Sprint(tc.params) {
			t.Errorf("%s: fields %+v, want %s %s %v", tc.name, got.Fields, tc.field, tc.rule, tc.params)
		}
	}
}

func TestEveryRefusalOfTheDomainIsMapped(t *testing.T) {
	for _, err := range []error{
		domain.ErrNotFound, domain.ErrRoomNotFound, domain.ErrItemsNotFound, domain.ErrInvalidTransition, domain.ErrRoomOccupied,
		domain.ErrRoomNotVacant, domain.ErrRoomNotListed, domain.ErrAlreadyRegistered, domain.ErrOwnRoom, domain.ErrCancellationNotAllowed,
		domain.ErrNotDeletable, domain.ErrStartInPast, domain.ErrEndNotAfterStart, domain.ErrOccupantsInvalid,
		&domain.BelowMinimumPeriodError{}, &domain.ContractLimitError{}, &domain.RegistrationLimitError{},
		domain.ErrInvitationInvalid, domain.ErrInvitationNotPending, domain.ErrInvitationLinkUnavailable, domain.ErrInvitationOwnOffer,
	} {
		if !domain.IsRefusal(err) {
			t.Errorf("%v is not reported as a refusal", err)
		}
		if contractError(err) == nil {
			t.Errorf("%v has no API error", err)
		}
	}
	if domain.IsRefusal(errors.New("boom")) {
		t.Error("an unknown error is not a refusal")
	}
}

func TestUnknownErrorsAreLeftAlone(t *testing.T) {
	boom := errors.New("boom")
	if got := mapContractError(boom); !errors.Is(got, boom) {
		t.Errorf("%v", got)
	}
	if mapContractError(nil) != nil {
		t.Error("no error, no mapping")
	}
	own := apperr.Malformed()
	if got := mapContractError(own); !errors.Is(got, own) {
		t.Errorf("%v", got)
	}
}
