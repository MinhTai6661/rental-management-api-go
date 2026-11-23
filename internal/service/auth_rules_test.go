package service

import (
	"testing"
	"time"
)

func TestDecideRefresh(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	ptr := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	grace := 10 * time.Second

	cases := map[string]struct {
		state refreshState
		want  refreshDecision
	}{
		"fresh token":                  {refreshState{expiresAt: now.Add(time.Hour)}, refreshAllow},
		"expired":                      {refreshState{expiresAt: now.Add(-time.Second)}, refreshReject},
		"expires exactly now":          {refreshState{expiresAt: now}, refreshReject},
		"session closed":               {refreshState{expiresAt: now.Add(time.Hour), sessionRevokedAt: ptr(-time.Minute)}, refreshReject},
		"rotated a moment ago":         {refreshState{expiresAt: now.Add(time.Hour), rotatedAt: ptr(-3 * time.Second)}, refreshAllow},
		"rotated exactly grace ago":    {refreshState{expiresAt: now.Add(time.Hour), rotatedAt: ptr(-grace)}, refreshAllow},
		"rotated long ago: stolen":     {refreshState{expiresAt: now.Add(time.Hour), rotatedAt: ptr(-11 * time.Second)}, refreshReuse},
		"rotated and session closed":   {refreshState{expiresAt: now.Add(time.Hour), rotatedAt: ptr(-time.Hour), sessionRevokedAt: ptr(-time.Minute)}, refreshReject},
		"rotated long ago and expired": {refreshState{expiresAt: now.Add(-time.Minute), rotatedAt: ptr(-time.Hour)}, refreshReject},
	}
	for name, tc := range cases {
		if got := decideRefresh(tc.state, now, grace); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
}

func TestVerificationUsable(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	used := now.Add(-time.Minute)
	if !verificationUsable(now.Add(time.Hour), nil, now) {
		t.Error("an unused token before its expiry must be usable")
	}
	if verificationUsable(now.Add(time.Hour), &used, now) {
		t.Error("a used token must not be usable again")
	}
	if verificationUsable(now.Add(-time.Second), nil, now) || verificationUsable(now, nil, now) {
		t.Error("an expired token must not be usable")
	}
}

func TestResendAllowed(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	cooldown := 60 * time.Second
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	if !resendAllowed(nil, now, cooldown) {
		t.Error("no email was ever sent: allowed")
	}
	if resendAllowed(at(-30*time.Second), now, cooldown) {
		t.Error("30 s after the last email: refused")
	}
	if !resendAllowed(at(-60*time.Second), now, cooldown) || !resendAllowed(at(-2*time.Minute), now, cooldown) {
		t.Error("a full cooldown after the last email: allowed")
	}
}

func TestInitialRole(t *testing.T) {
	if got := initialRole([]string{"tenant", "landlord"}, ""); got != "landlord" {
		t.Errorf("no last role: the most senior one, got %q", got)
	}
	if got := initialRole([]string{"landlord", "tenant"}, "tenant"); got != "tenant" {
		t.Errorf("the role used last wins while it is still held, got %q", got)
	}
	if got := initialRole([]string{"tenant"}, "admin"); got != "tenant" {
		t.Errorf("a last role that is no longer held is ignored, got %q", got)
	}
	if got := initialRole(nil, "tenant"); got != "" {
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
	for _, a := range accounts {
		if len(a.Roles) == 0 {
			t.Errorf("%s has no role", a.Email)
		}
	}
}
