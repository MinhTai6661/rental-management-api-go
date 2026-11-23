package contract

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
)

// invWorld is the world of a landlord's invitations: the contract world plus the invitation ports.
type invWorld struct {
	*world
	repo     *fakeInvitations
	invitees *fakeInvitees
	mailer   *recordingMailer
	uc       *Invitations
	answers  *InvitationAnswers
}

func newInvWorld(t *testing.T) *invWorld {
	t.Helper()
	w := newWorld(t, nil)
	iw := &invWorld{world: w, repo: newFakeInvitations(w.store), invitees: &fakeInvitees{}, mailer: &recordingMailer{}}
	w.deps.Invitations, w.deps.Invitees, w.deps.Tokens, w.deps.Sealer, w.deps.Mailer = iw.repo, iw.invitees, &fakeTokens{}, fakeSealer{}, iw.mailer
	iw.uc = NewInvitations(w.deps, Options{WebURL: "http://web.test/"})
	iw.answers = NewInvitationAnswers(w.deps, Options{WebURL: "http://web.test/"})
	iw.links = NewRoomLinks(w.deps, Options{})
	return iw
}

func (w *invWorld) create(in CreateInvitationInput) (IssuedInvitation, error) {
	if in.RoomID == uuid.Nil {
		in.RoomID = w.room.ID
	}
	if in.StartsOn.IsZero() {
		in.StartsOn = w.today()
	}
	return w.uc.Create(context.Background(), w.landlord, in)
}

func (w *invWorld) mustCreate(in CreateInvitationInput) IssuedInvitation {
	w.t.Helper()
	got, err := w.create(in)
	if err != nil {
		w.t.Fatalf("create: %v", err)
	}
	return got
}

// addressTo makes Resolve answer with the account that has this email.
func (w *invWorld) account(email *string) *invitation.Person {
	return &invitation.Person{ID: w.tenant, FullName: "Tenant", Email: email, Active: true, AcceptsInvitations: true, Roles: []string{"tenant"}}
}

func ptr[T any](v T) *T { return &v }

func tokenOf(link string) string { return link[strings.Index(link, "token=")+len("token="):] }

func TestCreateOffersTheRoomOnItsOwnPriceForTheTTL(t *testing.T) {
	w := newInvWorld(t)
	got := w.mustCreate(CreateInvitationInput{})
	inv := got.Invitation
	if inv.Status != domain.InvitationPending || inv.Terms.PriceVND != 3_000_000 || inv.Terms.OccupantCount != 1 ||
		inv.Terms.ContractStatus != domain.StatusActive || inv.Audience() != invitation.AudiencePublic || inv.RoomName != "P101" {
		t.Errorf("invitation = %+v", inv)
	}
	if want := testNow.Add(7 * 24 * time.Hour); !inv.ExpiresAt.Equal(want) || !got.Link.ExpiresAt.Equal(want) {
		t.Errorf("expires %v, link %v, want %v", inv.ExpiresAt, got.Link.ExpiresAt, want)
	}
	token := tokenOf(got.Link.URL)
	if !strings.HasPrefix(got.Link.URL, "http://web.test/contract-invitation?token=") || !domain.ValidLinkToken(token) {
		t.Errorf("link = %q", got.Link.URL)
	}
	// Only the hash and the encrypted copy are stored, never the token itself.
	stored := w.repo.rows[inv.ID]
	if stored.TokenHash != "hash:"+token || stored.Sealed == nil || *stored.Sealed == token {
		t.Errorf("stored = %+v", stored)
	}
	if got.Delivery != (Delivery{}) || len(w.notifier.sent) != 0 || len(w.mailer.sent) != 0 {
		t.Error("an offer to anybody is sent nowhere")
	}
	if !slices.Equal(w.audit.actions(), []string{AuditInvitationCreated}) || w.audit.events[0].Meta["audience"] != "public" {
		t.Errorf("audit = %+v", w.audit.events)
	}
}

func TestCreateTakesTheTermsGiven(t *testing.T) {
	w := newInvWorld(t)
	ends := w.today().AddDate(0, 6, 0)
	got := w.mustCreate(CreateInvitationInput{
		PriceVND: ptr(int64(2_400_000)), DepositVND: ptr(int64(1_000_000)), OccupantCount: 2, EndsOn: &ends,
		ContractStatus: domain.StatusPending, Note: ptr("Tầng 2"),
	})
	terms := got.Invitation.Terms
	if terms.PriceVND != 2_400_000 || *terms.DepositVND != 1_000_000 || terms.OccupantCount != 2 || terms.ContractStatus != domain.StatusPending ||
		*terms.Note != "Tầng 2" || !terms.EndsOn.Equal(ends) {
		t.Errorf("terms = %+v", terms)
	}
}

func TestCreateRules(t *testing.T) {
	past := domain.Date(2026, 1, 1)
	cases := map[string]struct {
		prepare func(w *invWorld)
		in      CreateInvitationInput
		want    error
	}{
		"a room of another landlord": {func(w *invWorld) { w.room.LandlordID = w.other }, CreateInvitationInput{}, domain.ErrRoomNotFound},
		"a deleted room":             {func(w *invWorld) { w.room.Live = false }, CreateInvitationInput{}, domain.ErrRoomNotFound},
		"no such room":               {func(*invWorld) {}, CreateInvitationInput{RoomID: uuid.New()}, domain.ErrRoomNotFound},
		"an active offer on a room a tenant holds": {func(w *invWorld) {
			w.store.put(w.room, &w.tenant, domain.StatusActive)
		}, CreateInvitationInput{}, domain.ErrRoomOccupied},
		"zero occupants are one": {func(*invWorld) {}, CreateInvitationInput{OccupantCount: 0}, nil},
		"fifty-one occupants":    {func(*invWorld) {}, CreateInvitationInput{OccupantCount: 51}, domain.ErrOccupantsInvalid},
		"a negative occupant":    {func(*invWorld) {}, CreateInvitationInput{OccupantCount: -1}, domain.ErrOccupantsInvalid},
		"a start in the past":    {func(*invWorld) {}, CreateInvitationInput{StartsOn: past}, nil},
		"a room with no listing": {func(w *invWorld) { w.room.Listed = false }, CreateInvitationInput{}, nil},
		"a room under repair":    {func(w *invWorld) { w.room.Status = "maintenance" }, CreateInvitationInput{}, nil},
		"an ended status":        {func(*invWorld) {}, CreateInvitationInput{ContractStatus: domain.StatusEnded}, domain.ErrInvalidTransition},
		"a blank contract only":  {func(w *invWorld) { w.store.put(w.room, nil, domain.StatusActive) }, CreateInvitationInput{}, nil},
		"a pending offer on a held room": {func(w *invWorld) {
			w.store.put(w.room, &w.tenant, domain.StatusActive)
		}, CreateInvitationInput{ContractStatus: domain.StatusPending}, nil},
	}
	for name, tc := range cases {
		w := newInvWorld(t)
		tc.prepare(w)
		_, err := w.create(tc.in)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
		if tc.want != nil && len(w.repo.rows) != 0 {
			t.Errorf("%s: something was stored", name)
		}
	}
}

func TestCreateRefusesDatesAndAShortStay(t *testing.T) {
	w := newInvWorld(t)
	start := w.today()
	if _, err := w.create(CreateInvitationInput{StartsOn: start, EndsOn: &start}); !errors.Is(err, domain.ErrEndNotAfterStart) {
		t.Errorf("end on the start day: %v", err)
	}
	w.room.MinPeriod = 2
	soon := start.AddDate(0, 1, 0)
	var below *domain.BelowMinimumPeriodError
	if _, err := w.create(CreateInvitationInput{StartsOn: start, EndsOn: &soon}); !errors.As(err, &below) {
		t.Errorf("below the minimum: %v", err)
	}
	if len(w.repo.rows) != 0 {
		t.Error("nothing is stored for a refused offer")
	}
}

func TestCreateWithoutASealerKeepsNoLink(t *testing.T) {
	w := newInvWorld(t)
	w.deps.Sealer = nil
	w.uc = NewInvitations(w.deps, Options{WebURL: "http://web.test"})
	got := w.mustCreate(CreateInvitationInput{})
	if got.Invitation.Sealed != nil || got.Invitation.LinkAvailable(testNow) || got.Link.URL == "" {
		t.Errorf("the link is shown once and not kept: %+v", got.Invitation)
	}
	if _, err := w.uc.Link(context.Background(), w.landlord, got.Invitation.ID); !errors.Is(err, domain.ErrInvitationLinkUnavailable) {
		t.Errorf("link: %v", err)
	}
}

func TestCreatePassesTheRecipientErrorsOn(t *testing.T) {
	for _, want := range []error{invitation.ErrRecipientInvalid, invitation.ErrOwnEmail, invitation.ErrRecipientMismatch} {
		w := newInvWorld(t)
		w.invitees.err = want
		if _, err := w.create(CreateInvitationInput{Email: ptr("x@y.vn")}); !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
		if len(w.repo.rows) != 0 || len(w.audit.events) != 0 {
			t.Error("nothing is stored or recorded")
		}
	}
}

func TestDeliveryByAudience(t *testing.T) {
	email := "tenant@example.vn"
	cases := map[string]struct {
		prepare   func(w *invWorld)
		wantDel   Delivery
		notified  int
		mailTo    string
		wantToken bool // the mail link carries the secret
		audience  invitation.Audience
	}{
		"an account with an email": {
			func(w *invWorld) { w.invitees.result = invitation.Addressee{Recipient: w.account(&email)} },
			Delivery{App: true, Email: true}, 1, email, false, invitation.AudienceAccount,
		},
		"an account without an email (Zalo)": {
			func(w *invWorld) { w.invitees.result = invitation.Addressee{Recipient: w.account(nil)} },
			Delivery{App: true}, 1, "", false, invitation.AudienceAccount,
		},
		"an address nobody holds": {
			func(w *invWorld) { w.invitees.result = invitation.Addressee{Email: &email} },
			Delivery{Email: true}, 0, email, true, invitation.AudienceEmail,
		},
		"an address that asked for no more": {
			func(w *invWorld) {
				w.invitees.result, w.invitees.noMail = invitation.Addressee{Email: &email}, true
			},
			Delivery{}, 0, "", false, invitation.AudienceEmail,
		},
		"an account whose address asked for no more": {
			func(w *invWorld) {
				w.invitees.result, w.invitees.noMail = invitation.Addressee{Recipient: w.account(&email)}, true
			},
			Delivery{App: true}, 1, "", false, invitation.AudienceAccount,
		},
		"anybody": {func(*invWorld) {}, Delivery{}, 0, "", false, invitation.AudiencePublic},
	}
	for name, tc := range cases {
		w := newInvWorld(t)
		tc.prepare(w)
		got := w.mustCreate(CreateInvitationInput{})
		if got.Delivery != tc.wantDel || got.Invitation.Audience() != tc.audience {
			t.Errorf("%s: delivery %+v audience %s", name, got.Delivery, got.Invitation.Audience())
		}
		if len(w.notifier.sent) != tc.notified {
			t.Errorf("%s: %d notifications", name, len(w.notifier.sent))
		}
		for _, n := range w.notifier.sent {
			id := got.Invitation.ID.String()
			if n.Kind != NotifyInvitationReceived || n.Audience != AudienceTenant || n.UserID != w.tenant ||
				n.URL != "/contract-invitation?invitation="+id || strings.Contains(n.URL, "token=") || n.Params["room"] != "P101" || n.Params["landlord"] != "Landlord" {
				t.Errorf("%s: notification = %+v", name, n)
			}
		}
		switch {
		case tc.mailTo == "" && len(w.mailer.sent) != 0:
			t.Errorf("%s: an email went out", name)
		case tc.mailTo != "":
			if len(w.mailer.sent) != 1 || w.mailer.sent[0].To != tc.mailTo {
				t.Fatalf("%s: mails = %+v", name, w.mailer.sent)
			}
			m := w.mailer.sent[0]
			if strings.Contains(m.URL, "token=") != tc.wantToken || m.Room != "P101" || m.Landlord != "Landlord" || !m.ExpiresAt.Equal(got.Invitation.ExpiresAt) {
				t.Errorf("%s: mail = %+v", name, m)
			}
			if !tc.wantToken && !strings.Contains(m.URL, "invitation="+got.Invitation.ID.String()) {
				t.Errorf("%s: the mail link names the invitation: %q", name, m.URL)
			}
		}
		if tc.audience == invitation.AudienceAccount && w.repo.rows[got.Invitation.ID].RecipientID == nil {
			t.Errorf("%s: the account is stored as the recipient", name)
		}
	}
}

func TestAMailFailureNeverUndoesTheOffer(t *testing.T) {
	w := newInvWorld(t)
	email := "tenant@example.vn"
	w.invitees.result = invitation.Addressee{Recipient: w.account(&email)}
	w.mailer.err = errors.New("smtp down")
	got := w.mustCreate(CreateInvitationInput{})
	if got.Delivery != (Delivery{App: true}) || len(w.repo.rows) != 1 {
		t.Errorf("delivery = %+v, rows = %d", got.Delivery, len(w.repo.rows))
	}
}

func TestLinkIsTheSameUntilRegenerated(t *testing.T) {
	w := newInvWorld(t)
	made := w.mustCreate(CreateInvitationInput{})
	id := made.Invitation.ID
	got, err := w.uc.Link(context.Background(), w.landlord, id)
	if err != nil || got.URL != made.Link.URL || !got.ExpiresAt.Equal(made.Link.ExpiresAt) {
		t.Fatalf("link = %+v, %v; created with %+v", got, err, made.Link)
	}
	w.clock = testNow.Add(24 * time.Hour)
	fresh, err := w.uc.Regenerate(context.Background(), w.landlord, id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Link.URL == made.Link.URL || !fresh.Link.ExpiresAt.Equal(w.clock.Add(7*24*time.Hour)) {
		t.Errorf("a new link and a new end: %+v", fresh.Link)
	}
	if w.repo.rows[id].TokenHash != "hash:"+tokenOf(fresh.Link.URL) {
		t.Error("the old secret is replaced, not kept beside the new one")
	}
	again, err := w.uc.Link(context.Background(), w.landlord, id)
	if err != nil || again.URL != fresh.Link.URL {
		t.Errorf("the new link is the one shown now: %+v, %v", again, err)
	}
	if fresh.Delivery != (Delivery{}) || len(w.mailer.sent) != 0 {
		t.Error("a new link is not sent anywhere")
	}
	if !slices.Equal(w.audit.actions(), []string{AuditInvitationCreated, AuditInvitationRegenerated}) {
		t.Errorf("audit = %v", w.audit.actions())
	}
}

func TestLinkIsUnavailableWhenItCannotBeShown(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(w *invWorld, id uuid.UUID){
		"revoked": func(w *invWorld, id uuid.UUID) { _ = w.uc.Revoke(ctx, w.landlord, id) },
		"accepted": func(w *invWorld, id uuid.UUID) {
			w.repo.rows[id].Answer(domain.InvitationAccepted, w.tenant, testNow, ptr(uuid.New()))
		},
		"expired":            func(w *invWorld, _ uuid.UUID) { w.clock = testNow.Add(8 * 24 * time.Hour) },
		"made before saving": func(w *invWorld, id uuid.UUID) { w.repo.rows[id].Sealed = nil },
		"a secret that will not open": func(w *invWorld, id uuid.UUID) {
			w.repo.rows[id].Sealed = ptr("garbage")
		},
	}
	for name, change := range cases {
		w := newInvWorld(t)
		id := w.mustCreate(CreateInvitationInput{}).Invitation.ID
		change(w, id)
		if _, err := w.uc.Link(ctx, w.landlord, id); !errors.Is(err, domain.ErrInvitationLinkUnavailable) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestAnotherLandlordSeesNothing(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	id := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	if _, err := w.uc.Get(ctx, w.other, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if _, err := w.uc.Link(ctx, w.other, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("link: %v", err)
	}
	if _, err := w.uc.Regenerate(ctx, w.other, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("regenerate: %v", err)
	}
	if err := w.uc.Revoke(ctx, w.other, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke: %v", err)
	}
	if items, total, _ := w.uc.List(ctx, w.other, nil, 10, 0); len(items) != 0 || total != 0 {
		t.Errorf("list = %d, %d", len(items), total)
	}
	if w.repo.rows[id].Status != domain.InvitationPending {
		t.Error("the offer is untouched")
	}
}

func TestRegenerateOnlyForAWaitingOffer(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	id := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	w.clock = testNow.Add(10 * 24 * time.Hour) // expired, still pending: a new link is allowed
	if _, err := w.uc.Regenerate(ctx, w.landlord, id); err != nil {
		t.Errorf("an expired offer gets a new link: %v", err)
	}
	w.repo.rows[id].Answer(domain.InvitationAccepted, w.tenant, w.clock, ptr(uuid.New()))
	if _, err := w.uc.Regenerate(ctx, w.landlord, id); !errors.Is(err, domain.ErrInvitationNotPending) {
		t.Errorf("an accepted offer: %v", err)
	}
}

func TestRevokeWipesTheSecretAndWorksOnce(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	id := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	if err := w.uc.Revoke(ctx, w.landlord, id); err != nil {
		t.Fatal(err)
	}
	if got := w.repo.rows[id]; got.Status != domain.InvitationRevoked || got.Sealed != nil || got.AnsweredAt == nil {
		t.Errorf("revoked = %+v", got)
	}
	if err := w.uc.Revoke(ctx, w.landlord, id); !errors.Is(err, domain.ErrInvitationNotPending) {
		t.Errorf("twice: %v", err)
	}
	if !slices.Equal(w.audit.actions(), []string{AuditInvitationCreated, AuditInvitationRevoked}) {
		t.Errorf("audit = %v", w.audit.actions())
	}
}

func TestListFiltersByStatusAndPages(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	first := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	w.mustCreate(CreateInvitationInput{})
	if err := w.uc.Revoke(ctx, w.landlord, first); err != nil {
		t.Fatal(err)
	}
	pending, revoked := domain.InvitationPending, domain.InvitationRevoked
	if items, total, _ := w.uc.List(ctx, w.landlord, &pending, 10, 0); total != 1 || len(items) != 1 {
		t.Errorf("pending: %d of %d", len(items), total)
	}
	if items, total, _ := w.uc.List(ctx, w.landlord, &revoked, 10, 0); total != 1 || len(items) != 1 || items[0].ID != first {
		t.Errorf("revoked: %d of %d", len(items), total)
	}
	if items, total, _ := w.uc.List(ctx, w.landlord, nil, 1, 0); total != 2 || len(items) != 1 {
		t.Errorf("a page of one: %d of %d", len(items), total)
	}
}
