package i18n

import (
	"encoding/json"
	"sort"
	"testing"

	"rental-management/pkg/apperr"
)

func TestNegotiate(t *testing.T) {
	cases := []struct {
		name, header, want string
	}{
		{"no header", "", "vi"},
		{"english with region", "en-US,en;q=0.9", "en"},
		{"region only falls back to base", "en-GB", "en"},
		{"unsupported language", "fr", "vi"},
		{"unsupported first, supported later", "fr,en;q=0.5", "en"},
		{"quality order beats header order", "vi;q=0.3,en;q=0.9", "en"},
		{"vietnamese", "vi-VN,vi;q=0.9,en;q=0.8", "vi"},
		{"wildcard only", "*", "vi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Negotiate(tc.header); got != tc.want {
				t.Errorf("Negotiate(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestCatalogLoadsBothLanguagesWithSameKeys(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatal(err)
	}
	keys := func(locale string) []string {
		raw, err := localeFiles.ReadFile("locales/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range messages {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	vi, en := keys(Vietnamese), keys(English)
	if len(vi) == 0 || len(vi) != len(en) {
		t.Fatalf("vi has %d keys, en has %d", len(vi), len(en))
	}
	for i := range vi {
		if vi[i] != en[i] {
			t.Errorf("key mismatch: vi %q, en %q", vi[i], en[i])
		}
	}
}

// Every error code and validation rule the code can produce must have a message in both languages.
func TestEveryCodeAndRuleIsTranslated(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{Vietnamese, English} {
		l := c.Localizer(locale)
		for _, code := range apperr.Codes() {
			if !l.Has("errors." + code) {
				t.Errorf("%s: no message for error code %q", locale, code)
			}
		}
		for _, rule := range apperr.Rules() {
			if !l.Has("validation." + rule) {
				t.Errorf("%s: no message for validation rule %q", locale, rule)
			}
		}
	}
}

func TestTranslateReplacesPlaceholders(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]string{"attribute": "per page", "max": "100"}
	if got, want := c.Localizer(English).T("validation.max_number", args), "The per page field must not be greater than 100."; got != want {
		t.Errorf("en: got %q, want %q", got, want)
	}
	if got, want := c.Localizer(Vietnamese).T("validation.max_number", args), "Trường per page không được lớn hơn 100."; got != want {
		t.Errorf("vi: got %q, want %q", got, want)
	}
}

func TestTranslateMissingKeyReturnsKey(t *testing.T) {
	c, _ := New()
	if got := c.Localizer(English).T("nope.missing", nil); got != "nope.missing" {
		t.Errorf("got %q", got)
	}
}

func TestUnknownLocaleFallsBackToVietnamese(t *testing.T) {
	c, _ := New()
	if got := c.Localizer("fr").Locale(); got != Vietnamese {
		t.Errorf("got %q", got)
	}
}

// A text without placeholders ignores the arguments, and one with placeholders still renders when an argument is
// missing instead of failing the request.
func TestTranslateWithoutOrWithMissingArgs(t *testing.T) {
	c, _ := New()
	l := c.Localizer(English)
	if got, want := l.T("errors.auth.unauthenticated", map[string]string{"x": "y"}), "Please log in."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := l.T("validation.required", nil); got == "validation.required" || got == "" {
		t.Errorf("a message with a missing argument must still render, got %q", got)
	}
}
