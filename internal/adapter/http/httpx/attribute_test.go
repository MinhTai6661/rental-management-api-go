package httpx

import (
	"testing"

	"rental-management/pkg/i18n"
)

func TestAttributeName(t *testing.T) {
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	l := catalog.Localizer("en")
	for field, want := range map[string]string{
		"email":                "email",
		"charges.2.name":       "name",
		"targeting.roles.0":    "roles", // a list position is not a name
		"people_ids":           "people",
		"items.1.link_url":     "link url",
		"targeting.user_ids.3": "user ids",
	} {
		if got := attributeName(l, field); got != want {
			t.Errorf("%s: want %q, got %q", field, want, got)
		}
	}
}
