package apperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"rental-management/pkg/i18n"
)

func TestConstructors(t *testing.T) {
	cases := map[string]struct {
		err  *Error
		kind Kind
		code string
	}{
		"unauthenticated":     {Unauthenticated(), KindUnauthenticated, CodeUnauthenticated},
		"invalid credentials": {InvalidCredentials(), KindUnauthenticated, CodeInvalidCredentials},
		"inactive":            {AccountInactive(), KindForbidden, CodeAccountInactive},
		"forbidden":           {Forbidden(), KindForbidden, CodeForbidden},
		"not found":           {NotFound(), KindNotFound, CodeResourceNotFound},
		"malformed":           {Malformed(), KindBadRequest, CodeRequestMalformed},
		"too large":           {TooLarge(), KindTooLarge, CodeRequestTooLarge},
		"busy":                {Busy(nil), KindBusy, CodeServerBusy},
		"internal":            {Internal(nil), KindInternal, CodeServerInternal},
		"conflict":            {Conflict(CodeRoomHasContract), KindConflict, CodeRoomHasContract},
		"invalid":             {Invalid(Field{Name: "x", Rule: RuleRequired}), KindValidation, CodeValidationFailed},
	}
	for name, tc := range cases {
		if tc.err.Kind != tc.kind || tc.err.Code != tc.code {
			t.Errorf("%s: got kind %d code %q, want %d %q", name, tc.err.Kind, tc.err.Code, tc.kind, tc.code)
		}
	}
}

func TestHeadersAndCause(t *testing.T) {
	if got := RateLimited(30).Headers["Retry-After"]; got != "30" {
		t.Errorf("Retry-After = %q", got)
	}
	if got := MethodNotAllowed("GET, HEAD").Headers["Allow"]; got != "GET, HEAD" {
		t.Errorf("Allow = %q", got)
	}
	cause := errors.New("db down")
	err := Internal(cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "db down") {
		t.Errorf("cause not wrapped: %v", err)
	}
}

func TestAsFindsWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("load user: %w", Forbidden())
	if ae, ok := As(wrapped); !ok || ae.Code != CodeForbidden {
		t.Errorf("As = %v, %v", ae, ok)
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Error("a plain error is not an *Error")
	}
}

func TestCodesAndRulesAreUnique(t *testing.T) {
	for name, list := range map[string][]string{"codes": Codes(), "rules": Rules()} {
		seen := map[string]bool{}
		for _, item := range list {
			if seen[item] {
				t.Errorf("%s: %q listed twice", name, item)
			}
			seen[item] = true
		}
	}
}

// Every error code and validation rule the code can produce must have a message in both languages.
func TestEveryCodeAndRuleIsTranslated(t *testing.T) {
	c, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{i18n.Vietnamese, i18n.English} {
		l := c.Localizer(locale)
		for _, code := range Codes() {
			if !l.Has("errors." + code) {
				t.Errorf("%s: no message for error code %q", locale, code)
			}
		}
		for _, rule := range Rules() {
			if !l.Has("validation." + rule) {
				t.Errorf("%s: no message for validation rule %q", locale, rule)
			}
		}
	}
}
