package invitation

import (
	"testing"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

func ptr[T any](v T) *T { return &v }

func TestAudienceOf(t *testing.T) {
	id := uuid.New()
	cases := map[string]struct {
		id    *uuid.UUID
		email *string
		want  Audience
	}{
		"account":              {&id, nil, AudienceAccount},
		"account and email":    {&id, ptr("a@b.vn"), AudienceAccount},
		"email only":           {nil, ptr("a@b.vn"), AudienceEmail},
		"nobody in particular": {nil, nil, AudiencePublic},
	}
	for name, tc := range cases {
		if got := AudienceOf(tc.id, tc.email); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestAllows(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	cases := map[string]struct {
		recipientID    *uuid.UUID
		recipientEmail *string
		userID         uuid.UUID
		userEmail      *string
		verified       bool
		want           bool
	}{
		"public: anybody, no email, unverified":    {nil, nil, me, nil, false, true},
		"account: the named one":                   {&me, nil, me, nil, false, true},
		"account: another id, even verified email": {&other, nil, me, ptr("a@b.vn"), true, false},
		"email: verified, other case":              {nil, ptr("new@example.com"), me, ptr("New@Example.com"), true, true},
		"email: unverified":                        {nil, ptr("new@example.com"), me, ptr("new@example.com"), false, false},
		"email: another address":                   {nil, ptr("new@example.com"), me, ptr("x@example.com"), true, false},
		"email: user without an address (Zalo)":    {nil, ptr("new@example.com"), me, nil, true, false},
	}
	for name, tc := range cases {
		if got := Allows(tc.recipientID, tc.recipientEmail, tc.userID, tc.userEmail, tc.verified); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct {
		in   *string
		want string // "" = nil
	}{
		{ptr("tai@gmail.com"), "t**@gmail.com"},
		{ptr("renter@example.com"), "r*****@example.com"},
		{ptr("a@x.vn"), "a**@x.vn"},
		{ptr("đức@x.vn"), "đ**@x.vn"},
		{ptr("no-at"), ""},
		{ptr("@x.vn"), ""},
		{nil, ""},
	}
	for _, tc := range cases {
		got := MaskEmail(tc.in)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("MaskEmail(%v) = %q, want nil", tc.in, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("MaskEmail(%q) = %v, want %q", *tc.in, got, tc.want)
		}
	}
}

func TestInvitable(t *testing.T) {
	inviter := uuid.New()
	base := func() Person {
		return Person{ID: uuid.New(), Active: true, AcceptsInvitations: true, Roles: []string{authz.RoleTenant}}
	}
	cases := map[string]struct {
		mutate func(*Person)
		want   bool
	}{
		"a tenant":                   {func(*Person) {}, true},
		"a landlord who is also one": {func(p *Person) { p.Roles = []string{authz.RoleLandlord, authz.RoleTenant} }, true},
		"the inviter":                {func(p *Person) { p.ID = inviter }, false},
		"inactive":                   {func(p *Person) { p.Active = false }, false},
		"opted out":                  {func(p *Person) { p.AcceptsInvitations = false }, false},
		"no tenant role":             {func(p *Person) { p.Roles = []string{authz.RoleLandlord} }, false},
		"admin":                      {func(p *Person) { p.Roles = []string{authz.RoleAdmin} }, false},
		"tenant and admin":           {func(p *Person) { p.Roles = []string{authz.RoleTenant, authz.RoleAdmin} }, false},
	}
	for name, tc := range cases {
		p := base()
		tc.mutate(&p)
		if got := Invitable(p, inviter); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestNormalizeAndBlockHash(t *testing.T) {
	if got := NormalizeEmail(" A@B.VN "); got != "a@b.vn" {
		t.Errorf("NormalizeEmail = %q", got)
	}
	if BlockHash("A@b.vn") != BlockHash(" a@B.VN ") {
		t.Error("the hash must not depend on case or spaces")
	}
	if BlockHash("a@b.vn") == BlockHash("c@b.vn") || len(BlockHash("a@b.vn")) != 64 {
		t.Error("different addresses hash differently, to 64 hex characters")
	}
}

func TestAddresseeAudience(t *testing.T) {
	if (Addressee{}).Audience() != AudiencePublic {
		t.Error("nobody named is public")
	}
	if (Addressee{Email: ptr("a@b.vn")}).Audience() != AudienceEmail {
		t.Error("an address is email")
	}
	if (Addressee{Recipient: &Person{ID: uuid.New()}, Email: ptr("a@b.vn")}).Audience() != AudienceAccount {
		t.Error("a named account wins")
	}
}

func TestParseLookupBy(t *testing.T) {
	for _, s := range []string{"email", "id"} {
		if by, ok := ParseLookupBy(s); !ok || string(by) != s {
			t.Errorf("ParseLookupBy(%q) = %q, %v", s, by, ok)
		}
	}
	if _, ok := ParseLookupBy("name"); ok {
		t.Error("name is not a way to look an account up")
	}
}
