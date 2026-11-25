package contract

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
	"rental-management/internal/domain/shared"
)

// The contract use cases run here against hand-written fakes of the ports: no database. fakeStore is one in-memory
// world (rooms, users, contracts) that implements the repository, the room reader and the room status setter, so a
// test reads like a story: rooms and people exist, contracts change them.

type fakeStore struct {
	now       time.Time
	rooms     map[uuid.UUID]*domain.RoomFacts
	names     map[uuid.UUID]string // user id -> full name
	contracts map[uuid.UUID]*domain.Contract
	deleted   map[uuid.UUID]bool
	order     []uuid.UUID                  // insertion order, for stable lists
	statuses  []string                     // what the setter was asked, as "room:status"
	billing   map[uuid.UUID]domain.Billing // contract id -> what the bills and payments say (the read-only view)
	setterErr error
}

func newStore(now time.Time) *fakeStore {
	return &fakeStore{
		now: now, rooms: map[uuid.UUID]*domain.RoomFacts{}, names: map[uuid.UUID]string{},
		contracts: map[uuid.UUID]*domain.Contract{}, deleted: map[uuid.UUID]bool{},
	}
}

func (f *fakeStore) user(name string) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.names[id] = name
	return id
}

func (f *fakeStore) room(landlord uuid.UUID, name, status string) *domain.RoomFacts {
	r := &domain.RoomFacts{
		ID: uuid.Must(uuid.NewV7()), LandlordID: landlord, Name: name, Status: status, PriceVND: 3_000_000,
		MinPeriod: 1, PeriodUnit: domain.UnitMonth, Live: true, Listed: true,
	}
	f.rooms[r.ID] = r
	return r
}

// put stores a contract directly.
func (f *fakeStore) put(room *domain.RoomFacts, tenant *uuid.UUID, status domain.Status) *domain.Contract {
	confirmed := f.now
	c := &domain.Contract{
		ID: uuid.Must(uuid.NewV7()), RoomID: room.ID, LandlordID: room.LandlordID, TenantID: tenant, Status: status,
		PriceVND: room.PriceVND, OccupantCount: 1, StartsOn: domain.Today(f.now, time.UTC), ConfirmedAt: &confirmed,
	}
	f.contracts[c.ID] = c
	f.order = append(f.order, c.ID)
	return c
}

func (f *fakeStore) detailOf(c *domain.Contract) domain.Detail {
	r := f.rooms[c.RoomID]
	d := domain.Detail{
		Contract: *c, Billing: f.billing[c.ID], CreatedAt: f.now, UpdatedAt: f.now,
		Room:     domain.RoomRef{ID: r.ID, Name: r.Name, Status: r.Status, PriceVND: r.PriceVND, MinPeriod: r.MinPeriod, PeriodUnit: r.PeriodUnit},
		Landlord: domain.Person{ID: c.LandlordID, FullName: f.names[c.LandlordID]},
	}
	if c.TenantID != nil {
		d.Tenant = &domain.Person{ID: *c.TenantID, FullName: f.names[*c.TenantID]}
	}
	return d
}

func (f *fakeStore) sees(v domain.Viewer, c *domain.Contract) bool {
	if f.deleted[c.ID] {
		return false
	}
	if v.AsLandlord {
		return c.LandlordID == v.UserID && c.ConfirmedAt != nil && f.rooms[c.RoomID].Live
	}
	return c.HeldBy(v.UserID)
}

// ContractRepository

func (f *fakeStore) Search(_ context.Context, v domain.Viewer, q domain.Query) ([]domain.Detail, int, error) {
	var out []domain.Detail
	for _, id := range f.order {
		c := f.contracts[id]
		if !f.sees(v, c) {
			continue
		}
		if len(q.Statuses) > 0 && !containsStatus(q.Statuses, c.Status) {
			continue
		}
		if q.CancellationStatus != nil && (c.Cancellation.Status == nil || *c.Cancellation.Status != *q.CancellationStatus) {
			continue
		}
		if q.PaymentState != nil && !stateOf(f.billing[id], *q.PaymentState) {
			continue
		}
		if q.HasUnpaidInvoice && f.billing[id].UnpaidInvoices == 0 {
			continue
		}
		out = append(out, f.detailOf(c))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayOrder < out[j].DisplayOrder })
	total := len(out)
	if q.Limit > 0 && q.Offset < len(out) {
		out = out[q.Offset:min(len(out), q.Offset+q.Limit)]
	}
	return out, total, nil
}

func containsStatus(list []domain.Status, s domain.Status) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (f *fakeStore) Get(_ context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	c, ok := f.contracts[id]
	if !ok || !f.sees(v, c) {
		return domain.Detail{}, shared.ErrNotFound
	}
	return f.detailOf(c), nil
}

func (f *fakeStore) ForUpdate(_ context.Context, id uuid.UUID) (domain.Contract, error) {
	c, ok := f.contracts[id]
	if !ok || f.deleted[id] {
		return domain.Contract{}, shared.ErrNotFound
	}
	return *c, nil
}

func (f *fakeStore) Insert(_ context.Context, c domain.Contract) (uuid.UUID, error) {
	c.ID = uuid.Must(uuid.NewV7())
	f.contracts[c.ID] = &c
	f.order = append(f.order, c.ID)
	return c.ID, nil
}

func (f *fakeStore) Save(_ context.Context, c domain.Contract) error {
	f.contracts[c.ID] = &c
	return nil
}

func (f *fakeStore) SoftDelete(_ context.Context, id uuid.UUID) error {
	f.deleted[id] = true
	return nil
}

func (f *fakeStore) Occupancy(_ context.Context, roomID uuid.UUID) (domain.Occupancy, error) {
	var o domain.Occupancy
	for id, c := range f.contracts {
		if c.RoomID == roomID && c.Status == domain.StatusActive && !f.deleted[id] {
			o.Running++
			if c.TenantID != nil {
				o.WithTenant++
			}
		}
	}
	return o, nil
}

func (f *fakeStore) RegistrationOpen(_ context.Context, roomID, tenantID uuid.UUID) (bool, error) {
	for id, c := range f.contracts {
		if c.RoomID == roomID && c.HeldBy(tenantID) && (c.Status == domain.StatusPending || c.Status == domain.StatusActive) && !f.deleted[id] {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) RejectOtherPending(_ context.Context, roomID, except uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for id, c := range f.contracts {
		if c.RoomID == roomID && id != except && c.Status == domain.StatusPending && !f.deleted[id] {
			c.Status = domain.StatusRejected
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeStore) CloseBlank(_ context.Context, roomIDs []uuid.UUID, today time.Time) ([]domain.ClosedBlank, error) {
	var out []domain.ClosedBlank
	for id, c := range f.contracts {
		if !containsID(roomIDs, c.RoomID) || c.Status != domain.StatusActive || c.TenantID != nil || f.deleted[id] {
			continue
		}
		end := domain.ClosingDate(c.EndsOn, today)
		c.Status, c.EndsOn = domain.StatusEnded, &end
		out = append(out, domain.ClosedBlank{ContractID: id, RoomID: c.RoomID, LandlordID: c.LandlordID})
	}
	return out, nil
}

func containsID(list []uuid.UUID, id uuid.UUID) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func (f *fakeStore) TopOrder(_ context.Context, landlordID uuid.UUID) (int, error) {
	smallest := 0
	for id, c := range f.contracts {
		if c.LandlordID == landlordID && !f.deleted[id] && c.DisplayOrder < smallest {
			smallest = c.DisplayOrder
		}
	}
	return smallest - 1, nil
}

func (f *fakeStore) Order(_ context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error) {
	var out []domain.OrderedID
	for _, id := range f.order {
		if c := f.contracts[id]; f.sees(domain.Viewer{UserID: landlordID, AsLandlord: true}, c) {
			out = append(out, domain.OrderedID{ID: id, Order: c.DisplayOrder})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func (f *fakeStore) SetOrder(_ context.Context, _ uuid.UUID, moves []domain.Position) error {
	for _, m := range moves {
		f.contracts[m.ID].DisplayOrder = m.Order
	}
	return nil
}

func (f *fakeStore) CountRunning(_ context.Context, landlordID uuid.UUID) (int, error) {
	n := 0
	for id, c := range f.contracts {
		if c.LandlordID == landlordID && c.Status == domain.StatusActive && c.TenantID != nil && !f.deleted[id] {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CountPending(_ context.Context, tenantID uuid.UUID) (int, error) {
	n := 0
	for id, c := range f.contracts {
		if c.HeldBy(tenantID) && c.Status == domain.StatusPending && !f.deleted[id] {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) RunningTenantRooms(_ context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, id := range roomIDs {
		if o, _ := f.Occupancy(context.Background(), id); o.WithTenant > 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeStore) LockLandlord(context.Context, uuid.UUID) error { return nil }

func (f *fakeStore) StatusCounts(_ context.Context, tenantID uuid.UUID) (domain.StatusCounts, error) {
	out := domain.StatusCounts{}
	for id, c := range f.contracts {
		if c.HeldBy(tenantID) && !f.deleted[id] {
			out[c.Status]++
		}
	}
	return out, nil
}

// stateOf says whether a contract with this billing is in the state of money.
func stateOf(b domain.Billing, s domain.PaymentState) bool {
	switch s {
	case domain.PaymentDue:
		return b.DueCount > 0
	case domain.PaymentReview:
		return b.ReviewCount > 0
	}
	return b.DueCount == 0 && b.ReviewCount == 0
}

func (f *fakeStore) PaymentCounts(_ context.Context, tenantID uuid.UUID, statuses []domain.Status) (domain.PaymentCounts, error) {
	var out domain.PaymentCounts
	for id, c := range f.contracts {
		if !c.HeldBy(tenantID) || f.deleted[id] || !containsStatus(statuses, c.Status) {
			continue
		}
		b := f.billing[id]
		out.All++
		if b.DueCount > 0 {
			out.Due++
		}
		if b.ReviewCount > 0 {
			out.Review++
		}
		if b.DueCount == 0 && b.ReviewCount == 0 {
			out.Settled++
		}
	}
	return out, nil
}

func (f *fakeStore) RentalExtras(context.Context, uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error) {
	return []domain.ChargeLine{{Name: "Dien", Kind: "electricity", AmountVND: 3500, Unit: "per_kwh"}}, nil, nil
}

func (f *fakeStore) CountOfTenant(_ context.Context, landlordID, tenantID uuid.UUID) (int, error) {
	n := 0
	for id, c := range f.contracts {
		if c.LandlordID == landlordID && c.HeldBy(tenantID) && !f.deleted[id] {
			n++
		}
	}
	return n, nil
}

// RoomReader

func (f *fakeStore) Lock(_ context.Context, id uuid.UUID) (domain.RoomFacts, error) {
	r, ok := f.rooms[id]
	if !ok {
		return domain.RoomFacts{}, shared.ErrNotFound
	}
	return *r, nil
}

func (f *fakeStore) FindByName(_ context.Context, landlordID uuid.UUID, name string) (domain.RoomFacts, error) {
	for _, r := range f.rooms {
		if r.LandlordID == landlordID && r.Name == name {
			return *r, nil
		}
	}
	return domain.RoomFacts{}, shared.ErrNotFound
}

// RoomStatusSetter

func (f *fakeStore) SetStatus(_ context.Context, _, roomID uuid.UUID, status string) error {
	if f.setterErr != nil {
		return f.setterErr
	}
	f.rooms[roomID].Status = status
	f.statuses = append(f.statuses, f.rooms[roomID].Name+":"+status)
	return nil
}

// fakeTx runs the function and records rollbacks.
type fakeTx struct{ rollbacks *int }

func (f fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := fn(ctx); err != nil {
		if f.rollbacks != nil {
			*f.rollbacks++
		}
		return err
	}
	return nil
}

type recordingNotifier struct{ sent []Notification }

func (r *recordingNotifier) Notify(_ context.Context, n Notification) error {
	r.sent = append(r.sent, n)
	return nil
}

func (r *recordingNotifier) kinds() []string {
	var out []string
	for _, n := range r.sent {
		out = append(out, n.Kind+":"+n.Audience)
	}
	return out
}

type recordingAudit struct{ events []AuditEvent }

func (r *recordingAudit) Record(_ context.Context, e AuditEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recordingAudit) actions() []string {
	var out []string
	for _, e := range r.events {
		out = append(out, e.Action)
	}
	return out
}

type fixedPlans struct {
	UnlimitedPlans
	contracts *int
	pending   *int
}

func (p fixedPlans) MaxActiveContracts(context.Context, uuid.UUID) (*int, error) {
	return p.contracts, nil
}
func (p fixedPlans) MaxPendingRegistrations(context.Context, uuid.UUID) (*int, error) {
	return p.pending, nil
}

// The invitation ports: an in-memory repository over the same store, and recorders for the rest.

type fakeInvitations struct {
	store *fakeStore
	rows  map[uuid.UUID]*domain.Invitation
	order []uuid.UUID
	// lost lists the managers who no longer manage the room of what they wrote (their own offers are then hidden).
	lost map[uuid.UUID]bool
}

func newFakeInvitations(store *fakeStore) *fakeInvitations {
	return &fakeInvitations{store: store, rows: map[uuid.UUID]*domain.Invitation{}}
}

func (f *fakeInvitations) detail(inv *domain.Invitation) domain.InvitationDetail {
	d := domain.InvitationDetail{Invitation: *inv, LandlordName: f.store.names[inv.LandlordID]}
	if r, ok := f.store.rooms[inv.RoomID]; ok {
		d.RoomName = r.Name
	}
	return d
}

func (f *fakeInvitations) Insert(_ context.Context, inv domain.Invitation) (uuid.UUID, error) {
	inv.ID = uuid.Must(uuid.NewV7())
	inv.CreatedAt, inv.UpdatedAt = f.store.now, f.store.now
	f.rows[inv.ID] = &inv
	f.order = append(f.order, inv.ID)
	return inv.ID, nil
}

func (f *fakeInvitations) Get(_ context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error) {
	inv, ok := f.rows[id]
	if !ok || inv.LandlordID != landlordID {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	return f.detail(inv), nil
}

func (f *fakeInvitations) List(_ context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	var out []domain.InvitationDetail
	for _, id := range f.order {
		inv := f.rows[id]
		if inv.LandlordID == landlordID && (status == nil || inv.Status == *status) {
			out = append(out, f.detail(inv))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f *fakeInvitations) GetAuthored(_ context.Context, authorID, id uuid.UUID) (domain.InvitationDetail, error) {
	inv, ok := f.rows[id]
	if !ok || inv.CreatedByID == nil || *inv.CreatedByID != authorID || f.lost[authorID] {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	return f.detail(inv), nil
}

func (f *fakeInvitations) ListAuthored(_ context.Context, authorID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	var out []domain.InvitationDetail
	for _, id := range f.order {
		inv := f.rows[id]
		if inv.CreatedByID != nil && *inv.CreatedByID == authorID && !f.lost[authorID] && (status == nil || inv.Status == *status) {
			out = append(out, f.detail(inv))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f *fakeInvitations) RevokeAwaitingOfAuthor(_ context.Context, authorID uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	var out []domain.Invitation
	for _, id := range f.order {
		inv := f.rows[id]
		if inv.CreatedByID != nil && *inv.CreatedByID == authorID && inv.Status == domain.InvitationAwaitingApproval && inv.Revoke(at) == nil {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (f *fakeInvitations) ForUpdate(_ context.Context, id uuid.UUID) (domain.Invitation, error) {
	inv, ok := f.rows[id]
	if !ok {
		return domain.Invitation{}, shared.ErrNotFound
	}
	return *inv, nil
}

func (f *fakeInvitations) Save(_ context.Context, inv domain.Invitation) error {
	f.rows[inv.ID] = &inv
	return nil
}

func (f *fakeInvitations) FindByTokenHash(_ context.Context, hash string) (domain.Invitation, error) {
	for _, inv := range f.rows {
		if inv.TokenHash == hash {
			return *inv, nil
		}
	}
	return domain.Invitation{}, shared.ErrNotFound
}

func (f *fakeInvitations) FindForRecipient(_ context.Context, id, recipientID uuid.UUID) (domain.Invitation, error) {
	inv, ok := f.rows[id]
	if !ok || inv.RecipientID == nil || *inv.RecipientID != recipientID {
		return domain.Invitation{}, shared.ErrNotFound
	}
	return *inv, nil
}

func (f *fakeInvitations) offer(inv *domain.Invitation) domain.InvitationOffer {
	o := domain.InvitationOffer{Invitation: *inv, Landlord: domain.Person{ID: inv.LandlordID, FullName: f.store.names[inv.LandlordID]}}
	if r, ok := f.store.rooms[inv.RoomID]; ok {
		o.Room = domain.RoomRef{ID: r.ID, Name: r.Name, PriceVND: r.PriceVND}
	}
	return o
}

func (f *fakeInvitations) Offer(_ context.Context, id uuid.UUID) (domain.InvitationOffer, error) {
	inv, ok := f.rows[id]
	if !ok {
		return domain.InvitationOffer{}, shared.ErrNotFound
	}
	return f.offer(inv), nil
}

func (f *fakeInvitations) Inbox(_ context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error) {
	var out []domain.InvitationOffer
	for i := len(f.order) - 1; i >= 0; i-- {
		inv := f.rows[f.order[i]]
		if inv.RecipientID != nil && *inv.RecipientID == recipientID && inv.OpenAt(now) {
			out = append(out, f.offer(inv))
		}
	}
	total := len(out)
	if limit > 0 && offset < len(out) {
		out = out[offset:min(len(out), offset+limit)]
	}
	return out, total, nil
}

func (f *fakeInvitations) RevokeOpenOfRooms(_ context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	var out []domain.Invitation
	for _, id := range f.order {
		inv := f.rows[id]
		if containsID(roomIDs, inv.RoomID) && inv.Revoke(at) == nil {
			out = append(out, *inv)
		}
	}
	return out, nil
}

// fakeInvitees answers Resolve with what the test set, and remembers what it was asked.
type fakeInvitees struct {
	result  invitation.Addressee
	err     error
	noMail  bool
	mailErr error
	asked   []uuid.UUID
	// people are the accounts Person knows (the ones answering an invitation).
	people map[uuid.UUID]invitation.Person
}

func (f *fakeInvitees) Resolve(_ context.Context, _ uuid.UUID, id *uuid.UUID, _ *string) (invitation.Addressee, error) {
	if id != nil {
		f.asked = append(f.asked, *id)
	}
	return f.result, f.err
}

func (f *fakeInvitees) Person(_ context.Context, id uuid.UUID) (invitation.Person, error) {
	if p, ok := f.people[id]; ok {
		return p, nil
	}
	return invitation.Person{}, shared.ErrNotFound
}

func (f *fakeInvitees) MayMail(context.Context, string) (bool, error) { return !f.noMail, f.mailErr }

// fakeTokens makes tokens of 64 valid characters, one more each time.
type fakeTokens struct{ n int }

func (f *fakeTokens) New() (token, hash string, err error) {
	f.n++
	token = fmt.Sprintf("%064d", f.n)
	return token, f.Hash(token), nil
}

func (*fakeTokens) Hash(token string) string { return "hash:" + token }

// fakeSealer "encrypts" by prefixing, and refuses to open a copy that does not carry the prefix.
type fakeSealer struct{}

func (fakeSealer) Seal(plain string) (string, error) { return "sealed:" + plain, nil }

func (fakeSealer) Open(sealed string) (string, error) {
	plain, ok := strings.CutPrefix(sealed, "sealed:")
	if !ok {
		return "", errors.New("cannot open")
	}
	return plain, nil
}

type recordingMailer struct {
	sent []InvitationMail
	err  error
}

func (r *recordingMailer) SendInvitation(_ context.Context, m InvitationMail) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, m)
	return nil
}

// fakeManagers says what each (account, room) was given.
type fakeManagers map[[2]uuid.UUID]Grant

func (f fakeManagers) Grant(_ context.Context, userID, roomID uuid.UUID) (Grant, error) {
	return f[[2]uuid.UUID{userID, roomID}], nil
}
