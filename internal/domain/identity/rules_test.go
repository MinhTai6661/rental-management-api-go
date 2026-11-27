package identity

import (
	"errors"
	"testing"
	"time"
)

func TestDecideRefresh(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	ptr := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	grace := 10 * time.Second

	cases := map[string]struct {
		state RefreshState
		want  RefreshDecision
	}{
		"fresh token":                  {RefreshState{ExpiresAt: now.Add(time.Hour)}, RefreshAllow},
		"expired":                      {RefreshState{ExpiresAt: now.Add(-time.Second)}, RefreshReject},
		"expires exactly now":          {RefreshState{ExpiresAt: now}, RefreshReject},
		"session closed":               {RefreshState{ExpiresAt: now.Add(time.Hour), SessionRevokedAt: ptr(-time.Minute)}, RefreshReject},
		"rotated a moment ago":         {RefreshState{ExpiresAt: now.Add(time.Hour), RotatedAt: ptr(-3 * time.Second)}, RefreshAllow},
		"rotated exactly grace ago":    {RefreshState{ExpiresAt: now.Add(time.Hour), RotatedAt: ptr(-grace)}, RefreshAllow},
		"rotated long ago: stolen":     {RefreshState{ExpiresAt: now.Add(time.Hour), RotatedAt: ptr(-11 * time.Second)}, RefreshReuse},
		"rotated and session closed":   {RefreshState{ExpiresAt: now.Add(time.Hour), RotatedAt: ptr(-time.Hour), SessionRevokedAt: ptr(-time.Minute)}, RefreshReject},
		"rotated long ago and expired": {RefreshState{ExpiresAt: now.Add(-time.Minute), RotatedAt: ptr(-time.Hour)}, RefreshReject},
	}
	for name, tc := range cases {
		if got := DecideRefresh(tc.state, now, grace); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
}

func TestVerificationUsable(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	used := now.Add(-time.Minute)
	if !VerificationUsable(now.Add(time.Hour), nil, now) {
		t.Error("an unused token before its expiry must be usable")
	}
	if VerificationUsable(now.Add(time.Hour), &used, now) {
		t.Error("a used token must not be usable again")
	}
	if VerificationUsable(now.Add(-time.Second), nil, now) || VerificationUsable(now, nil, now) {
		t.Error("an expired token must not be usable")
	}
}

func TestResendAllowed(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	cooldown := 60 * time.Second
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	if !ResendAllowed(nil, now, cooldown) {
		t.Error("no email was ever sent: allowed")
	}
	if ResendAllowed(at(-30*time.Second), now, cooldown) {
		t.Error("30 s after the last email: refused")
	}
	if !ResendAllowed(at(-60*time.Second), now, cooldown) || !ResendAllowed(at(-2*time.Minute), now, cooldown) {
		t.Error("a full cooldown after the last email: allowed")
	}
}

func TestInitialRole(t *testing.T) {
	if got := InitialRole([]string{"tenant", "landlord"}, ""); got != "landlord" {
		t.Errorf("no last role: the most senior one, got %q", got)
	}
	if got := InitialRole([]string{"landlord", "tenant"}, "tenant"); got != "tenant" {
		t.Errorf("the role used last wins while it is still held, got %q", got)
	}
	if got := InitialRole([]string{"tenant"}, "admin"); got != "tenant" {
		t.Errorf("a last role that is no longer held is ignored, got %q", got)
	}
	if got := InitialRole(nil, "tenant"); got != "" {
		t.Errorf("no roles at all gives no role, got %q", got)
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Ann.Lee@Example.VN "); got != "ann.lee@example.vn" {
		t.Errorf("got %q", got)
	}
}

func TestDemoAccountsNeverMixStaffWithOthers(t *testing.T) {
	accounts := DemoAccounts()
	if len(accounts) != 4 {
		t.Fatalf("expected 4 demo accounts, got %d", len(accounts))
	}
	samples := SampleAccounts()
	if len(samples) != 6 {
		t.Fatalf("expected 6 sample accounts, got %d", len(samples))
	}
	seen := map[string]bool{}
	for _, a := range append(accounts, samples...) {
		if len(a.Roles) == 0 {
			t.Errorf("%s has no role", a.Email)
		}
		if err := ValidateRoleGrant(a.Roles); err != nil {
			t.Errorf("%s: %v", a.Email, err)
		}
		if seen[a.Email] {
			t.Errorf("%s is listed twice", a.Email)
		}
		seen[a.Email] = true
	}
}

func TestValidateRoleGrant(t *testing.T) {
	if err := ValidateRoleGrant([]string{"landlord", "tenant"}); err != nil {
		t.Errorf("landlord with tenant is fine: %v", err)
	}
	if err := ValidateRoleGrant([]string{"admin", "tenant"}); !errors.Is(err, ErrStaffRolesMixed) {
		t.Errorf("staff mixed with others must be refused, got %v", err)
	}
	if err := ValidateRoleGrant([]string{"wizard"}); !errors.Is(err, ErrUnknownRole) {
		t.Errorf("an unknown role must be refused, got %v", err)
	}
}
