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

// know makes the invitee port know an account, as the person answering an invitation.
func (w *invWorld) know(id uuid.UUID, name string, email *string, verified bool) {
	if w.invitees.people == nil {
		w.invitees.people = map[uuid.UUID]invitation.Person{}
	}
	w.invitees.people[id] = invitation.Person{
		ID: id, FullName: name, Email: email, EmailVerified: verified, Active: true, AcceptsInvitations: true, Roles: []string{"tenant"},
	}
}

// issue makes an offer to anybody and returns its id and the token of its link.
func (w *invWorld) issue(in CreateInvitationInput) (uuid.UUID, string) {
	w.t.Helper()
	got := w.mustCreate(in)
	w.notifier.sent, w.audit.events = nil, nil
	return got.Invitation.ID, tokenOf(got.Link.URL)
}

func byToken(t string) InvitationRef  { return InvitationRef{Token: &t} }
func byID(id uuid.UUID) InvitationRef { return InvitationRef{InvitationID: &id} }

func (w *invWorld) accept(who uuid.UUID, ref InvitationRef) (domain.Detail, error) {
	return w.answers.Accept(context.Background(), who, ref)
}

func (w *invWorld) status(id uuid.UUID) domain.InvitationStatus { return w.repo.rows[id].Status }

func TestEveryUnusableInvitationReadsTheSame(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(w *invWorld, id uuid.UUID, token string) InvitationRef{
		"an unknown link": func(*invWorld, uuid.UUID, string) InvitationRef {
			return byToken(strings.Repeat("a", domain.LinkTokenLength))
		},
		"a malformed link": func(*invWorld, uuid.UUID, string) InvitationRef { return byToken("short") },
		"no reference":     func(*invWorld, uuid.UUID, string) InvitationRef { return InvitationRef{} },
		"an expired link": func(w *invWorld, _ uuid.UUID, token string) InvitationRef {
			w.clock = testNow.Add(8 * 24 * time.Hour)
			return byToken(token)
		},
		"a withdrawn link": func(w *invWorld, id uuid.UUID, token string) InvitationRef {
			_ = w.uc.Revoke(ctx, w.landlord, id)
			return byToken(token)
		},
		"an answered link": func(w *invWorld, id uuid.UUID, token string) InvitationRef {
			w.repo.rows[id].Answer(domain.InvitationDeclined, w.other, testNow, nil)
			return byToken(token)
		},
		"a link awaiting approval": func(w *invWorld, id uuid.UUID, token string) InvitationRef {
			w.repo.rows[id].Status = domain.InvitationAwaitingApproval
			return byToken(token)
		},
		"an id not addressed to the caller": func(w *invWorld, id uuid.UUID, _ string) InvitationRef { return byID(id) },
		"a deleted room": func(w *invWorld, _ uuid.UUID, token string) InvitationRef {
			w.room.Live = false
			return byToken(token)
		},
	}
	for name, prepare := range cases {
		w := newInvWorld(t)
		w.know(w.tenant, "Tenant", ptr("t@example.vn"), true)
		id, token := w.issue(CreateInvitationInput{})
		ref := prepare(w, id, token)
		// A deleted room has its offers withdrawn by the deletion itself (see RoomLinks.Deleted), so only the answers that lock
		// the room meet it here.
		if _, err := w.answers.Preview(ctx, w.tenant, ref); !errors.Is(err, domain.ErrInvitationInvalid) && name != "a deleted room" {
			t.Errorf("%s: preview error = %v", name, err)
		}
		if _, err := w.accept(w.tenant, ref); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("%s: accept error = %v", name, err)
		}
		if err := w.answers.Decline(ctx, w.tenant, ref); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("%s: decline error = %v", name, err)
		}
		if len(w.store.contracts) != 0 || len(w.notifier.sent) != 0 {
			t.Errorf("%s: nothing is made or sent", name)
		}
	}
}

func TestAudienceDecidesWhoMayAnswer(t *testing.T) {
	addr := "New@Example.vn"
	cases := map[string]struct {
		in      func(w *invWorld) CreateInvitationInput
		who     func(w *invWorld) (uuid.UUID, ref2)
		allowed bool
	}{
		"the named account by token":    {accountOffer, tenantByToken, true},
		"the named account by id":       {accountOffer, tenantByID, true},
		"another account by token":      {accountOffer, otherByToken, false},
		"another account by id":         {accountOffer, otherByID, false},
		"an address, verified holder":   {addressOffer(addr), verifiedHolder("new@example.vn"), true},
		"an address, unverified":        {addressOffer(addr), unverifiedHolder("new@example.vn"), false},
		"an address, another address":   {addressOffer(addr), verifiedHolder("else@example.vn"), false},
		"an address, no email (Zalo)":   {addressOffer(addr), noEmailHolder, false},
		"anybody, no email, unverified": {func(*invWorld) CreateInvitationInput { return CreateInvitationInput{} }, noEmailHolder, true},
	}
	for name, tc := range cases {
		w := newInvWorld(t)
		in := tc.in(w)
		who, ref := tc.who(w)
		got := w.mustCreate(in)
		r := ref(got)
		_, err := w.answers.Preview(context.Background(), who, r)
		if (err == nil) != tc.allowed || (err != nil && !errors.Is(err, domain.ErrInvitationInvalid)) {
			t.Errorf("%s: preview error = %v, allowed %v", name, err, tc.allowed)
		}
	}
}

// ref2 turns the created invitation into the reference the caller uses.
type ref2 func(IssuedInvitation) InvitationRef

func accountOffer(w *invWorld) CreateInvitationInput {
	w.invitees.result = invitation.Addressee{Recipient: w.account(ptr("t@example.vn"))}
	return CreateInvitationInput{}
}

func addressOffer(addr string) func(w *invWorld) CreateInvitationInput {
	return func(w *invWorld) CreateInvitationInput {
		n := invitation.NormalizeEmail(addr)
		w.invitees.result = invitation.Addressee{Email: &n}
		return CreateInvitationInput{}
	}
}

func tokenRef(i IssuedInvitation) InvitationRef { return byToken(tokenOf(i.Link.URL)) }
func idRef(i IssuedInvitation) InvitationRef    { return byID(i.Invitation.ID) }

func tenantByToken(w *invWorld) (uuid.UUID, ref2) {
	w.know(w.tenant, "Tenant", ptr("t@example.vn"), true)
	return w.tenant, tokenRef
}

func tenantByID(w *invWorld) (uuid.UUID, ref2) {
	w.know(w.tenant, "Tenant", ptr("t@example.vn"), true)
	return w.tenant, idRef
}

func otherByToken(w *invWorld) (uuid.UUID, ref2) {
	w.know(w.other, "Other", ptr("o@example.vn"), true)
	return w.other, tokenRef
}

func otherByID(w *invWorld) (uuid.UUID, ref2) {
	w.know(w.other, "Other", ptr("o@example.vn"), true)
	return w.other, idRef
}

func verifiedHolder(email string) func(w *invWorld) (uuid.UUID, ref2) {
	return func(w *invWorld) (uuid.UUID, ref2) {
		w.know(w.other, "Other", &email, true)
		return w.other, tokenRef
	}
}

func unverifiedHolder(email string) func(w *invWorld) (uuid.UUID, ref2) {
	return func(w *invWorld) (uuid.UUID, ref2) {
		w.know(w.other, "Other", &email, false)
		return w.other, tokenRef
	}
}

func noEmailHolder(w *invWorld) (uuid.UUID, ref2) {
	w.know(w.other, "Other", nil, false)
	return w.other, tokenRef
}

func TestAcceptMakesTheRunningContractForTheCaller(t *testing.T) {
	w := newInvWorld(t)
	w.know(w.tenant, "Tenant", nil, false)
	rival := w.store.put(w.room, &w.other, domain.StatusPending)
	blank := w.store.put(w.room, nil, domain.StatusActive)
	id, token := w.issue(CreateInvitationInput{DepositVND: ptr(int64(1_000_000)), OccupantCount: 2, Note: ptr("Tầng 2")})

	d, err := w.accept(w.tenant, byToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.StatusActive || d.TenantID == nil || *d.TenantID != w.tenant || d.PriceVND != 3_000_000 ||
		*d.DepositVND != 1_000_000 || d.OccupantCount != 2 || *d.Notes != "Tầng 2" || d.ConfirmedAt == nil {
		t.Errorf("contract = %+v", d)
	}
	inv := w.repo.rows[id]
	if inv.Status != domain.InvitationAccepted || inv.ContractID == nil || *inv.ContractID != d.ID || inv.Sealed != nil ||
		inv.AnsweredByID == nil || *inv.AnsweredByID != w.tenant {
		t.Errorf("invitation = %+v", inv)
	}
	if w.room.Status != domain.RoomRented {
		t.Errorf("room status = %s", w.room.Status)
	}
	if w.store.contracts[rival.ID].Status != domain.StatusRejected || w.store.contracts[blank.ID].Status != domain.StatusEnded {
		t.Error("the rival registration is rejected and the blank contract gives way")
	}
	// Only the landlord hears of it.
	if got := w.notifier.kinds(); !slices.Equal(got, []string{"contract_invitation_accepted:landlord"}) || w.notifier.sent[0].UserID != w.landlord {
		t.Errorf("notifications = %v", got)
	}
	if got := w.audit.actions(); !slices.Equal(got, []string{AuditTerminated, AuditCreated, AuditInvitationAccepted}) {
		t.Errorf("audit = %v", got)
	}
	// The link cannot be used twice.
	if _, err := w.accept(w.tenant, byToken(token)); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("second accept: %v", err)
	}
}

func TestAcceptOfAPendingOfferMakesAWaitingRegistration(t *testing.T) {
	w := newInvWorld(t)
	w.know(w.tenant, "Tenant", nil, false)
	_, token := w.issue(CreateInvitationInput{ContractStatus: domain.StatusPending})
	d, err := w.accept(w.tenant, byToken(token))
	if err != nil || d.Status != domain.StatusPending {
		t.Fatalf("accept = %+v, %v", d, err)
	}
	if w.room.Status != domain.RoomAvailable {
		t.Error("a waiting registration does not rent the room")
	}
	// The same tenant has one open contract on the room: a second offer cannot make another.
	id2, token2 := w.issue(CreateInvitationInput{ContractStatus: domain.StatusPending})
	if _, err := w.accept(w.tenant, byToken(token2)); !errors.Is(err, domain.ErrAlreadyRegistered) {
		t.Errorf("second accept: %v", err)
	}
	if w.status(id2) != domain.InvitationPending {
		t.Error("the refused invitation stays open")
	}
}

func TestARefusedAcceptLeavesTheInvitationOpen(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		prepare func(w *invWorld)
		check   func(t *testing.T, err error)
	}{
		"the room is taken meanwhile": {
			func(w *invWorld) { w.store.put(w.room, &w.other, domain.StatusActive) },
			func(t *testing.T, err error) { is(t, err, domain.ErrRoomOccupied) },
		},
		"the plan limit": {
			func(w *invWorld) {
				w.deps.Plans = fixedPlans{contracts: ptr(1)}
				w.answers = NewInvitationAnswers(w.deps, Options{})
				w.store.put(w.store.room(w.landlord, "P102", domain.RoomRented), &w.other, domain.StatusActive)
			},
			func(t *testing.T, err error) {
				var limit *domain.ContractLimitError
				if !errors.As(err, &limit) {
					t.Errorf("error = %v", err)
				}
			},
		},
		"the room's minimum period grew": {
			func(w *invWorld) { w.room.MinPeriod = 3 },
			func(t *testing.T, err error) {
				var below *domain.BelowMinimumPeriodError
				if !errors.As(err, &below) {
					t.Errorf("error = %v", err)
				}
			},
		},
		"the offer is the caller's own": {
			func(w *invWorld) {
				w.tenant = w.landlord
				w.know(w.tenant, "Landlord", nil, false)
			},
			func(t *testing.T, err error) { is(t, err, domain.ErrInvitationOwnOffer) },
		},
	}
	for name, tc := range cases {
		w := newInvWorld(t)
		ends := w.today().AddDate(0, 1, 0)
		id, token := w.issue(CreateInvitationInput{EndsOn: &ends})
		w.know(w.tenant, "Tenant", nil, false)
		tc.prepare(w)
		before := len(w.store.contracts)
		_, err := w.accept(w.tenant, byToken(token))
		tc.check(t, err)
		if w.status(id) != domain.InvitationPending || w.repo.rows[id].Sealed == nil {
			t.Errorf("%s: the invitation must stay open and keep its link", name)
		}
		// The contract store only changes through the use case's own writes, which come after every refusal here.
		if name != "the room is taken meanwhile" && name != "the plan limit" && len(w.store.contracts) != before {
			t.Errorf("%s: a contract was made", name)
		}
		if len(w.notifier.sent) != 0 {
			t.Errorf("%s: nobody is told", name)
		}
		if _, err := w.answers.Preview(ctx, w.tenant, byToken(token)); name != "the offer is the caller's own" && err != nil {
			t.Errorf("%s: still previewable: %v", name, err)
		}
	}
}

func TestDeclineTellsTheLandlordAndClosesTheOffer(t *testing.T) {
	w := newInvWorld(t)
	w.know(w.tenant, "Tenant", nil, false)
	id, token := w.issue(CreateInvitationInput{})
	if err := w.answers.Decline(context.Background(), w.tenant, byToken(token)); err != nil {
		t.Fatal(err)
	}
	inv := w.repo.rows[id]
	if inv.Status != domain.InvitationDeclined || inv.Sealed != nil || inv.ContractID != nil {
		t.Errorf("invitation = %+v", inv)
	}
	if got := w.notifier.kinds(); !slices.Equal(got, []string{"contract_invitation_declined:landlord"}) ||
		w.notifier.sent[0].Params["tenant"] != "Tenant" || w.notifier.sent[0].Params["room"] != "P101" {
		t.Errorf("notifications = %+v", w.notifier.sent)
	}
	if !slices.Equal(w.audit.actions(), []string{AuditInvitationDeclined}) {
		t.Errorf("audit = %v", w.audit.actions())
	}
	if _, err := w.accept(w.tenant, byToken(token)); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("accept after decline: %v", err)
	}
}

func TestPreviewShowsTheOfferAndChangesNothing(t *testing.T) {
	w := newInvWorld(t)
	w.know(w.tenant, "Tenant", nil, false)
	id, token := w.issue(CreateInvitationInput{})
	o, err := w.answers.Preview(context.Background(), w.tenant, byToken(token))
	if err != nil || o.ID != id || o.Room.Name != "P101" || o.Landlord.FullName != "Landlord" || o.Landlord.ID != w.landlord {
		t.Errorf("offer = %+v, %v", o, err)
	}
	if w.status(id) != domain.InvitationPending || len(w.audit.events) != 0 || len(w.notifier.sent) != 0 {
		t.Error("a preview changes nothing")
	}
}

func TestInboxListsOnlyOpenOffersAddressedToTheCaller(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	w.know(w.tenant, "Tenant", ptr("t@example.vn"), true)
	w.invitees.result = invitation.Addressee{Recipient: w.account(ptr("t@example.vn"))}
	open := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	answered := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	late := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	w.invitees.result = invitation.Addressee{} // public: not in anybody's inbox
	w.mustCreate(CreateInvitationInput{})
	w.invitees.result = invitation.Addressee{Recipient: &invitation.Person{ID: w.other}}
	w.mustCreate(CreateInvitationInput{})

	w.repo.rows[answered].Answer(domain.InvitationDeclined, w.tenant, testNow, nil)
	w.repo.rows[late].ExpiresAt = testNow.Add(-time.Second)

	items, total, err := w.answers.Inbox(ctx, w.tenant, 10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != open {
		t.Errorf("inbox = %d of %d, %v", len(items), total, err)
	}
}

func TestDeletingRoomsRevokesTheirOpenOffersOnly(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	open := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	done := w.mustCreate(CreateInvitationInput{}).Invitation.ID
	w.repo.rows[done].Answer(domain.InvitationDeclined, w.tenant, testNow, nil)
	blank := w.store.put(w.room, nil, domain.StatusActive)
	elsewhere := w.store.room(w.landlord, "P102", domain.RoomAvailable)
	kept := w.mustCreate(CreateInvitationInput{RoomID: elsewhere.ID}).Invitation.ID
	w.audit.events = nil

	if err := w.links.Deleted(ctx, []uuid.UUID{w.room.ID}); err != nil {
		t.Fatal(err)
	}
	if w.status(open) != domain.InvitationRevoked || w.repo.rows[open].Sealed != nil || w.status(done) != domain.InvitationDeclined ||
		w.status(kept) != domain.InvitationPending {
		t.Errorf("statuses: open %s, done %s, kept %s", w.status(open), w.status(done), w.status(kept))
	}
	if w.store.contracts[blank.ID].Status != domain.StatusEnded {
		t.Error("the blank contract ends with the room")
	}
	var revokes int
	for _, e := range w.audit.events {
		if e.Action == AuditInvitationRevoked {
			revokes++
			if e.InvitationID != open || e.Meta["reason"] != "room_deleted" || e.ActorID != uuid.Nil {
				t.Errorf("event = %+v", e)
			}
		}
	}
	if revokes != 1 {
		t.Errorf("%d revoked events", revokes)
	}
	if err := w.links.Deleted(ctx, nil); err != nil {
		t.Errorf("no rooms: %v", err)
	}
}

func TestSeedDemoInvitationOffersP105OnceToTheDemoTenant(t *testing.T) {
	ctx := context.Background()
	w := newInvWorld(t)
	w.invitees.result = invitation.Addressee{Recipient: w.account(ptr("t@example.vn"))}
	seeder := NewSeeder(w.deps, Options{}, w.store)
	if n, err := seeder.SeedDemoInvitation(ctx, w.landlord, w.tenant); err != nil || n != 0 {
		t.Errorf("without the room: %d, %v", n, err)
	}
	w.store.room(w.landlord, "P105", domain.RoomAvailable)
	if n, err := seeder.SeedDemoInvitation(ctx, w.landlord, w.tenant); err != nil || n != 1 {
		t.Fatalf("seed: %d, %v", n, err)
	}
	if n, err := seeder.SeedDemoInvitation(ctx, w.landlord, w.tenant); err != nil || n != 0 {
		t.Errorf("again: %d, %v", n, err)
	}
	if len(w.repo.rows) != 1 || len(w.mailer.sent) != 1 {
		t.Errorf("one invitation: %d rows", len(w.repo.rows))
	}
	for _, inv := range w.repo.rows {
		if inv.RecipientID == nil || *inv.RecipientID != w.tenant || inv.Terms.StartsOn.Sub(w.today()) != 14*24*time.Hour {
			t.Errorf("invitation = %+v", inv)
		}
	}
}
