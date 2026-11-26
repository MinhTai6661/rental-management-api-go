package identity

import (
	"errors"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestDecideLink(t *testing.T) {
	now := time.Now()
	linked := &OAuthAccount{ProviderUserID: "A", LinkedAt: now}
	unlinked := &OAuthAccount{ProviderUserID: "A", LinkedAt: now, UnlinkedAt: &now}
	tests := []struct {
		name     string
		existing *OAuthAccount
		id       string
		want     LinkDecision
	}{
		{"nothing on record", nil, "A", LinkCreate},
		{"same account, linked", linked, "A", LinkAlready},
		{"same account, unlinked", unlinked, "A", LinkRelink},
		{"another account, linked", linked, "B", LinkTaken},
		{"another account, unlinked", unlinked, "B", LinkReplace},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideLink(tc.existing, tc.id); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLinkEmailRefusal(t *testing.T) {
	tests := []struct {
		name            string
		vouches         bool
		account, remote *string
		want            error
	}{
		{"provider without email links freely", false, nil, nil, nil},
		{"provider without email, account with one", false, strPtr("a@x.vn"), nil, nil},
		{"same email, different case", true, strPtr("A@X.vn"), strPtr("a@x.VN"), nil},
		{"surrounding spaces ignored", true, strPtr(" a@x.vn "), strPtr("a@x.vn"), nil},
		{"different email", true, strPtr("a@x.vn"), strPtr("b@x.vn"), ErrLinkEmailMismatch},
		{"provider gave none", true, strPtr("a@x.vn"), nil, ErrLinkEmailMismatch},
		{"account has no email", true, nil, strPtr("a@x.vn"), ErrLinkNoEmail},
		{"account email blank", true, strPtr(" "), strPtr("a@x.vn"), ErrLinkNoEmail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LinkEmailRefusal(tc.vouches, tc.account, tc.remote); !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSignInMethodsCanUnlink(t *testing.T) {
	tests := []struct {
		name    string
		methods SignInMethods
		want    bool
	}{
		{"password and one provider", SignInMethods{true, []string{"google"}}, true},
		{"only provider", SignInMethods{false, []string{"google"}}, false},
		{"two providers, no password", SignInMethods{false, []string{"google", "zalo"}}, true},
		{"not linked", SignInMethods{true, []string{"zalo"}}, false},
		{"nothing linked", SignInMethods{true, nil}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.methods.CanUnlink("google"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPKCEChallengeMatchesRFC7636(t *testing.T) {
	// The example of RFC 7636, appendix B.
	got := PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestValidPKCEVerifier(t *testing.T) {
	rep := func(c string, n int) string {
		s := ""
		for i := 0; i < n; i++ {
			s += c
		}
		return s
	}
	tests := []struct {
		verifier string
		want     bool
	}{
		{rep("a", 43), true}, {rep("a", 128), true}, {rep("a", 42), false}, {rep("a", 129), false},
		{rep("a", 42) + "/", false}, {rep("a", 40) + "-._~", true},
	}
	for _, tc := range tests {
		if got := ValidPKCEVerifier(tc.verifier); got != tc.want {
			t.Errorf("len %d: got %v, want %v", len(tc.verifier), got, tc.want)
		}
	}
}

func TestSafeReturnPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"dashboard", "dashboard"}, {"/my-rooms", "my-rooms"}, {"a/b_c", "a/b_c"},
		{"//evil.com", "home"}, {"https://evil.com", "home"}, {"a?b=1", "home"}, {"", "home"}, {"../x", "home"},
	}
	for _, tc := range tests {
		if got := SafeReturnPath(tc.in, "home"); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
