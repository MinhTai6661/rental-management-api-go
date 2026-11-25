package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
	"rental-management/internal/domain/shared"
)

// The manager use cases run here against hand-written fakes of the ports: no database. store is one in-memory world
// (rooms, accounts, invitations, hand-overs) that implements every repository port.

type fakeRoom struct {
	ID       uuid.UUID
	Landlord uuid.UUID
	Name     string
	Live     bool
}

type store struct {
	now     time.Time
	rooms   map[uuid.UUID]*fakeRoom
	people  map[uuid.UUID]invitation.Person
	invs    map[uuid.UUID]*domain.Invitation
	invList []uuid.UUID
	assigns map[uuid.UUID]*domain.Assignment
	aList   []uuid.UUID
	roles   map[uuid.UUID]bool
	// withdrawn records the managers whose waiting offers were withdrawn.
	withdrawn []uuid.UUID
	// resolve is what Resolve answers; resolveErr what it fails with.
	resolve    invitation.Addressee
	resolveErr error
	noMail     bool
	// events
	notices []Notification
	audits  []AuditEvent
	mails   []InvitationMail
}

func newStore(now time.Time) *store {
	return &store{
		now: now, rooms: map[uuid.UUID]*fakeRoom{}, people: map[uuid.UUID]invitation.Person{}, invs: map[uuid.UUID]*domain.Invitation{},
		assigns: map[uuid.UUID]*domain.Assignment{}, roles: map[uuid.UUID]bool{},
	}
}

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func (s *store) person(name string, roles ...string) invitation.Person {
	email := strings.ToLower(name) + "@example.com"
	p := invitation.Person{ID: newID(), FullName: name, Email: &email, EmailVerified: true, Active: true, AcceptsInvitations: true, Roles: roles}
	s.people[p.ID] = p
	return p
}

func (s *store) room(landlord uuid.UUID, name string) *fakeRoom {
	r := &fakeRoom{ID: newID(), Landlord: landlord, Name: name, Live: true}
	s.rooms[r.ID] = r
	return r
}

// give stores an active hand-over directly.
func (s *store) give(landlord, manager, room uuid.UUID, caps ...domain.Capability) *domain.Assignment {
	a := &domain.Assignment{
		ID: newID(), LandlordID: landlord, RoomID: room, ManagerID: manager, Status: domain.AssignmentActive,
		NotifyLandlord: true, Capabilities: domain.Set(caps), AssignedByID: landlord, AcceptedAt: s.now, CreatedAt: s.now,
	}
	s.assigns[a.ID] = a
	s.aList = append(s.aList, a.ID)
	return a
}

// TxManager: the fake has no rollback, so tests that need one check that nothing was written before the error.
type fakeTx struct{}

func (fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

// RoomReader

func (s *store) Lock(_ context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]RoomInfo, error) {
	var out []RoomInfo
	for _, id := range ids {
		if r, ok := s.rooms[id]; ok && r.Live && r.Landlord == landlordID {
			out = append(out, RoomInfo{ID: r.ID, Name: r.Name})
		}
	}
	return out, nil
}

func (s *store) Names(_ context.Context, ids []uuid.UUID) ([]RoomInfo, error) {
	var out []RoomInfo
	for _, id := range ids {
		if r, ok := s.rooms[id]; ok {
			out = append(out, RoomInfo{ID: r.ID, Name: r.Name})
		}
	}
	return out, nil
}

// InvitationRepository

func (s *store) Insert(_ context.Context, inv domain.Invitation) (uuid.UUID, error) {
	inv.ID = newID()
	inv.CreatedAt = s.now
	s.invs[inv.ID] = &inv
	s.invList = append(s.invList, inv.ID)
	return inv.ID, nil
}

func (s *store) detail(inv *domain.Invitation) domain.InvitationDetail {
	d := domain.InvitationDetail{Invitation: *inv}
	for _, r := range inv.RoomIDs {
		d.Rooms = append(d.Rooms, domain.Room{ID: r, Name: s.rooms[r].Name})
	}
	return d
}

func (s *store) Get(_ context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error) {
	inv, ok := s.invs[id]
	if !ok || inv.LandlordID != landlordID {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	return s.detail(inv), nil
}

func (s *store) List(_ context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	var out []domain.InvitationDetail
	for _, id := range s.invList {
		inv := s.invs[id]
		if inv.LandlordID == landlordID && (status == nil || inv.Status == *status) {
			out = append(out, s.detail(inv))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (s *store) ForUpdate(_ context.Context, id uuid.UUID) (domain.Invitation, error) {
	inv, ok := s.invs[id]
	if !ok {
		return domain.Invitation{}, shared.ErrNotFound
	}
	return *inv, nil
}

func (s *store) Save(_ context.Context, inv domain.Invitation) error {
	s.invs[inv.ID] = &inv
	return nil
}

func (s *store) FindByTokenHash(_ context.Context, hash string) (domain.Invitation, error) {
	for _, inv := range s.invs {
		if inv.TokenHash == hash {
			return *inv, nil
		}
	}
	return domain.Invitation{}, shared.ErrNotFound
}

func (s *store) FindForRecipient(_ context.Context, id, recipientID uuid.UUID) (domain.Invitation, error) {
	inv, ok := s.invs[id]
	if !ok || inv.RecipientID == nil || *inv.RecipientID != recipientID {
		return domain.Invitation{}, shared.ErrNotFound
	}
	return *inv, nil
}

func (s *store) Offer(_ context.Context, id uuid.UUID) (domain.InvitationOffer, error) {
	inv, ok := s.invs[id]
	if !ok {
		return domain.InvitationOffer{}, shared.ErrNotFound
	}
	d := s.detail(inv)
	return domain.InvitationOffer{Invitation: *inv, Rooms: d.Rooms, LandlordName: s.people[inv.LandlordID].FullName}, nil
}

func (s *store) Inbox(_ context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error) {
	var out []domain.InvitationOffer
	for i := len(s.invList) - 1; i >= 0; i-- {
		inv := s.invs[s.invList[i]]
		if inv.RecipientID != nil && *inv.RecipientID == recipientID && inv.OpenAt(now) {
			out = append(out, domain.InvitationOffer{Invitation: *inv})
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (s *store) LockOpenOfRooms(_ context.Context, roomIDs []uuid.UUID, now time.Time) ([]domain.Invitation, error) {
	var out []domain.Invitation
	for _, id := range s.invList {
		inv := s.invs[id]
		if inv.OpenAt(now) && slices.ContainsFunc(inv.RoomIDs, func(r uuid.UUID) bool { return slices.Contains(roomIDs, r) }) {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (s *store) RemoveRooms(_ context.Context, invitationID uuid.UUID, roomIDs []uuid.UUID) error {
	inv := s.invs[invitationID]
	inv.RoomIDs = slices.DeleteFunc(slices.Clone(inv.RoomIDs), func(r uuid.UUID) bool { return slices.Contains(roomIDs, r) })
	return nil
}

func (s *store) RevokeOpenOfRooms(_ context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	var out []domain.Invitation
	for _, id := range s.invList {
		inv := s.invs[id]
		if slices.ContainsFunc(inv.RoomIDs, func(r uuid.UUID) bool { return slices.Contains(roomIDs, r) }) && inv.Revoke(at) == nil {
			out = append(out, *inv)
		}
	}
	return out, nil
}

// AssignmentRepository

func (s *store) activeOfRoom(room uuid.UUID) *domain.Assignment {
	for _, id := range s.aList {
		if a := s.assigns[id]; a.RoomID == room && a.Active() {
			return a
		}
	}
	return nil
}

type fakeAssignments struct{ *store }

func (f fakeAssignments) Insert(_ context.Context, landlordID, managerID uuid.UUID, roomIDs []uuid.UUID, notify bool, caps []domain.Capability, at time.Time) ([]domain.Assignment, error) {
	var out []domain.Assignment
	for _, r := range roomIDs {
		a := f.give(landlordID, managerID, r, caps...)
		a.NotifyLandlord, a.AcceptedAt = notify, at
		out = append(out, *a)
	}
	return out, nil
}

func (f fakeAssignments) ManagedRooms(_ context.Context, roomIDs []uuid.UUID) ([]RoomInfo, error) {
	var out []RoomInfo
	for _, r := range roomIDs {
		if f.activeOfRoom(r) != nil {
			out = append(out, RoomInfo{ID: r, Name: f.rooms[r].Name})
		}
	}
	return out, nil
}

func (f fakeAssignments) ForUpdate(_ context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.Assignment, error) {
	var out []domain.Assignment
	for _, id := range ids {
		if a, ok := f.assigns[id]; ok && a.Active() && a.LandlordID == landlordID {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (f fakeAssignments) detail(a *domain.Assignment) AssignmentDetail {
	return AssignmentDetail{
		Assignment: *a, Room: domain.Room{ID: a.RoomID, Name: f.rooms[a.RoomID].Name},
		Manager: domain.Person{ID: a.ManagerID, FullName: f.people[a.ManagerID].FullName},
	}
}

func (f fakeAssignments) Get(_ context.Context, landlordID, id uuid.UUID) (AssignmentDetail, error) {
	a, ok := f.assigns[id]
	if !ok || !a.Active() || a.LandlordID != landlordID {
		return AssignmentDetail{}, shared.ErrNotFound
	}
	return f.detail(a), nil
}

func (f fakeAssignments) Save(_ context.Context, a domain.Assignment) error {
	f.assigns[a.ID] = &a
	return nil
}

func (f fakeAssignments) Revoke(_ context.Context, ids []uuid.UUID, at time.Time) error {
	for _, id := range ids {
		a := f.assigns[id]
		a.Status, a.RevokedAt = domain.AssignmentRevoked, &at
	}
	return nil
}

func (f fakeAssignments) List(_ context.Context, landlordID uuid.UUID, managerID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error) {
	var out []AssignmentDetail
	for i := len(f.aList) - 1; i >= 0; i-- {
		a := f.assigns[f.aList[i]]
		if a.Active() && a.LandlordID == landlordID && (managerID == nil || a.ManagerID == *managerID) {
			out = append(out, f.detail(a))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f fakeAssignments) Groups(_ context.Context, landlordID uuid.UUID, limit, offset int) ([]Group, int, error) {
	byManager := map[uuid.UUID]*Group{}
	var order []uuid.UUID
	for i := len(f.aList) - 1; i >= 0; i-- {
		a := f.assigns[f.aList[i]]
		if !a.Active() || a.LandlordID != landlordID {
			continue
		}
		g, ok := byManager[a.ManagerID]
		if !ok {
			g = &Group{Manager: domain.Person{ID: a.ManagerID}, Capabilities: map[domain.Capability]int{}}
			byManager[a.ManagerID] = g
			order = append(order, a.ManagerID)
		}
		g.RoomCount++
		g.AssignmentIDs = append(g.AssignmentIDs, a.ID)
		g.RoomIDs = append(g.RoomIDs, a.RoomID)
		if a.NotifyLandlord {
			g.NotifyCount++
		}
		for _, c := range a.Capabilities {
			g.Capabilities[c]++
		}
	}
	var out []Group
	for _, m := range order {
		out = append(out, *byManager[m])
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f fakeAssignments) BusyRoomIDs(_ context.Context, landlordID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, id := range f.aList {
		if a := f.assigns[id]; a.Active() && a.LandlordID == landlordID {
			out = append(out, a.RoomID)
		}
	}
	return out, nil
}

func (f fakeAssignments) ManagedList(_ context.Context, managerID uuid.UUID, landlordID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error) {
	var out []AssignmentDetail
	for i := len(f.aList) - 1; i >= 0; i-- {
		a := f.assigns[f.aList[i]]
		if a.Active() && a.ManagerID == managerID && (landlordID == nil || a.LandlordID == *landlordID) {
			out = append(out, f.detail(a))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f fakeAssignments) ManagedGroups(_ context.Context, managerID uuid.UUID, limit, offset int) ([]ManagedGroup, int, error) {
	return nil, 0, nil
}

func (f fakeAssignments) Of(_ context.Context, managerID, roomID uuid.UUID) (domain.Assignment, error) {
	for _, id := range f.aList {
		if a := f.assigns[id]; a.Active() && a.ManagerID == managerID && a.RoomID == roomID {
			return *a, nil
		}
	}
	return domain.Assignment{}, shared.ErrNotFound
}

func (f fakeAssignments) CountActive(_ context.Context, managerID uuid.UUID) (int, error) {
	n := 0
	for _, a := range f.assigns {
		if a.Active() && a.ManagerID == managerID {
			n++
		}
	}
	return n, nil
}

func (f fakeAssignments) ActiveOf(_ context.Context, managerID uuid.UUID) ([]domain.Assignment, error) {
	var out []domain.Assignment
	for _, id := range f.aList {
		if a := f.assigns[id]; a.Active() && a.ManagerID == managerID {
			out = append(out, *a)
		}
	}
	return out, nil
}

// Roles

type fakeRoles struct{ *store }

func (f fakeRoles) Grant(_ context.Context, userID uuid.UUID) error {
	f.roles[userID] = true
	return nil
}

func (f fakeRoles) Revoke(_ context.Context, userID uuid.UUID) error {
	delete(f.roles, userID)
	return nil
}

// Invitees

type fakeInvitees struct{ *store }

func (f fakeInvitees) Resolve(context.Context, uuid.UUID, *uuid.UUID, *string) (invitation.Addressee, error) {
	return f.resolve, f.resolveErr
}

func (f fakeInvitees) Person(_ context.Context, id uuid.UUID) (invitation.Person, error) {
	if p, ok := f.people[id]; ok {
		return p, nil
	}
	return invitation.Person{}, shared.ErrNotFound
}

func (f fakeInvitees) MayMail(context.Context, string) (bool, error) { return !f.noMail, nil }

// Tokens and sealer

type fakeTokens struct{ n int }

func (f *fakeTokens) New() (token, hash string, err error) {
	f.n++
	token = fmt.Sprintf("%064d", f.n)
	return token, f.Hash(token), nil
}

func (*fakeTokens) Hash(token string) string { return "hash:" + token }

type fakeSealer struct{}

func (fakeSealer) Seal(plain string) (string, error) { return "sealed:" + plain, nil }

func (fakeSealer) Open(sealed string) (string, error) {
	plain, ok := strings.CutPrefix(sealed, "sealed:")
	if !ok {
		return "", errors.New("cannot open")
	}
	return plain, nil
}

// Notifier, audit, mail, offers

type fakeNotifier struct{ *store }

func (f fakeNotifier) Notify(_ context.Context, n Notification) error {
	f.notices = append(f.notices, n)
	return nil
}

type fakeAudit struct{ *store }

func (f fakeAudit) Record(_ context.Context, e AuditEvent) error {
	f.audits = append(f.audits, e)
	return nil
}

type fakeMailer struct{ *store }

func (f fakeMailer) SendInvitation(_ context.Context, m InvitationMail) error {
	f.mails = append(f.mails, m)
	return nil
}

type fakeOffers struct{ *store }

func (f fakeOffers) WithdrawAwaiting(_ context.Context, managerID uuid.UUID) error {
	f.withdrawn = append(f.withdrawn, managerID)
	return nil
}
