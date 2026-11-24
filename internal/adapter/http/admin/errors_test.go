package adminhttp

import (
	"errors"
	"fmt"
	"testing"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		kind  apperr.Kind
		field string
		rule  string
	}{
		{"not found", shared.ErrNotFound, apperr.KindNotFound, "", ""},
		{"not a landlord is a 404", domain.ErrNotLandlord, apperr.KindNotFound, "", ""},
		{"unknown legal kind is a 404", domain.ErrUnknownKind, apperr.KindNotFound, "", ""},
		{"forbidden", fmt.Errorf("wrapped: %w", domain.ErrForbidden), apperr.KindForbidden, "", ""},
		{"empty roles", domain.ErrRolesEmpty, apperr.KindValidation, "roles", apperr.RuleMinItems},
		{"mixed staff roles", domain.ErrStaffRolesMixed, apperr.KindValidation, "roles", apperr.RuleStaffRolesMixed},
		{"running contracts", &domain.HasActiveContractsError{Count: 2}, apperr.KindValidation, "roles", apperr.RuleHasActiveContracts},
		{"already pending", domain.ErrAlreadyPending, apperr.KindValidation, "application", apperr.RuleAlreadyPending},
		{"not reviewable", domain.ErrNotReviewable, apperr.KindValidation, "application", apperr.RuleNotReviewable},
		{"erasure blocked", &domain.ErasureBlockedError{Blockers: []string{"x"}}, apperr.KindValidation, "account", apperr.RuleErasureBlocked},
		{"wrong password", domain.ErrPasswordWrong, apperr.KindValidation, "password", apperr.RulePasswordIncorrect},
		{"too many ids", domain.ErrTooManyIDs, apperr.KindValidation, "ids", apperr.RuleMaxItems},
		{"validation", domain.Invalid("content.vi.body", "richtext_empty", nil), apperr.KindValidation, "content.vi.body", "richtext_empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ae, ok := apperr.As(mapError(tc.err))
			if !ok || ae.Kind != tc.kind {
				t.Fatalf("want kind %v, got %+v", tc.kind, ae)
			}
			if tc.field != "" && (len(ae.Fields) == 0 || ae.Fields[0].Name != tc.field || ae.Fields[0].Rule != tc.rule) {
				t.Fatalf("want %s/%s, got %+v", tc.field, tc.rule, ae.Fields)
			}
		})
	}
	boom := errors.New("boom")
	if got := mapError(boom); !errors.Is(got, boom) {
		t.Fatalf("an unknown error comes back as it is: %v", got)
	}
}
