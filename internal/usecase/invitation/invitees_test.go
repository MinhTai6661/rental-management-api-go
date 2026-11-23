package invitation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invitation"
	"rental-management/pkg/authz"
)

func TestLookupFindsOnlyByExactEmailOrID(t *testing.T) {
	f := newFixture(t, nil)
	renter := f.add(person("renter@example.com", nil))
	ctx := context.Background()

	got, err := f.uc.Lookup(ctx, f.landlord.ID, domain.LookupByEmail, "  Renter@Example.com ")
	if err != nil || got == nil || got.ID != renter.ID {
		t.Fatalf("exact email (other case) should find the renter: %v, %v", got, err)
	}
	got, err = f.uc.Lookup(ctx, f.landlord.ID, domain.LookupByID, renter.ID.String())
	if err != nil || got == nil || got.ID != renter.ID {
		t.Fatalf("exact id should find the renter: %v, %v", got, err)
	}
	for name, tc := range map[string]struct {
		by    domain.LookupBy
		value string
	}{
		"partial email":    {domain.LookupByEmail, "renter@"},
		"a name":           {domain.LookupByEmail, "Renter"},
		"not a uuid":       {domain.LookupByID, "not-a-uuid"},
		"unknown id":       {domain.LookupByID, uuid.NewString()},
		"empty":            {domain.LookupByEmail, ""},
		"unknown by":       {domain.LookupBy("name"), "renter"},
		"the inviter":      {domain.LookupByID, f.landlord.ID.String()},
		"inviter by email": {domain.LookupByEmail, "landlord@example.vn"},
	} {
		if got, err := f.uc.Lookup(ctx, f.landlord.ID, tc.by, tc.value); err != nil || got != nil {
			t.Errorf("%s: found %v, err %v; want nobody", name, got, err)
		}
	}
}

func TestLookupHidesWhoCannotBeInvited(t *testing.T) {
	f := newFixture(t, nil)
	staff := f.add(person("admin@example.vn", func(p *domain.Person) { p.Roles = []string{authz.RoleAdmin} }))
	inactive := f.add(person("gone@example.vn", func(p *domain.Person) { p.Active = false }))
	optedOut := f.add(person("quiet@example.vn", func(p *domain.Person) { p.AcceptsInvitations = false }))
	for _, p := range []domain.Person{staff, inactive, optedOut} {
		if got, err := f.uc.Find(context.Background(), f.landlord.ID, domain.LookupByID, p.ID.String()); err != nil || got != nil {
			t.Errorf("%s must read as nobody: %v, %v", *p.Email, got, err)
		}
		if got, err := f.uc.Find(context.Background(), f.landlord.ID, domain.LookupByEmail, *p.Email); err != nil || got != nil {
			t.Errorf("%s by email must read as nobody: %v, %v", *p.Email, got, err)
		}
	}
}

func TestLookupAuditsHowAndWhetherButNeverTheTypedValue(t *testing.T) {
	f := newFixture(t, nil)
	renter := f.add(person("renter@example.com", nil))
	ctx := context.Background()
	if _, err := f.uc.Lookup(ctx, f.landlord.ID, domain.LookupByEmail, "renter@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Lookup(ctx, f.landlord.ID, domain.LookupByEmail, "nobody@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(f.audit.events) != 2 {
		t.Fatalf("two lookups, %d events", len(f.audit.events))
	}
	hit, miss := f.audit.events[0], f.audit.events[1]
	if hit.Action != AuditLookup || hit.ActorID != f.landlord.ID || hit.Meta["by"] != "email" || hit.Meta["found"] != true ||
		hit.TargetUserID == nil || *hit.TargetUserID != renter.ID {
		t.Errorf("hit event %+v", hit)
	}
	if miss.Meta["found"] != false || miss.TargetUserID != nil {
		t.Errorf("miss event %+v", miss)
	}
	noValueInMeta(t, f.audit.events, "renter@example.com")
	noValueInMeta(t, f.audit.events, "nobody@example.com")
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	t.Run("nobody named is public", func(t *testing.T) {
		f := newFixture(t, nil)
		got, err := f.uc.Resolve(ctx, f.landlord.ID, nil, ptr("   "))
		if err != nil || got.Audience() != domain.AudiencePublic || got.Email != nil {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("an id names the account", func(t *testing.T) {
		f := newFixture(t, nil)
		renter := f.add(person("renter@example.com", nil))
		got, err := f.uc.Resolve(ctx, f.landlord.ID, &renter.ID, nil)
		if err != nil || got.Recipient == nil || got.Recipient.ID != renter.ID || got.Audience() != domain.AudienceAccount {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("the address of an account is that account", func(t *testing.T) {
		f := newFixture(t, nil)
		renter := f.add(person("renter@example.com", nil))
		got, err := f.uc.Resolve(ctx, f.landlord.ID, nil, ptr(" Renter@Example.com "))
		if err != nil || got.Recipient == nil || got.Recipient.ID != renter.ID || got.Email != nil {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("an unknown address stays a plain address, normalized", func(t *testing.T) {
		f := newFixture(t, nil)
		got, err := f.uc.Resolve(ctx, f.landlord.ID, nil, ptr(" New.Person@Example.vn "))
		if err != nil || got.Recipient != nil || got.Email == nil || *got.Email != "new.person@example.vn" || got.Audience() != domain.AudienceEmail {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("id and the same account's address are fine", func(t *testing.T) {
		f := newFixture(t, nil)
		renter := f.add(person("renter@example.com", nil))
		got, err := f.uc.Resolve(ctx, f.landlord.ID, &renter.ID, ptr("renter@example.com"))
		if err != nil || got.Recipient == nil || got.Recipient.ID != renter.ID {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		f := newFixture(t, nil)
		renter := f.add(person("renter@example.com", nil))
		other := f.add(person("other@example.com", nil))
		staff := f.add(person("admin@example.vn", func(p *domain.Person) { p.Roles = []string{authz.RoleAdmin} }))
		unknown := uuid.New()
		cases := map[string]struct {
			id    *uuid.UUID
			email *string
			want  error
		}{
			"unknown id":                 {&unknown, nil, domain.ErrRecipientInvalid},
			"staff id":                   {&staff.ID, nil, domain.ErrRecipientInvalid},
			"the inviter's id":           {&f.landlord.ID, nil, domain.ErrRecipientInvalid},
			"the inviter's own address":  {nil, ptr("LANDLORD@example.vn"), domain.ErrOwnEmail},
			"id and another one's email": {&renter.ID, ptr("other@example.com"), domain.ErrRecipientMismatch},
		}
		_ = other
		for name, tc := range cases {
			if _, err := f.uc.Resolve(ctx, f.landlord.ID, tc.id, tc.email); !errors.Is(err, tc.want) {
				t.Errorf("%s: err %v, want %v", name, err, tc.want)
			}
		}
	})
	t.Run("a staff or opted-out address is only a plain address", func(t *testing.T) {
		f := newFixture(t, nil)
		f.add(person("admin@example.vn", func(p *domain.Person) { p.Roles = []string{authz.RoleAdmin} }))
		f.add(person("quiet@example.vn", func(p *domain.Person) { p.AcceptsInvitations = false }))
		for _, email := range []string{"admin@example.vn", "quiet@example.vn"} {
			got, err := f.uc.Resolve(ctx, f.landlord.ID, nil, ptr(email))
			if err != nil || got.Recipient != nil || got.Email == nil || *got.Email != email {
				t.Errorf("%s: %+v, %v", email, got, err)
			}
		}
	})
}

func TestMayMail(t *testing.T) {
	f := newFixture(t, fakeSigner{})
	f.add(person("quiet@example.vn", func(p *domain.Person) { p.AcceptsInvitations = false }))
	ctx := context.Background()
	for email, want := range map[string]bool{"fresh@example.vn": true, "QUIET@example.vn": false} {
		if got, err := f.uc.MayMail(ctx, email); err != nil || got != want {
			t.Errorf("MayMail(%q) = %v, %v; want %v", email, got, err, want)
		}
	}
	f.blocks.hashes[domain.BlockHash("blocked@example.vn")] = true
	if got, err := f.uc.MayMail(ctx, " Blocked@Example.vn "); err != nil || got {
		t.Errorf("a blocked address (any case) is not mailed: %v, %v", got, err)
	}
}

func TestOptOut(t *testing.T) {
	f := newFixture(t, fakeSigner{})
	ctx := context.Background()
	if err := f.uc.OptOut(ctx, "a@example.vn", "wrong"); !errors.Is(err, domain.ErrOptOutInvalid) {
		t.Errorf("bad token: %v", err)
	}
	if len(f.blocks.hashes) != 0 {
		t.Error("a bad token stores nothing")
	}
	// The upper-case address is the same address.
	for range 2 {
		if err := f.uc.OptOut(ctx, "A@Example.vn", "sig:a@example.vn"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.blocks.hashes) != 1 || !f.blocks.hashes[domain.BlockHash("a@example.vn")] {
		t.Errorf("one hash of the normalized address: %v", f.blocks.hashes)
	}
	if ok, _ := f.uc.MayMail(ctx, "a@example.vn"); ok {
		t.Error("an address that opted out is not mailed")
	}

	unsigned := newFixture(t, nil)
	if err := unsigned.uc.OptOut(ctx, "a@example.vn", "sig:a@example.vn"); !errors.Is(err, domain.ErrOptOutInvalid) {
		t.Errorf("without a signer no link is valid: %v", err)
	}
}

func TestOptOutURL(t *testing.T) {
	f := newFixture(t, fakeSigner{})
	got := f.uc.OptOutURL(" New+Person@Example.vn ")
	if !strings.HasPrefix(got, "http://web.test/invitation-opt-out?") || !strings.Contains(got, "email=new%2Bperson%40example.vn") ||
		!strings.Contains(got, "token=sig%3Anew%2Bperson%40example.vn") {
		t.Errorf("url %q", got)
	}
	if newFixture(t, nil).uc.OptOutURL("a@b.vn") != "" {
		t.Error("no signer, no link")
	}
}

func TestErrorsPassThrough(t *testing.T) {
	f := newFixture(t, nil)
	f.uc.people = failingPeople{}
	if _, err := f.uc.Find(context.Background(), f.landlord.ID, domain.LookupByEmail, "a@b.vn"); !errors.Is(err, errBoom) {
		t.Errorf("a store failure is returned, not read as nobody: %v", err)
	}
}

type failingPeople struct{ PeopleRepository }

func (failingPeople) ByEmail(context.Context, string) (domain.Person, error) {
	return domain.Person{}, errBoom
}
