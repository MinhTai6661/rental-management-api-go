package manager

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
	"rental-management/pkg/authz"
)

var testNow = time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)

// world is a landlord with three rooms, a tenant who may be asked and a stranger.
type world struct {
	t           *testing.T
	s           *store
	clock       time.Time
	landlord    invitation.Person
	tenant      invitation.Person
	stranger    invitation.Person
	r1, r2, r3  *fakeRoom
	invitations *Invitations
	answers     *Answers
	assignments *Assignments
	managed     *Managed
	access      *Access
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s := newStore(testNow)
	w := &world{t: t, s: s, clock: testNow}
	w.landlord = s.person("Landlord", authz.RoleLandlord, authz.RoleTenant)
	w.tenant = s.person("Tenant", authz.RoleTenant)
	w.stranger = s.person("Stranger", authz.RoleTenant)
	w.r1, w.r2, w.r3 = s.room(w.landlord.ID, "P101"), s.room(w.landlord.ID, "P102"), s.room(w.landlord.ID, "P103")
	deps := Deps{
		Tx: fakeTx{}, Invitations: s, Assignments: fakeAssignments{s}, Rooms: s, Roles: fakeRoles{s}, Invitees: fakeInvitees{s},
		Tokens: &fakeTokens{}, Sealer: fakeSealer{}, Mailer: fakeMailer{s}, Notifier: fakeNotifier{s}, Audit: fakeAudit{s},
		Offers: fakeOffers{s}, Clock: ClockFunc(func() time.Time { return w.clock }),
	}
	opts := Options{WebURL: "http://web.test/"}
	w.invitations, w.answers = NewInvitations(deps, opts), NewAnswers(deps, opts)
	w.assignments, w.managed, w.access = NewAssignments(deps, opts), NewManaged(deps, opts), NewAccess(deps, opts)
	return w
}

func (w *world) invite(in CreateInput) (Issued, error) {
	if in.RoomIDs == nil {
		in.RoomIDs = []uuid.UUID{w.r1.ID}
	}
	return w.invitations.Create(context.Background(), w.landlord.ID, in)
}

func (w *world) mustInvite(in CreateInput) Issued {
	w.t.Helper()
	got, err := w.invite(in)
	if err != nil {
		w.t.Fatalf("invite: %v", err)
	}
	return got
}

func tokenOf(link string) string { return link[strings.Index(link, "token=")+len("token="):] }

func ids(r ...*fakeRoom) []uuid.UUID {
	out := make([]uuid.UUID, len(r))
	for i, x := range r {
		out[i] = x.ID
	}
	return out
}

func (w *world) accept(who invitation.Person, token string) (Accepted, error) {
	return w.answers.Accept(context.Background(), who.ID, Ref{Token: &token})
}

func (w *world) kinds() []string {
	var out []string
	for _, n := range w.s.notices {
		out = append(out, n.Kind)
	}
	return out
}

func (w *world) actions() []string {
	var out []string
	for _, e := range w.s.audits {
		out = append(out, e.Action)
	}
	return out
}

func TestCreateMakesAnInvitationForTheRooms(t *testing.T) {
	w := newWorld(t)
	got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2), Capabilities: []domain.Capability{domain.ContractsCreate}, NotifyLandlord: true})
	inv := got.Invitation
	if inv.Status != domain.InvitationPending || len(inv.RoomIDs) != 2 || inv.Audience() != invitation.AudiencePublic {
		t.Errorf("invitation = %+v", inv.Invitation)
	}
	// Capabilities come with what they require.
	if !slices.Equal(inv.Capabilities, []domain.Capability{domain.ContractsCreate, domain.ContractsPropose}) {
		t.Errorf("capabilities = %v", inv.Capabilities)
	}
	if want := testNow.Add(7 * 24 * time.Hour); !inv.ExpiresAt.Equal(want) {
		t.Errorf("expires %v want %v", inv.ExpiresAt, want)
	}
	token := tokenOf(got.Link.URL)
	stored := w.s.invs[inv.ID]
	if !strings.HasPrefix(got.Link.URL, "http://web.test/manager-invitation?token=") || !domain.ValidLinkToken(token) ||
		stored.TokenHash != "hash:"+token || stored.Sealed == nil || *stored.Sealed == token {
		t.Errorf("link = %q stored = %+v", got.Link.URL, stored)
	}
	if got.Delivery != (Delivery{}) || len(w.s.notices) != 0 || len(w.s.mails) != 0 {
		t.Error("an invitation to anybody is sent nowhere")
	}
	// One audit event per room.
	if !slices.Equal(w.actions(), []string{AuditInvited, AuditInvited}) || w.s.audits[0].ActorID != w.landlord.ID {
		t.Errorf("audit = %v", w.actions())
	}
}

func TestCreateRules(t *testing.T) {
	cases := map[string]struct {
		prepare func(w *world)
		in      func(w *world) CreateInput
		want    error
		names   []string
	}{
		"no rooms": {func(*world) {}, func(w *world) CreateInput { return CreateInput{RoomIDs: []uuid.UUID{}} }, domain.ErrNoRooms, nil},
		"replace not among the rooms": {func(*world) {}, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1), ReplaceRoomIDs: ids(w.r2)}
		}, domain.ErrReplaceNotListed, nil},
		"a room of another landlord": {func(w *world) { w.r1.Landlord = w.stranger.ID }, func(w *world) CreateInput { return CreateInput{RoomIDs: ids(w.r1)} }, domain.ErrRoomNotOwned, nil},
		"a deleted room":             {func(w *world) { w.r1.Live = false }, func(w *world) CreateInput { return CreateInput{RoomIDs: ids(w.r1)} }, domain.ErrRoomNotOwned, nil},
		"no such room":               {func(*world) {}, func(w *world) CreateInput { return CreateInput{RoomIDs: []uuid.UUID{uuid.New()}} }, domain.ErrRoomNotOwned, nil},
		"one of two rooms is not the landlord's": {func(w *world) { w.r2.Landlord = w.stranger.ID }, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1, w.r2)}
		}, domain.ErrRoomNotOwned, nil},
		"a room that has a manager": {func(w *world) { w.s.give(w.landlord.ID, w.tenant.ID, w.r2.ID) }, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1, w.r2)}
		}, domain.ErrRoomsManaged, []string{"P102"}},
		"a room in a waiting invitation": {func(w *world) { w.mustInvite(CreateInput{RoomIDs: ids(w.r3)}) }, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1, w.r3)}
		}, domain.ErrRoomsPending, []string{"P103"}},
		"the recipient cannot be invited": {func(w *world) { w.s.resolveErr = invitation.ErrRecipientInvalid }, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1)}
		}, invitation.ErrRecipientInvalid, nil},
		"the landlord's own email": {func(w *world) { w.s.resolveErr = invitation.ErrOwnEmail }, func(w *world) CreateInput {
			return CreateInput{RoomIDs: ids(w.r1)}
		}, invitation.ErrOwnEmail, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			tc.prepare(w)
			before := len(w.s.invs)
			_, err := w.invite(tc.in(w))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var re *domain.RoomsError
			if tc.names != nil && (!errors.As(err, &re) || !slices.Equal(re.Names, tc.names)) {
				t.Errorf("names = %+v, want %v", re, tc.names)
			}
			if len(w.s.invs) != before {
				t.Error("a refused invitation stores nothing")
			}
		})
	}
}

func TestCreateRoomsWaitingInAnInvitationMustBeReplaced(t *testing.T) {
	w := newWorld(t)
	old := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2)})
	w.s.audits = nil
	other := w.s.person("Other", authz.RoleTenant)
	w.s.invs[old.Invitation.ID].RecipientID = &other.ID

	// Replacing one room: the old invitation keeps the other.
	got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1), ReplaceRoomIDs: ids(w.r1)})
	oldNow := w.s.invs[old.Invitation.ID]
	if !slices.Equal(oldNow.RoomIDs, ids(w.r2)) || oldNow.Status != domain.InvitationPending {
		t.Errorf("old invitation = %+v", oldNow)
	}
	if !slices.Equal(got.Replaced, []uuid.UUID{old.Invitation.ID}) {
		t.Errorf("replaced = %v", got.Replaced)
	}
	// The recipient of the old invitation hears that a room left it.
	if !slices.Equal(w.kinds(), []string{NotifyInvitationWithdrawn}) || w.s.notices[0].UserID != other.ID || w.s.notices[0].Params["count"] != "1" {
		t.Errorf("notices = %+v", w.s.notices)
	}
	if !slices.Equal(w.actions(), []string{AuditInvitationRevoked, AuditInvited}) || w.s.audits[0].Meta["replaced"] != true ||
		w.s.audits[1].Meta["replaced_invitation_ids"].([]string)[0] != old.Invitation.ID.String() {
		t.Errorf("audit = %+v", w.s.audits)
	}

	// Replacing the last room withdraws the old invitation.
	w.mustInvite(CreateInput{RoomIDs: ids(w.r2), ReplaceRoomIDs: ids(w.r2)})
	if w.s.invs[old.Invitation.ID].Status != domain.InvitationRevoked || w.s.invs[old.Invitation.ID].Sealed != nil {
		t.Error("an invitation left with no room is withdrawn")
	}
}

func TestCreateExpiredInvitationDoesNotHoldItsRooms(t *testing.T) {
	w := newWorld(t)
	w.mustInvite(CreateInput{RoomIDs: ids(w.r1)})
	w.clock = testNow.Add(8 * 24 * time.Hour)
	if _, err := w.invite(CreateInput{RoomIDs: ids(w.r1)}); err != nil {
		t.Errorf("the rooms of an expired invitation are free: %v", err)
	}
}

func TestCreateDelivery(t *testing.T) {
	t.Run("to an account: inbox and email, no secret in the link", func(t *testing.T) {
		w := newWorld(t)
		w.s.resolve = invitation.Addressee{Recipient: &w.tenant}
		got := w.mustInvite(CreateInput{})
		if !got.Delivery.App || !got.Delivery.Email || got.Invitation.Audience() != invitation.AudienceAccount {
			t.Errorf("delivery = %+v", got.Delivery)
		}
		n := w.s.notices[0]
		if n.Kind != NotifyInvitationReceived || n.UserID != w.tenant.ID || n.Audience != AudienceTenant || strings.Contains(n.URL, "token") ||
			n.URL != "/manager-invitation?invitation="+got.Invitation.ID.String() {
			t.Errorf("notice = %+v", n)
		}
		if len(w.s.mails) != 1 || strings.Contains(w.s.mails[0].URL, "token=") || w.s.mails[0].Landlord != "Landlord" || w.s.mails[0].Rooms[0] != "P101" {
			t.Errorf("mails = %+v", w.s.mails)
		}
	})
	t.Run("to an address: email with the link", func(t *testing.T) {
		w := newWorld(t)
		email := "new@example.com"
		w.s.resolve = invitation.Addressee{Email: &email}
		got := w.mustInvite(CreateInput{})
		if got.Delivery.App || !got.Delivery.Email || len(w.s.notices) != 0 || !strings.Contains(w.s.mails[0].URL, "token="+tokenOf(got.Link.URL)) {
			t.Errorf("delivery = %+v mails = %+v", got.Delivery, w.s.mails)
		}
	})
	t.Run("an address that asked for no more invitations is not mailed", func(t *testing.T) {
		w := newWorld(t)
		email := "new@example.com"
		w.s.resolve, w.s.noMail = invitation.Addressee{Email: &email}, true
		if got := w.mustInvite(CreateInput{}); got.Delivery.Email || len(w.s.mails) != 0 {
			t.Error("the opt-out must be honoured")
		}
	})
}

func TestLinkRegenerateRevoke(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	got := w.mustInvite(CreateInput{})
	id := got.Invitation.ID

	link, err := w.invitations.Link(ctx, w.landlord.ID, id)
	if err != nil || link.URL != got.Link.URL {
		t.Errorf("the same link every time: %v %q", err, link.URL)
	}
	if _, err := w.invitations.Link(ctx, w.stranger.ID, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	renewed, err := w.invitations.Regenerate(ctx, w.landlord.ID, id)
	if err != nil || renewed.Link.URL == got.Link.URL {
		t.Fatalf("regenerate: %v", err)
	}
	// The old link is dead.
	old := tokenOf(got.Link.URL)
	if _, err := w.answers.Preview(ctx, w.tenant.ID, Ref{Token: &old}); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("old link: %v", err)
	}
	fresh := tokenOf(renewed.Link.URL)
	if _, err := w.answers.Preview(ctx, w.tenant.ID, Ref{Token: &fresh}); err != nil {
		t.Errorf("new link: %v", err)
	}
	// Expired: the link cannot be shown, a new one revives it.
	w.clock = testNow.Add(8 * 24 * time.Hour)
	if _, err := w.invitations.Link(ctx, w.landlord.ID, id); !errors.Is(err, domain.ErrLinkUnavailable) {
		t.Errorf("expired link: %v", err)
	}
	if _, err := w.invitations.Regenerate(ctx, w.landlord.ID, id); err != nil {
		t.Errorf("regenerate an expired one: %v", err)
	}
	w.clock = testNow
	w.s.audits = nil
	if err := w.invitations.Revoke(ctx, w.stranger.ID, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke by another: %v", err)
	}
	if err := w.invitations.Revoke(ctx, w.landlord.ID, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !slices.Equal(w.actions(), []string{AuditInvitationRevoked}) {
		t.Errorf("audit = %v", w.actions())
	}
	if err := w.invitations.Revoke(ctx, w.landlord.ID, id); !errors.Is(err, domain.ErrInvitationNotPending) {
		t.Errorf("revoke twice: %v", err)
	}
	if _, err := w.invitations.Regenerate(ctx, w.landlord.ID, id); !errors.Is(err, domain.ErrInvitationNotPending) {
		t.Errorf("regenerate a withdrawn one: %v", err)
	}
	if _, err := w.invitations.Link(ctx, w.landlord.ID, id); !errors.Is(err, domain.ErrLinkUnavailable) {
		t.Errorf("link of a withdrawn one: %v", err)
	}
}

func TestAcceptHandsTheRoomsOver(t *testing.T) {
	w := newWorld(t)
	got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2), Capabilities: []domain.Capability{domain.PaymentsReview}, NotifyLandlord: false})
	w.s.audits = nil

	res, err := w.accept(w.tenant, tokenOf(got.Link.URL))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(res.Assignments) != 2 || !w.s.roles[w.tenant.ID] {
		t.Fatalf("assignments = %+v roles = %v", res.Assignments, w.s.roles)
	}
	for _, a := range res.Assignments {
		if a.ManagerID != w.tenant.ID || a.LandlordID != w.landlord.ID || a.NotifyLandlord || !a.Can(domain.PaymentsReview) || !a.AcceptedAt.Equal(testNow) {
			t.Errorf("assignment = %+v", a)
		}
	}
	inv := w.s.invs[got.Invitation.ID]
	if inv.Status != domain.InvitationAccepted || inv.AnsweredByID == nil || *inv.AnsweredByID != w.tenant.ID || inv.Sealed != nil {
		t.Errorf("invitation = %+v", inv)
	}
	// One message for the landlord, naming the rooms.
	if !slices.Equal(w.kinds(), []string{NotifyAccepted}) || w.s.notices[0].UserID != w.landlord.ID || w.s.notices[0].Params["count"] != "2" ||
		w.s.notices[0].Params["rooms"] != "P101, P102" || w.s.notices[0].Params["manager"] != "Tenant" {
		t.Errorf("notices = %+v", w.s.notices)
	}
	if !slices.Equal(w.actions(), []string{AuditAccepted, AuditAccepted}) || w.s.audits[0].ActorID != w.tenant.ID || w.s.audits[0].OwnerID != w.landlord.ID {
		t.Errorf("audit = %+v", w.s.audits)
	}
	// The hand-over is now what a lookup finds.
	a, err := w.access.Of(context.Background(), w.tenant.ID, w.r1.ID)
	if err != nil || a == nil || !a.Can(domain.PaymentsReview) {
		t.Errorf("access = %+v %v", a, err)
	}
	if a, _ := w.access.Of(context.Background(), w.tenant.ID, w.r3.ID); a != nil {
		t.Error("a room that was not handed over has no hand-over")
	}
	// Once answered the link is dead.
	if _, err := w.accept(w.tenant, tokenOf(got.Link.URL)); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("accept twice: %v", err)
	}
}

func TestAcceptNeedsSomeoneTheLandlordMayAsk(t *testing.T) {
	cases := map[string]func(w *world, p *invitation.Person){
		"staff":          func(_ *world, p *invitation.Person) { p.Roles = []string{authz.RoleTenant, authz.RoleAdmin} },
		"super admin":    func(_ *world, p *invitation.Person) { p.Roles = []string{authz.RoleTenant, authz.RoleSuperAdmin} },
		"inactive":       func(_ *world, p *invitation.Person) { p.Active = false },
		"no tenant role": func(_ *world, p *invitation.Person) { p.Roles = []string{authz.RoleLandlord} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			got := w.mustInvite(CreateInput{})
			p := w.tenant
			mutate(w, &p)
			w.s.people[p.ID] = p
			if _, err := w.accept(p, tokenOf(got.Link.URL)); !errors.Is(err, domain.ErrCannotManage) {
				t.Fatalf("err = %v", err)
			}
			if len(w.s.assigns) != 0 || w.s.roles[p.ID] || w.s.invs[got.Invitation.ID].Status != domain.InvitationPending {
				t.Error("nothing is handed over and the invitation stays open")
			}
		})
	}
	t.Run("the landlord themselves", func(t *testing.T) {
		w := newWorld(t)
		got := w.mustInvite(CreateInput{})
		if _, err := w.accept(w.landlord, tokenOf(got.Link.URL)); !errors.Is(err, domain.ErrCannotManage) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAcceptRefusesAnInvitationThatIsNotForTheCaller(t *testing.T) {
	cases := map[string]func(w *world, id uuid.UUID, token string) (invitation.Person, string){
		"a token that does not exist": func(w *world, _ uuid.UUID, _ string) (invitation.Person, string) {
			return w.tenant, strings.Repeat("a", 64)
		},
		"a malformed token": func(w *world, _ uuid.UUID, _ string) (invitation.Person, string) { return w.tenant, "nope" },
		"an invitation to another account": func(w *world, id uuid.UUID, token string) (invitation.Person, string) {
			w.s.invs[id].RecipientID = &w.stranger.ID
			return w.tenant, token
		},
		"an invitation to another email": func(w *world, id uuid.UUID, token string) (invitation.Person, string) {
			e := "other@example.com"
			w.s.invs[id].RecipientEmail = &e
			return w.tenant, token
		},
		"an email invitation and an unverified account": func(w *world, id uuid.UUID, token string) (invitation.Person, string) {
			e := *w.tenant.Email
			w.s.invs[id].RecipientEmail = &e
			p := w.tenant
			p.EmailVerified = false
			w.s.people[p.ID] = p
			return p, token
		},
		"an expired invitation": func(w *world, _ uuid.UUID, token string) (invitation.Person, string) {
			w.clock = testNow.Add(8 * 24 * time.Hour)
			return w.tenant, token
		},
		"a withdrawn invitation": func(w *world, id uuid.UUID, token string) (invitation.Person, string) {
			_ = w.invitations.Revoke(context.Background(), w.landlord.ID, id)
			return w.tenant, token
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			got := w.mustInvite(CreateInput{})
			who, token := setup(w, got.Invitation.ID, tokenOf(got.Link.URL))
			if _, err := w.accept(who, token); !errors.Is(err, domain.ErrInvitationInvalid) {
				t.Fatalf("accept: %v", err)
			}
			if err := w.answers.Decline(context.Background(), who.ID, Ref{Token: &token}); !errors.Is(err, domain.ErrInvitationInvalid) {
				t.Errorf("decline: %v", err)
			}
			if _, err := w.answers.Preview(context.Background(), who.ID, Ref{Token: &token}); !errors.Is(err, domain.ErrInvitationInvalid) {
				t.Errorf("preview: %v", err)
			}
			if len(w.s.assigns) != 0 {
				t.Error("nothing is handed over")
			}
		})
	}
}

func TestAcceptByIDForTheNamedAccountOnly(t *testing.T) {
	w := newWorld(t)
	w.s.resolve = invitation.Addressee{Recipient: &w.tenant}
	got := w.mustInvite(CreateInput{})
	ctx := context.Background()
	id := got.Invitation.ID
	if _, err := w.answers.Accept(ctx, w.stranger.ID, Ref{InvitationID: &id}); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("another account: %v", err)
	}
	if _, err := w.answers.Accept(ctx, w.tenant.ID, Ref{}); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("no reference: %v", err)
	}
	items, total, err := w.answers.Inbox(ctx, w.tenant.ID, 10, 0)
	if err != nil || total != 1 || items[0].ID != id {
		t.Errorf("inbox = %v %d %v", items, total, err)
	}
	if _, err := w.answers.Accept(ctx, w.tenant.ID, Ref{InvitationID: &id}); err != nil {
		t.Errorf("the named account: %v", err)
	}
}

func TestAcceptIsAllOrNothing(t *testing.T) {
	cases := map[string]func(w *world){
		"a room was deleted":             func(w *world) { w.r2.Live = false },
		"a room changed hands":           func(w *world) { w.r2.Landlord = w.stranger.ID },
		"a room got a manager meanwhile": func(w *world) { w.s.give(w.landlord.ID, w.stranger.ID, w.r2.ID) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2)})
			change(w)
			before := len(w.s.assigns)
			if _, err := w.accept(w.tenant, tokenOf(got.Link.URL)); !errors.Is(err, domain.ErrRoomsChanged) {
				t.Fatalf("err = %v", err)
			}
			if len(w.s.assigns) != before || w.s.roles[w.tenant.ID] || w.s.invs[got.Invitation.ID].Status != domain.InvitationPending {
				t.Error("nothing is handed over, not even the first room")
			}
			if len(w.s.notices) != 0 {
				t.Error("the landlord is told nothing")
			}
		})
	}
}

func TestDecline(t *testing.T) {
	w := newWorld(t)
	got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2)})
	w.s.audits = nil
	token := tokenOf(got.Link.URL)
	if err := w.answers.Decline(context.Background(), w.tenant.ID, Ref{Token: &token}); err != nil {
		t.Fatalf("decline: %v", err)
	}
	inv := w.s.invs[got.Invitation.ID]
	if inv.Status != domain.InvitationDeclined || *inv.AnsweredByID != w.tenant.ID || inv.Sealed != nil || len(w.s.assigns) != 0 || w.s.roles[w.tenant.ID] {
		t.Errorf("invitation = %+v", inv)
	}
	if !slices.Equal(w.kinds(), []string{NotifyDeclined}) || w.s.notices[0].Params["count"] != "2" {
		t.Errorf("notices = %+v", w.s.notices)
	}
	if !slices.Equal(w.actions(), []string{AuditDeclined, AuditDeclined}) {
		t.Errorf("audit = %v", w.actions())
	}
	// The rooms are free for a new invitation.
	if _, err := w.invite(CreateInput{RoomIDs: ids(w.r1)}); err != nil {
		t.Errorf("rooms of a declined invitation: %v", err)
	}
}

// handed gives two rooms to the tenant through the real flow and clears what was recorded.
func (w *world) handed(caps ...domain.Capability) []domain.Assignment {
	w.t.Helper()
	got := w.mustInvite(CreateInput{RoomIDs: ids(w.r1, w.r2), Capabilities: caps, NotifyLandlord: true})
	res, err := w.accept(w.tenant, tokenOf(got.Link.URL))
	if err != nil {
		w.t.Fatalf("accept: %v", err)
	}
	w.s.audits, w.s.notices = nil, nil
	return res.Assignments
}

func TestRevokeTakesRoomsBackAndKeepsTheRoleWhileRoomsRemain(t *testing.T) {
	w := newWorld(t)
	as := w.handed()
	ctx := context.Background()

	if err := w.assignments.Revoke(ctx, w.stranger.ID, as[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if err := w.assignments.Revoke(ctx, w.landlord.ID, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := w.assignments.Revoke(ctx, w.landlord.ID, as[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !w.s.roles[w.tenant.ID] || len(w.s.withdrawn) != 0 {
		t.Error("a manager with a room left keeps the role and their offers")
	}
	if !slices.Equal(w.kinds(), []string{NotifyRevoked}) || w.s.notices[0].UserID != w.tenant.ID || w.s.notices[0].Params["rooms"] != "P101" {
		t.Errorf("notices = %+v", w.s.notices)
	}
	if !slices.Equal(w.actions(), []string{AuditRevoked}) || w.s.audits[0].TargetUserID == nil || *w.s.audits[0].TargetUserID != w.tenant.ID {
		t.Errorf("audit = %+v", w.s.audits)
	}
	// Revoked once, gone for good.
	if err := w.assignments.Revoke(ctx, w.landlord.ID, as[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke twice: %v", err)
	}
	// The room is free again.
	if _, err := w.invite(CreateInput{RoomIDs: ids(w.r1)}); err != nil {
		t.Errorf("a revoked room can be handed over again: %v", err)
	}
	// The last room takes the role and the waiting offers.
	if err := w.assignments.Revoke(ctx, w.landlord.ID, as[1].ID); err != nil {
		t.Fatalf("revoke last: %v", err)
	}
	if w.s.roles[w.tenant.ID] || !slices.Equal(w.s.withdrawn, []uuid.UUID{w.tenant.ID}) {
		t.Errorf("roles = %v withdrawn = %v", w.s.roles, w.s.withdrawn)
	}
}

func TestBulkRevoke(t *testing.T) {
	w := newWorld(t)
	as := w.handed()
	ctx := context.Background()
	res, err := w.assignments.BulkRevoke(ctx, w.landlord.ID, []uuid.UUID{as[0].ID, as[1].ID, uuid.New(), as[0].ID})
	if err != nil || len(res.Done) != 2 {
		t.Fatalf("bulk: %v %+v", err, res)
	}
	// One message per manager, however many rooms.
	if len(w.s.notices) != 1 || w.s.notices[0].Params["count"] != "2" || w.s.notices[0].Params["rooms"] != "P101, P102" {
		t.Errorf("notices = %+v", w.s.notices)
	}
	if len(w.s.audits) != 2 || w.s.roles[w.tenant.ID] {
		t.Errorf("audit = %d roles = %v", len(w.s.audits), w.s.roles)
	}
	// Somebody else's hand-overs are skipped, not failed.
	w.handed()
	other, err := w.assignments.BulkRevoke(ctx, w.stranger.ID, []uuid.UUID{w.s.aList[len(w.s.aList)-1]})
	if err != nil || len(other.Done) != 0 {
		t.Errorf("another landlord: %v %+v", err, other)
	}
}

func TestUpdateCapabilities(t *testing.T) {
	w := newWorld(t)
	as := w.handed()
	ctx := context.Background()
	no := false

	got, err := w.assignments.Update(ctx, w.landlord.ID, as[0].ID, UpdateInput{Grant: []domain.Capability{domain.ContractsCreate}, NotifyLandlord: &no})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !slices.Equal(got.Capabilities, []domain.Capability{domain.ContractsCreate, domain.ContractsPropose}) || got.NotifyLandlord {
		t.Errorf("got = %+v", got.Assignment)
	}
	if len(w.s.audits) != 1 || w.s.audits[0].Action != AuditUpdated || w.s.audits[0].Meta["assignment_id"] != as[0].ID.String() {
		t.Fatalf("audit = %+v", w.s.audits)
	}
	ch := w.s.audits[0].Changes
	if ch["capabilities"].(map[string]any)["to"].([]string)[0] != "contracts.create" || ch["notify_landlord"].(map[string]any)["to"] != false {
		t.Errorf("changes = %+v", ch)
	}
	// Taking propose takes create with it.
	got, err = w.assignments.Update(ctx, w.landlord.ID, as[0].ID, UpdateInput{Revoke: []domain.Capability{domain.ContractsPropose}})
	if err != nil || len(got.Capabilities) != 0 {
		t.Errorf("revoke propose: %v %+v", err, got.Capabilities)
	}
	// No change, no audit.
	w.s.audits = nil
	if _, err := w.assignments.Update(ctx, w.landlord.ID, as[0].ID, UpdateInput{}); err != nil || len(w.s.audits) != 0 {
		t.Errorf("no-op update: %v %v", err, w.s.audits)
	}
	if _, err := w.assignments.Update(ctx, w.stranger.ID, as[0].ID, UpdateInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
}

func TestBulkUpdateAndGroups(t *testing.T) {
	w := newWorld(t)
	as := w.handed(domain.PaymentsReview)
	ctx := context.Background()
	w.s.give(w.landlord.ID, w.stranger.ID, w.r3.ID)

	groups, total, err := w.assignments.Groups(ctx, w.landlord.ID, 10, 0)
	if err != nil || total != 2 || len(groups) != 2 {
		t.Fatalf("groups: %v %d", err, total)
	}
	var mine GroupView
	for _, g := range groups {
		if g.RoomCount == 2 {
			mine = g
		}
	}
	if mine.Notify != domain.FlagAll || mine.Capabilities[domain.PaymentsReview] != domain.FlagAll || mine.Capabilities[domain.ContractsCreate] != domain.FlagNone {
		t.Errorf("flags = %+v", mine)
	}
	// Change one room: the group becomes mixed.
	if _, err := w.assignments.Update(ctx, w.landlord.ID, as[0].ID, UpdateInput{Revoke: []domain.Capability{domain.PaymentsReview}}); err != nil {
		t.Fatal(err)
	}
	groups, _, _ = w.assignments.Groups(ctx, w.landlord.ID, 10, 0)
	for _, g := range groups {
		if g.RoomCount == 2 && g.Capabilities[domain.PaymentsReview] != domain.FlagSome {
			t.Errorf("flag = %v", g.Capabilities[domain.PaymentsReview])
		}
	}
	// Bulk: the same change for the whole group; unknown ids are skipped.
	res, err := w.assignments.BulkUpdate(ctx, w.landlord.ID, []uuid.UUID{as[0].ID, as[1].ID, uuid.New()}, UpdateInput{Grant: []domain.Capability{domain.ReviewRegistrations}})
	if err != nil || len(res.Done) != 2 {
		t.Fatalf("bulk: %v %+v", err, res)
	}
	d, _ := w.assignments.Get(ctx, w.landlord.ID, as[1].ID)
	if !d.Can(domain.ReviewRegistrations) {
		t.Error("the bulk change reached every hand-over")
	}
	busy, _ := w.assignments.BusyRoomIDs(ctx, w.landlord.ID)
	if len(busy) != 3 {
		t.Errorf("busy = %v", busy)
	}
	rooms, total, err := w.assignments.List(ctx, w.landlord.ID, &w.tenant.ID, 10, 0)
	if err != nil || total != 2 || len(rooms) != 2 {
		t.Errorf("list of one manager: %v %d", err, total)
	}
}

func TestManagedListsOnlyTheManagersRooms(t *testing.T) {
	w := newWorld(t)
	w.handed()
	w.s.give(w.landlord.ID, w.stranger.ID, w.r3.ID)
	items, total, err := w.managed.Rooms(context.Background(), w.tenant.ID, nil, 10, 0)
	if err != nil || total != 2 || len(items) != 2 {
		t.Errorf("managed = %v %d %v", items, total, err)
	}
	ids, err := w.access.RoomIDs(context.Background(), w.tenant.ID)
	if err != nil || len(ids) != 2 {
		t.Errorf("room ids = %v %v", ids, err)
	}
}
