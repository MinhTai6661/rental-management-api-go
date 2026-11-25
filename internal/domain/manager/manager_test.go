package manager

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

func caps(c ...Capability) []Capability { return c }

func TestChange(t *testing.T) {
	cases := map[string]struct {
		current, grant, revoke []Capability
		want                   []Capability
	}{
		"grant adds what it requires":             {nil, caps(ContractsCreate), nil, caps(ContractsCreate, ContractsPropose)},
		"revoking a requirement cascades":         {caps(ContractsCreate, ContractsPropose), nil, caps(ContractsPropose), nil},
		"revoke wins over grant":                  {nil, caps(PaymentsReview), caps(PaymentsReview), nil},
		"grant and revoke the requirement":        {nil, caps(ContractsCreate), caps(ContractsPropose), nil},
		"duplicates collapse and sort":            {caps(PaymentsReview), caps(PaymentsReview, ContractsPropose), nil, caps(ContractsPropose, PaymentsReview)},
		"nothing changes":                         {caps(ReviewRegistrations), nil, nil, caps(ReviewRegistrations)},
		"create without propose in DB is dropped": {caps(ContractsCreate), nil, nil, nil},
	}
	for name, tc := range cases {
		got := Change(tc.current, tc.grant, tc.revoke)
		if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestSetAndParse(t *testing.T) {
	if got := Set(caps(ContractsCreate)); !slices.Equal(got, caps(ContractsCreate, ContractsPropose)) {
		t.Errorf("Set: %v", got)
	}
	if _, err := ParseCapability("payments.refund"); !errors.Is(err, ErrUnknownCapability) {
		t.Errorf("unknown code: %v", err)
	}
	if _, err := ParseCapabilities([]string{"payments.review", "nope"}); !errors.Is(err, ErrUnknownCapability) {
		t.Errorf("unknown in list: %v", err)
	}
	// A code this version does not know is ignored when read.
	if got := KnownCapabilities([]string{"nope", "payments.review", "payments.review"}); !slices.Equal(got, caps(PaymentsReview)) {
		t.Errorf("KnownCapabilities: %v", got)
	}
}

func TestCatalogueIsStable(t *testing.T) {
	want := map[Capability]struct {
		group string
		risk  Risk
	}{
		"payments.review":                {"payments", RiskMedium},
		"contracts.review_registrations": {"contracts", RiskMedium},
		"contracts.propose":              {"contracts", RiskLow},
		"contracts.create":               {"contracts", RiskHigh},
	}
	got := Catalogue()
	if len(got) != len(want) {
		t.Fatalf("catalogue has %d entries, want %d", len(got), len(want))
	}
	for _, c := range got {
		if w, ok := want[c.Code]; !ok || w.group != c.Group || w.risk != c.Risk {
			t.Errorf("%s changed meaning: %+v", c.Code, c)
		}
	}
}

func TestCanBeAsked(t *testing.T) {
	landlord := uuid.New()
	base := func() Candidate { return Candidate{ID: uuid.New(), Active: true, Roles: []string{authz.RoleTenant}} }
	cases := map[string]struct {
		mutate func(*Candidate)
		ok     bool
	}{
		"a tenant":                   {func(*Candidate) {}, true},
		"a landlord who is a tenant": {func(c *Candidate) { c.Roles = []string{authz.RoleTenant, authz.RoleLandlord} }, true},
		"already a manager":          {func(c *Candidate) { c.Roles = []string{authz.RoleTenant, authz.RoleManager} }, true},
		"the landlord themselves":    {func(c *Candidate) { c.ID = landlord }, false},
		"inactive":                   {func(c *Candidate) { c.Active = false }, false},
		"no tenant role":             {func(c *Candidate) { c.Roles = []string{authz.RoleLandlord} }, false},
		"admin":                      {func(c *Candidate) { c.Roles = []string{authz.RoleTenant, authz.RoleAdmin} }, false},
		"super admin":                {func(c *Candidate) { c.Roles = []string{authz.RoleTenant, authz.RoleSuperAdmin} }, false},
	}
	for name, tc := range cases {
		c := base()
		tc.mutate(&c)
		err := CanBeAsked(landlord, c)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrCannotManage)) {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestPolicy(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	grant := func(c ...Capability) *Assignment {
		return &Assignment{ManagerID: me, Status: AssignmentActive, Capabilities: c}
	}
	revoked := &Assignment{ManagerID: me, Status: AssignmentRevoked, Capabilities: caps(PaymentsReview, ContractsCreate, ContractsPropose, ReviewRegistrations)}
	tenant := func(id uuid.UUID) *uuid.UUID { return &id }

	t.Run("no hand-over: nothing is allowed", func(t *testing.T) {
		var none *Assignment
		if CanSeeContract(none, tenant(other), me) || CanWorkOnInvoice(none, nil, me) || CanSetInvoiceStatus(none, nil, me) ||
			CanReviewPayment(none, nil, nil, me) || CanReviewRegistration(none, nil, me) || CanProposeOffer(none) {
			t.Error("a caller with no hand-over was allowed something")
		}
		if !OfferNeedsApproval(none) {
			t.Error("without a hand-over an offer needs approval")
		}
	})
	t.Run("a revoked hand-over allows nothing", func(t *testing.T) {
		if CanSeeContract(revoked, tenant(other), me) || CanSetInvoiceStatus(revoked, tenant(other), me) || CanProposeOffer(revoked) {
			t.Error("revoked hand-over still allows")
		}
	})
	t.Run("basics", func(t *testing.T) {
		g := grant()
		if !CanSeeContract(g, tenant(other), me) || !CanSeeContract(g, nil, me) || !CanWorkOnInvoice(g, tenant(other), me) {
			t.Error("the basics are missing")
		}
		if CanSetInvoiceStatus(g, tenant(other), me) || CanReviewPayment(g, tenant(other), tenant(other), me) ||
			CanReviewRegistration(g, tenant(other), me) || CanProposeOffer(g) {
			t.Error("a capability was allowed without being given")
		}
	})
	t.Run("conflict of interest has no exception", func(t *testing.T) {
		g := grant(PaymentsReview, ReviewRegistrations, ContractsPropose, ContractsCreate)
		own := tenant(me)
		if CanSeeContract(g, own, me) || CanWorkOnInvoice(g, own, me) || CanSetInvoiceStatus(g, own, me) ||
			CanReviewPayment(g, own, tenant(other), me) || CanReviewRegistration(g, own, me) || CanViewPayment(g, own, tenant(other), me) {
			t.Error("a manager reached their own tenancy")
		}
		if CanDeleteInvoice(g, own, me, true, tenant(me)) {
			t.Error("a manager deleted a draft of their own tenancy")
		}
	})
	t.Run("payments", func(t *testing.T) {
		g := grant(PaymentsReview)
		if !CanSetInvoiceStatus(g, tenant(other), me) || !CanReviewPayment(g, tenant(other), tenant(other), me) {
			t.Error("payments.review does not allow")
		}
		if CanReviewPayment(g, tenant(other), tenant(me), me) {
			t.Error("a manager reviewed their own payment")
		}
		if CanViewPayment(g, tenant(other), tenant(me), me) {
			t.Error("a manager saw their own payment as a manager")
		}
	})
	t.Run("registrations", func(t *testing.T) {
		if !CanReviewRegistration(grant(ReviewRegistrations), tenant(other), me) {
			t.Error("contracts.review_registrations does not allow")
		}
	})
	t.Run("delete only own drafts", func(t *testing.T) {
		g := grant()
		cases := map[string]struct {
			draft   bool
			creator *uuid.UUID
			want    bool
		}{
			"own draft":        {true, tenant(me), true},
			"draft of another": {true, tenant(other), false},
			"unknown author":   {true, nil, false},
			"own but sent":     {false, tenant(me), false},
		}
		for name, tc := range cases {
			if got := CanDeleteInvoice(g, tenant(other), me, tc.draft, tc.creator); got != tc.want {
				t.Errorf("%s: got %v", name, got)
			}
		}
	})
	t.Run("offers", func(t *testing.T) {
		if !CanProposeOffer(grant(ContractsPropose)) || !OfferNeedsApproval(grant(ContractsPropose)) {
			t.Error("propose alone must wait for approval")
		}
		if OfferNeedsApproval(grant(ContractsPropose, ContractsCreate)) {
			t.Error("create sends without approval")
		}
	})
}

func TestAssignmentUpdate(t *testing.T) {
	a := &Assignment{Status: AssignmentActive, NotifyLandlord: true, Capabilities: caps(ContractsCreate, ContractsPropose)}
	no := false
	before, after := a.Update(nil, caps(ContractsPropose), &no)
	if !before.Changed(after) || after.NotifyLandlord || len(after.Capabilities) != 0 {
		t.Errorf("update: %+v -> %+v", before, after)
	}
	before, after = a.Update(nil, nil, nil)
	if before.Changed(after) {
		t.Error("nothing changed but Changed says so")
	}
}

func TestInvitationLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	sealed := "box"
	inv := Invitation{Status: InvitationPending, ExpiresAt: now.Add(time.Hour), Sealed: &sealed}
	if !inv.OpenAt(now) || !inv.LinkAvailable(now) {
		t.Error("a fresh invitation is open with a link")
	}
	if inv.OpenAt(now.Add(2*time.Hour)) || inv.LinkAvailable(now.Add(2*time.Hour)) {
		t.Error("an expired invitation is not open")
	}
	if err := inv.Renew("h", nil, now.Add(24*time.Hour)); err != nil || inv.Sealed != nil || inv.TokenHash != "h" {
		t.Errorf("renew: %v %+v", err, inv)
	}
	if err := inv.Revoke(now); err != nil || inv.Status != InvitationRevoked || inv.AnsweredAt == nil {
		t.Errorf("revoke: %v", err)
	}
	if !errors.Is(inv.Revoke(now), ErrInvitationNotPending) || !errors.Is(inv.Renew("x", nil, now), ErrInvitationNotPending) {
		t.Error("a withdrawn invitation cannot be revoked or renewed again")
	}
	user := uuid.New()
	acc := Invitation{Status: InvitationPending, Sealed: &sealed}
	acc.Answer(InvitationAccepted, user, now)
	if acc.Sealed != nil || acc.AnsweredByID == nil || *acc.AnsweredByID != user {
		t.Error("answer wipes the secret and names the answerer")
	}
}

func TestValidLinkTokenAndNames(t *testing.T) {
	good := make([]byte, LinkTokenLength)
	for i := range good {
		good[i] = 'a'
	}
	if !ValidLinkToken(string(good)) || ValidLinkToken("short") || ValidLinkToken(string(good[:63])+"!") {
		t.Error("ValidLinkToken")
	}
	if got := NamesLabel([]string{"A1", "A2"}); got != "A1, A2" {
		t.Errorf("names: %q", got)
	}
	if got := NamesLabel([]string{"1", "2", "3", "4", "5", "6"}); got != "1, 2, 3, 4, 5 …" {
		t.Errorf("names: %q", got)
	}
}
