package planhttp

import (
	"errors"
	"fmt"
	"testing"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/plan"
)

func TestPlanErrorsMapToTheAPIsKindsCodesAndFields(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		kind  apperr.Kind
		code  string
		field string
		rule  string
	}{
		{"plan", domain.ErrPlanNotFound, apperr.KindNotFound, apperr.CodeResourceNotFound, "", ""},
		{"subscription", domain.ErrSubscriptionNotFound, apperr.KindNotFound, apperr.CodeResourceNotFound, "", ""},
		{"account", domain.ErrAccountNotFound, apperr.KindNotFound, apperr.CodeResourceNotFound, "", ""},
		{"unknown plan", domain.ErrPlanUnknown, apperr.KindValidation, apperr.CodeValidationFailed, "plan_id", apperr.RuleExists},
		{"audience", domain.ErrWrongAudience, apperr.KindConflict, apperr.CodePlanWrongAudience, "", ""},
		{"inactive", domain.ErrPlanInactive, apperr.KindConflict, apperr.CodePlanInactive, "", ""},
		{"already", domain.ErrAlreadyOnPlan, apperr.KindConflict, apperr.CodePlanAlreadyOn, "", ""},
		{"not pending", domain.ErrNotPending, apperr.KindConflict, apperr.CodePlanNotPending, "", ""},
		{"code taken", domain.ErrCodeTaken, apperr.KindValidation, apperr.CodeValidationFailed, "code", apperr.RuleUnique},
		{"code format", domain.ErrInvalidCode, apperr.KindValidation, apperr.CodeValidationFailed, "code", apperr.RuleFormat},
		{"end", domain.ErrEndInPast, apperr.KindValidation, apperr.CodeValidationFailed, "ends_at", apperr.RuleDateNotPast},
	}
	for _, tc := range cases {
		// A use case may wrap what it returns; the mapping must still find it.
		got, ok := apperr.As(mapPlanError(fmt.Errorf("wrapped: %w", tc.err)))
		if !ok || got.Kind != tc.kind || got.Code != tc.code {
			t.Errorf("%s: got %+v", tc.name, got)
			continue
		}
		if tc.field != "" && (len(got.Fields) != 1 || got.Fields[0].Name != tc.field || got.Fields[0].Rule != tc.rule) {
			t.Errorf("%s: fields %+v, want %s %s", tc.name, got.Fields, tc.field, tc.rule)
		}
	}
	if err := errors.New("db down"); !errors.Is(mapPlanError(err), err) {
		t.Error("an unknown error comes back as it is")
	}
}

func TestEveryDomainErrorIsMapped(t *testing.T) {
	for _, err := range []error{
		domain.ErrPlanNotFound, domain.ErrPlanUnknown, domain.ErrSubscriptionNotFound, domain.ErrAccountNotFound,
		domain.ErrWrongAudience, domain.ErrPlanInactive, domain.ErrAlreadyOnPlan, domain.ErrNotPending, domain.ErrCodeTaken,
		domain.ErrInvalidCode, domain.ErrEndInPast,
	} {
		if planError(err) == nil {
			t.Errorf("%v is not mapped", err)
		}
	}
}
