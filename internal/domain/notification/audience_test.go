package notification

import (
	"testing"

	"rental-management/pkg/authz"
)

func TestVisibleTo(t *testing.T) {
	cases := map[string]struct {
		only     Audience
		filtered bool
	}{
		authz.RoleTenant:     {AudienceTenant, true},
		authz.RoleLandlord:   {AudienceLandlord, true},
		authz.RoleManager:    {AudienceManager, true},
		authz.RoleAdmin:      {AudienceNone, false},
		authz.RoleSuperAdmin: {AudienceNone, false},
		"":                   {AudienceNone, false},
	}
	for role, want := range cases {
		only, filtered := VisibleTo(role)
		if only != want.only || filtered != want.filtered {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", role, only, filtered, want.only, want.filtered)
		}
	}
}

func TestResolveAudienceExplicitWins(t *testing.T) {
	if got := ResolveAudience(AudienceManager, InvoiceSent); got != AudienceManager {
		t.Errorf("explicit must win, got %q", got)
	}
	if got := ResolveAudience(AudienceNone, InvoiceSent); got != AudienceTenant {
		t.Errorf("default expected, got %q", got)
	}
	if got := ResolveAudience(AudienceNone, ContractEnded); got != AudienceNone {
		t.Errorf("no audience expected, got %q", got)
	}
}

func TestParseAudience(t *testing.T) {
	for _, s := range []string{"", "landlord", "tenant", "manager"} {
		if _, err := ParseAudience(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if _, err := ParseAudience("admin"); err == nil {
		t.Error("admin is not an audience")
	}
}
