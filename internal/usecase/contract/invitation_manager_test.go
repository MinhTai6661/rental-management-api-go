package contract

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
)

// mgrWorld is an invitation world where one account manages the room.
type mgrWorld struct {
	*invWorld
	manager  uuid.UUID
	managers fakeManagers
}

func newMgrWorld(t *testing.T, g Grant) *mgrWorld {
	t.Helper()
	iw := newInvWorld(t)
	m := &mgrWorld{invWorld: iw, manager: iw.store.user("Manager"), managers: fakeManagers{}}
	m.managers[[2]uuid.UUID{m.manager, iw.room.ID}] = g
	iw.deps.Managers = m.managers
	iw.uc = NewInvitations(iw.deps, Options{WebURL: "http://web.test/"})
	iw.answers = NewInvitationAnswers(iw.deps, Options{WebURL: "http://web.test/"})
	iw.links = NewRoomLinks(iw.deps, Options{})
	iw.invitees.people = map[uuid.UUID]invitation.Person{
		m.manager: {ID: m.manager, FullName: "Manager", Roles: []string{"tenant", "manager"}, Active: true},
	}
	return m
}

func (m *mgrWorld) offer(actor uuid.UUID, in CreateInvitationInput) (IssuedInvitation, error) {
	if in.RoomID == uuid.Nil {
		in.RoomID = m.room.ID
	}
	in.StartsOn = m.today()
	return m.uc.Create(context.Background(), actor, in)
}

func TestManagerCannotOfferARoomWithoutPropose(t *testing.T) {
	m := newMgrWorld(t, Grant{})
	if _, err := m.offer(m.manager, CreateInvitationInput{}); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Errorf("a manager without contracts.propose: %v", err)
	}
	// A room nobody handed over reads the same.
	if _, err := m.offer(m.other, CreateInvitationInput{}); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Errorf("a stranger: %v", err)
	}
	if len(m.repo.rows) != 0 {
		t.Error("nothing is stored for a refused offer")
	}
}

func TestManagerOfferWaitsForTheLandlord(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	m.invitees.result = invitation.Addressee{Recipient: m.account(ptr("tenant@example.com"))}
	got, err := m.offer(m.manager, CreateInvitationInput{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	inv := got.Invitation
	if inv.Status != domain.InvitationAwaitingApproval || inv.CreatedByID == nil || *inv.CreatedByID != m.manager || inv.LandlordID != m.landlord {
		t.Errorf("invitation = %+v", inv.Invitation)
	}
	if got.Link.URL != "" || got.Delivery != (Delivery{}) || m.repo.rows[inv.ID].Sealed != nil {
		t.Error("an offer awaiting approval has no link, no delivery and no kept secret")
	}
	if len(m.mailer.sent) != 0 {
		t.Error("nothing is emailed before the landlord approves")
	}
	// Only the landlord hears of it, and the person it is for hears nothing.
	if len(m.notifier.sent) != 1 || m.notifier.sent[0].Kind != NotifyInvitationNeedsApproval || m.notifier.sent[0].UserID != m.landlord ||
		m.notifier.sent[0].Params["manager"] != "Manager" {
		t.Errorf("notifications = %+v", m.notifier.sent)
	}
	if m.audit.events[0].ActorID != m.manager || m.audit.events[0].OwnerID != m.landlord || m.audit.events[0].Meta["awaiting_approval"] != true {
		t.Errorf("audit = %+v", m.audit.events)
	}
}

func TestManagerWithCreateSendsAtOnce(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true, Create: true})
	m.invitees.result = invitation.Addressee{Recipient: m.account(ptr("tenant@example.com"))}
	got, err := m.offer(m.manager, CreateInvitationInput{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Invitation.Status != domain.InvitationPending || got.Invitation.CreatedByID == nil || *got.Invitation.CreatedByID != m.manager {
		t.Errorf("invitation = %+v", got.Invitation.Invitation)
	}
	if !got.Delivery.App || got.Link.URL == "" || m.repo.rows[got.Invitation.ID].Sealed == nil {
		t.Errorf("delivery = %+v", got)
	}
}

func TestManagerCannotOfferTheRoomToItsLandlord(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true, Create: true})
	landlordAccount := m.account(nil)
	landlordAccount.ID = m.landlord
	m.invitees.result = invitation.Addressee{Recipient: landlordAccount}
	if _, err := m.offer(m.manager, CreateInvitationInput{}); !errors.Is(err, invitation.ErrRecipientInvalid) {
		t.Errorf("an offer to the landlord: %v", err)
	}
}

func TestManagerSeesOnlyWhatTheyWrote(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	mine, _ := m.offer(m.manager, CreateInvitationInput{})
	own := m.mustCreate(CreateInvitationInput{}) // the landlord's own offer on the same room
	ctx := context.Background()

	items, total, err := m.uc.ListAuthored(ctx, m.manager, nil, 10, 0)
	if err != nil || total != 1 || items[0].ID != mine.Invitation.ID {
		t.Errorf("list = %+v, %d, %v", items, total, err)
	}
	if _, err := m.uc.Get(ctx, m.manager, own.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the landlord's offer: %v", err)
	}
	if _, err := m.uc.Get(ctx, m.other, mine.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another account: %v", err)
	}
	if _, err := m.uc.Get(ctx, m.manager, mine.Invitation.ID); err != nil {
		t.Errorf("the author: %v", err)
	}
	// A manager withdraws their own offer but not the landlord's.
	if err := m.uc.Revoke(ctx, m.manager, own.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke the landlord's: %v", err)
	}
	if err := m.uc.Revoke(ctx, m.manager, mine.Invitation.ID); err != nil {
		t.Errorf("revoke own: %v", err)
	}
	// Once they lose the room their offers are hidden.
	m.repo.lost = map[uuid.UUID]bool{m.manager: true}
	if _, err := m.uc.Get(ctx, m.manager, mine.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after losing the room: %v", err)
	}
}

func TestApproveOpensTheOffer(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	tenant := m.account(ptr("tenant@example.com"))
	m.invitees.people[m.tenant] = *tenant
	m.invitees.result = invitation.Addressee{Recipient: tenant}
	made, _ := m.offer(m.manager, CreateInvitationInput{})
	id := made.Invitation.ID
	m.notifier.sent, m.audit.events = nil, nil
	ctx := context.Background()

	if _, err := m.uc.Approve(ctx, m.other, id, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := m.uc.Approve(ctx, m.manager, id, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the manager cannot approve their own offer: %v", err)
	}
	got, err := m.uc.Approve(ctx, m.landlord, id, ptr("ok"))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	stored := m.repo.rows[id]
	if stored.Status != domain.InvitationPending || stored.Sealed == nil || stored.ApprovedByID == nil || *stored.ApprovedByID != m.landlord ||
		*stored.ApprovalNote != "ok" || !domain.ValidLinkToken(tokenOf(got.Link.URL)) || stored.TokenHash != "hash:"+tokenOf(got.Link.URL) {
		t.Errorf("stored = %+v", stored)
	}
	if !got.Delivery.App || len(m.mailer.sent) != 1 {
		t.Errorf("the person it is for gets it now: %+v", got.Delivery)
	}
	kinds := []string{}
	for _, n := range m.notifier.sent {
		kinds = append(kinds, n.Kind)
	}
	if !slices.Contains(kinds, NotifyInvitationApproved) || !slices.Contains(kinds, NotifyInvitationReceived) {
		t.Errorf("notifications = %v", kinds)
	}
	if !slices.Equal(m.audit.actions(), []string{AuditInvitationApproved}) {
		t.Errorf("audit = %v", m.audit.actions())
	}
	if _, err := m.uc.Approve(ctx, m.landlord, id, nil); !errors.Is(err, domain.ErrInvitationNotAwaiting) {
		t.Errorf("approve twice: %v", err)
	}
}

func TestApproveRefusesARoomATenantNowHolds(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	made, _ := m.offer(m.manager, CreateInvitationInput{})
	m.store.put(m.room, &m.tenant, domain.StatusActive)
	if _, err := m.uc.Approve(context.Background(), m.landlord, made.Invitation.ID, nil); !errors.Is(err, domain.ErrRoomOccupied) {
		t.Errorf("approve on an occupied room: %v", err)
	}
	if m.repo.rows[made.Invitation.ID].Status != domain.InvitationAwaitingApproval {
		t.Error("the offer keeps waiting")
	}
}

func TestRejectWithdrawsTheOfferAndTellsTheManager(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	made, _ := m.offer(m.manager, CreateInvitationInput{})
	m.notifier.sent, m.audit.events = nil, nil
	ctx := context.Background()
	if err := m.uc.Reject(ctx, m.other, made.Invitation.ID, "no"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if err := m.uc.Reject(ctx, m.landlord, made.Invitation.ID, "Giá quá thấp"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	stored := m.repo.rows[made.Invitation.ID]
	if stored.Status != domain.InvitationRevoked || *stored.ApprovalNote != "Giá quá thấp" || stored.AnsweredAt == nil {
		t.Errorf("stored = %+v", stored)
	}
	if len(m.notifier.sent) != 1 || m.notifier.sent[0].Kind != NotifyInvitationRejected || m.notifier.sent[0].UserID != m.manager ||
		m.notifier.sent[0].Params["reason"] != "Giá quá thấp" {
		t.Errorf("notifications = %+v", m.notifier.sent)
	}
	if err := m.uc.Reject(ctx, m.landlord, made.Invitation.ID, "again"); !errors.Is(err, domain.ErrInvitationNotAwaiting) {
		t.Errorf("reject twice: %v", err)
	}
	if _, err := m.uc.Approve(ctx, m.landlord, made.Invitation.ID, nil); !errors.Is(err, domain.ErrInvitationNotAwaiting) {
		t.Errorf("approve a rejected one: %v", err)
	}
}

func TestOnlyAwaitingOffersCanBeApprovedOrRejected(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	pending := m.mustCreate(CreateInvitationInput{})
	ctx := context.Background()
	if _, err := m.uc.Approve(ctx, m.landlord, pending.Invitation.ID, nil); !errors.Is(err, domain.ErrInvitationNotAwaiting) {
		t.Errorf("approve a pending offer: %v", err)
	}
	if err := m.uc.Reject(ctx, m.landlord, pending.Invitation.ID, "x"); !errors.Is(err, domain.ErrInvitationNotAwaiting) {
		t.Errorf("reject a pending offer: %v", err)
	}
}

func TestAuthorCannotAcceptTheirOwnOffer(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true, Create: true})
	made, err := m.offer(m.manager, CreateInvitationInput{})
	if err != nil {
		t.Fatal(err)
	}
	token := tokenOf(made.Link.URL)
	_, err = m.answers.Accept(context.Background(), m.manager, InvitationRef{Token: &token})
	if !errors.Is(err, domain.ErrInvitationOwnOffer) {
		t.Errorf("a manager accepting what they wrote: %v", err)
	}
}

func TestWithdrawAwaitingOnlyTouchesTheManagersWaitingOffers(t *testing.T) {
	m := newMgrWorld(t, Grant{Propose: true})
	waiting, _ := m.offer(m.manager, CreateInvitationInput{})
	landlords := m.mustCreate(CreateInvitationInput{})
	links := NewManagerLinks(m.deps, Options{})
	m.audit.events = nil
	if err := links.WithdrawAwaiting(context.Background(), m.manager); err != nil {
		t.Fatal(err)
	}
	if m.repo.rows[waiting.Invitation.ID].Status != domain.InvitationRevoked || m.repo.rows[landlords.Invitation.ID].Status != domain.InvitationPending {
		t.Error("only the manager's waiting offer is withdrawn")
	}
	if !slices.Equal(m.audit.actions(), []string{AuditInvitationRevoked}) || m.audit.events[0].Meta["reason"] != "manager_lost_rooms" {
		t.Errorf("audit = %+v", m.audit.events)
	}
}
