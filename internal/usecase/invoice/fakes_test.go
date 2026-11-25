package invoice

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// The invoice use cases run here against hand-written fakes of the ports: no database. fakeStore is one in-memory world
// (contracts, charges, bills, readings) that implements the invoice repository, the contract reader and the charge
// reader, so a test reads like a story: contracts exist, bills change them.

type storedReading struct {
	seq int
	domain.Reading
}

type fakeStore struct {
	now       time.Time
	contracts map[uuid.UUID]*domain.ContractFacts
	order     []uuid.UUID // contract ids in creation order (the scan is by id order, which is this one)
	charges   map[uuid.UUID][]domain.Charge
	invoices  map[uuid.UUID]*domain.Invoice
	readings  []storedReading
	seq       int
	reminded  map[string]map[uuid.UUID]time.Time // kind -> bill -> day
	// hidden contracts are not confirmed yet, so a landlord cannot see them.
	hidden map[uuid.UUID]bool
	// grants are the rooms handed to managers: room -> manager -> grant.
	grants map[uuid.UUID]map[uuid.UUID]ManagerGrant
	// clashes is how many inserts fail with a number or transfer text that was taken at the same moment.
	clashes int
}

func newStore(now time.Time) *fakeStore {
	return &fakeStore{
		now: now, contracts: map[uuid.UUID]*domain.ContractFacts{}, charges: map[uuid.UUID][]domain.Charge{},
		invoices: map[uuid.UUID]*domain.Invoice{}, reminded: map[string]map[uuid.UUID]time.Time{}, hidden: map[uuid.UUID]bool{},
		grants: map[uuid.UUID]map[uuid.UUID]ManagerGrant{},
	}
}

func id() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// contract stores a running monthly contract of 3,000,000 VND that started `monthsAgo` months ago today.
func (f *fakeStore) contract(landlord uuid.UUID, tenant *uuid.UUID, room string, monthsAgo int) *domain.ContractFacts {
	today := domain.Today(f.now, time.UTC)
	c := &domain.ContractFacts{
		ID: id(), RoomID: id(), RoomName: room, LandlordID: landlord, LandlordName: "Landlord", TenantID: tenant, Status: "active",
		PriceVND: 3_000_000, OccupantCount: 1, StartsOn: domain.AddMonths(today, -monthsAgo),
	}
	if tenant != nil {
		name, email := "Tenant", "tenant@example.vn"
		c.TenantAccountName, c.TenantEmail = &name, &email
	}
	f.contracts[c.ID] = c
	f.order = append(f.order, c.ID)
	return c
}

// charge adds a charge to the room of the contract.
func (f *fakeStore) charge(c *domain.ContractFacts, unit, name string, amount int64) domain.Charge {
	ch := domain.Charge{ID: id(), Name: name, Kind: "other", Unit: unit, AmountVND: amount}
	f.charges[c.RoomID] = append(f.charges[c.RoomID], ch)
	return ch
}

func (f *fakeStore) sees(v domain.Viewer, c *domain.ContractFacts) bool {
	if v.AsManager {
		return f.grants[c.RoomID][v.UserID].Active && (c.TenantID == nil || *c.TenantID != v.UserID)
	}
	if v.AsLandlord {
		return c.LandlordID == v.UserID && !f.hidden[c.ID]
	}
	return c.TenantID != nil && *c.TenantID == v.UserID
}

func (f *fakeStore) sees2(v domain.Viewer, inv *domain.Invoice) bool {
	c := f.contracts[inv.ContractID]
	if !f.sees(v, c) {
		return false
	}
	return v.AsLandlord || v.AsManager || domain.VisibleToTenant(inv.Status, inv.Delivery)
}

func (f *fakeStore) detail(inv *domain.Invoice) domain.Detail {
	d := domain.Detail{Invoice: *inv, Contract: *f.contracts[inv.ContractID]}
	d.Items = slices.Clone(inv.Items)
	d.Readings = nil
	for _, r := range f.readings {
		if r.InvoiceID == inv.ID {
			d.Readings = append(d.Readings, r.Reading)
		}
	}
	return d
}

// InvoiceRepository

func (f *fakeStore) Get(_ context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	inv, ok := f.invoices[id]
	if !ok || !f.sees2(v, inv) {
		return domain.Detail{}, shared.ErrNotFound
	}
	return f.detail(inv), nil
}

func (f *fakeStore) ForUpdate(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	return f.Get(ctx, v, id)
}

func (f *fakeStore) List(_ context.Context, v domain.Viewer, contractID uuid.UUID, limit, offset int) ([]domain.Detail, int, error) {
	var all []*domain.Invoice
	for _, inv := range f.invoices {
		if inv.ContractID == contractID && f.sees2(v, inv) {
			all = append(all, inv)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].PeriodStart.After(all[j].PeriodStart) })
	total := len(all)
	var out []domain.Detail
	for i := offset; i < min(total, offset+limit); i++ {
		out = append(out, f.detail(all[i]))
	}
	return out, total, nil
}

func (f *fakeStore) FindByShareHash(_ context.Context, hash string) (domain.Detail, error) {
	for _, inv := range f.invoices {
		if inv.Share.TokenHash == hash && hash != "" {
			return f.detail(inv), nil
		}
	}
	return domain.Detail{}, shared.ErrNotFound
}

func (f *fakeStore) Insert(_ context.Context, inv domain.Invoice) (uuid.UUID, error) {
	if f.clashes > 0 {
		f.clashes--
		return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: "invoices_payment_code_key"}
	}
	for _, o := range f.invoices {
		switch {
		case o.ContractID == inv.ContractID && o.PeriodStart.Equal(inv.PeriodStart):
			return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: ConstraintPeriod}
		case o.Number == inv.Number:
			return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: "invoices_invoice_number_key"}
		case o.PaymentCode == inv.PaymentCode:
			return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: "invoices_payment_code_key"}
		}
	}
	inv.ID = id()
	inv.CreatedAt, inv.UpdatedAt = f.now, f.now
	readings := inv.Readings
	inv.Items, inv.Readings = slices.Clone(inv.Items), nil
	f.invoices[inv.ID] = &inv
	f.writeReadings(inv.ID, readings)
	return inv.ID, nil
}

func (f *fakeStore) writeReadings(invoiceID uuid.UUID, readings []domain.Reading) {
	keep := map[uuid.UUID]bool{}
	for _, r := range readings {
		keep[r.ChargeID] = true
	}
	var kept []storedReading
	for _, s := range f.readings {
		if s.InvoiceID != invoiceID || keep[s.ChargeID] {
			kept = append(kept, s)
		}
	}
	f.readings = kept
	for _, r := range readings {
		r.InvoiceID = invoiceID
		replaced := false
		for i, s := range f.readings {
			if s.InvoiceID == invoiceID && s.ChargeID == r.ChargeID {
				// A meter read again keeps its place in the history.
				r.ID, r.ReadOn = s.ID, s.ReadOn
				f.readings[i].Reading = r
				replaced = true
			}
		}
		if !replaced {
			f.seq++
			r.ID = id()
			f.readings = append(f.readings, storedReading{seq: f.seq, Reading: r})
		}
	}
}

func (f *fakeStore) Replace(_ context.Context, inv domain.Invoice) error {
	cur := f.invoices[inv.ID]
	if !cur.DueAt.Equal(inv.DueAt) {
		for _, m := range f.reminded {
			delete(m, inv.ID)
		}
	}
	cur.Name, cur.PaymentLabel, cur.PaymentCode, cur.TotalVND, cur.Note, cur.DueAt = inv.Name, inv.PaymentLabel, inv.PaymentCode, inv.TotalVND, inv.Note, inv.DueAt
	cur.Items = slices.Clone(inv.Items)
	f.writeReadings(inv.ID, inv.Readings)
	return nil
}

func (f *fakeStore) SaveState(_ context.Context, inv domain.Invoice) error {
	cur := f.invoices[inv.ID]
	cur.Status, cur.SentAt, cur.SentVia, cur.SentByID, cur.PaidAt, cur.Delivery = inv.Status, inv.SentAt, inv.SentVia, inv.SentByID, inv.PaidAt, inv.Delivery
	return nil
}

func (f *fakeStore) SaveShare(_ context.Context, id uuid.UUID, s domain.Share) error {
	f.invoices[id].Share = s
	return nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.invoices, id)
	f.readings = slices.DeleteFunc(f.readings, func(s storedReading) bool { return s.InvoiceID == id })
	return nil
}

func (f *fakeStore) BilledPeriods(_ context.Context, contractID uuid.UUID) ([]domain.BilledPeriod, error) {
	var out []domain.BilledPeriod
	for _, inv := range f.invoices {
		if inv.ContractID == contractID {
			out = append(out, domain.BilledPeriod{InvoiceID: inv.ID, PeriodStart: inv.PeriodStart, Number: inv.Number, Name: inv.Name, SentAt: inv.SentAt, SentVia: inv.SentVia})
		}
	}
	return out, nil
}

func (f *fakeStore) CountOfContract(ctx context.Context, contractID uuid.UUID) (int, error) {
	b, err := f.BilledPeriods(ctx, contractID)
	return len(b), err
}

func (f *fakeStore) MonthCounts(_ context.Context, landlordID uuid.UUID, from, to time.Time) (map[string]int, error) {
	out := map[string]int{}
	for _, inv := range f.invoices {
		if f.contracts[inv.ContractID].LandlordID == landlordID && !inv.PeriodStart.Before(from) && !inv.PeriodStart.After(to) {
			out[domain.MonthKey(inv.PeriodStart)]++
		}
	}
	return out, nil
}

func (f *fakeStore) NumberTaken(_ context.Context, number string) (bool, error) {
	for _, inv := range f.invoices {
		if inv.Number == number {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CodeTaken(_ context.Context, code string, except uuid.UUID) (bool, error) {
	for _, inv := range f.invoices {
		if inv.PaymentCode == code && inv.ID != except {
			return true, nil
		}
	}
	return false, nil
}

// LastReadings follows the SQL: the newest reading of each meter by (day, order); a bill being corrected counts from what
// came before it.
func (f *fakeStore) LastReadings(_ context.Context, chargeIDs []uuid.UUID, editing *uuid.UUID) (map[uuid.UUID]float64, error) {
	out := map[uuid.UUID]float64{}
	best := map[uuid.UUID]storedReading{}
	for _, s := range f.readings {
		if !slices.Contains(chargeIDs, s.ChargeID) {
			continue
		}
		if editing != nil {
			if s.InvoiceID == *editing {
				continue
			}
			own := false
			for _, o := range f.readings {
				if o.InvoiceID == *editing && o.ChargeID == s.ChargeID && o.seq <= s.seq {
					own = true
				}
			}
			if own {
				continue
			}
		}
		b, ok := best[s.ChargeID]
		if !ok || s.ReadOn.After(b.ReadOn) || (s.ReadOn.Equal(b.ReadOn) && s.seq > b.seq) {
			best[s.ChargeID] = s
		}
	}
	for c, s := range best {
		out[c] = s.Value
	}
	return out, nil
}

func (f *fakeStore) ReadingHistory(_ context.Context, roomID uuid.UUID, chargeID *uuid.UUID, limit, offset int) ([]domain.Reading, int, error) {
	var rows []storedReading
	for _, s := range f.readings {
		for _, c := range f.charges[roomID] {
			if c.ID == s.ChargeID && (chargeID == nil || *chargeID == c.ID) {
				s.ChargeName = c.Name
				rows = append(rows, s)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].seq > rows[j].seq })
	var out []domain.Reading
	for i := offset; i < min(len(rows), offset+limit); i++ {
		out = append(out, rows[i].Reading)
	}
	return out, len(rows), nil
}

func (f *fakeStore) Due(_ context.Context, soonFrom, soonTo, overdueFrom, overdueTo time.Time, after uuid.UUID, limit int) ([]domain.Detail, error) {
	var ids []uuid.UUID
	for id, inv := range f.invoices {
		_, soonDone := f.reminded["soon"][id]
		_, overdueDone := f.reminded["overdue"][id]
		soon := !inv.DueAt.Before(soonFrom) && !inv.DueAt.After(soonTo) && !soonDone
		over := !inv.DueAt.Before(overdueFrom) && !inv.DueAt.After(overdueTo) && !overdueDone
		if inv.Status == domain.StatusSent && (soon || over) && id.String() > after.String() {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	var out []domain.Detail
	for _, id := range ids[:min(len(ids), limit)] {
		out = append(out, f.detail(f.invoices[id]))
	}
	return out, nil
}

func (f *fakeStore) MarkReminded(_ context.Context, kind domain.Reminder, ids []uuid.UUID, on time.Time) error {
	name := "soon"
	if kind == domain.ReminderOverdue {
		name = "overdue"
	}
	if f.reminded[name] == nil {
		f.reminded[name] = map[uuid.UUID]time.Time{}
	}
	for _, id := range ids {
		f.reminded[name][id] = on
	}
	return nil
}

// ContractReader

func (f *fakeStore) contractGet(v domain.Viewer, id uuid.UUID) (domain.ContractFacts, error) {
	c, ok := f.contracts[id]
	if !ok || !f.sees(v, c) {
		return domain.ContractFacts{}, shared.ErrNotFound
	}
	return *c, nil
}

type fakeContracts struct{ *fakeStore }

func (f fakeContracts) Get(_ context.Context, v domain.Viewer, id uuid.UUID) (domain.ContractFacts, error) {
	return f.contractGet(v, id)
}

func (f fakeContracts) Lock(_ context.Context, id uuid.UUID) (domain.ContractFacts, error) {
	c, ok := f.contracts[id]
	if !ok {
		return domain.ContractFacts{}, shared.ErrNotFound
	}
	return *c, nil
}

func (f fakeContracts) Billable(_ context.Context, after uuid.UUID, limit int) ([]domain.ContractFacts, error) {
	var out []domain.ContractFacts
	for _, cid := range f.order {
		c := f.contracts[cid]
		if c.Running() && !f.hidden[cid] && cid.String() > after.String() {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b domain.ContractFacts) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out[:min(len(out), limit)], nil
}

func (f fakeContracts) FindByRoomName(_ context.Context, landlordID uuid.UUID, room string) (domain.ContractFacts, error) {
	for i := len(f.order) - 1; i >= 0; i-- {
		c := f.contracts[f.order[i]]
		if c.LandlordID == landlordID && strings.EqualFold(c.RoomName, room) {
			return *c, nil
		}
	}
	return domain.ContractFacts{}, shared.ErrNotFound
}

// ChargeReader

type fakeCharges struct{ *fakeStore }

func (f fakeCharges) ChargesOf(_ context.Context, roomID uuid.UUID) ([]domain.Charge, error) {
	return slices.Clone(f.charges[roomID]), nil
}

func (f fakeCharges) RoomOf(_ context.Context, landlordID, roomID uuid.UUID) (string, error) {
	for _, c := range f.contracts {
		if c.RoomID == roomID && c.LandlordID == landlordID {
			return c.RoomName, nil
		}
	}
	return "", shared.ErrNotFound
}

// The other ports.

type fakeLedger struct {
	totals  map[uuid.UUID]domain.Totals
	settled []Settlement
	voided  []uuid.UUID
	purged  []uuid.UUID
}

func (l *fakeLedger) Totals(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	out := map[uuid.UUID]domain.Totals{}
	for _, id := range ids {
		out[id] = l.totals[id]
	}
	return out, nil
}

func (l *fakeLedger) Settle(_ context.Context, s Settlement) error {
	l.settled = append(l.settled, s)
	t := l.totals[s.InvoiceID]
	t.PaidVND += s.AmountVND
	l.totals[s.InvoiceID] = t
	return nil
}

func (l *fakeLedger) Void(_ context.Context, id uuid.UUID) error {
	l.voided = append(l.voided, id)
	l.totals[id] = domain.Totals{}
	return nil
}

func (l *fakeLedger) Purge(_ context.Context, id uuid.UUID) error {
	l.purged = append(l.purged, id)
	return nil
}

type recorder struct {
	notes   []Notification
	events  []AuditEvent
	mails   []InvoiceMail
	failMsg bool
}

func (r *recorder) Notify(_ context.Context, n Notification) error {
	r.notes = append(r.notes, n)
	if r.failMsg {
		return fmt.Errorf("notify down")
	}
	return nil
}

func (r *recorder) Record(_ context.Context, e AuditEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) SendInvoice(_ context.Context, m InvoiceMail) error {
	r.mails = append(r.mails, m)
	if r.failMsg {
		return fmt.Errorf("mail down")
	}
	return nil
}

func (r *recorder) actions() []string {
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.Action
	}
	return out
}

func (r *recorder) kinds() []string {
	out := make([]string, len(r.notes))
	for i, n := range r.notes {
		out[i] = n.Kind
	}
	return out
}

type fakePlans map[uuid.UUID]bool

func (p fakePlans) AutoInvoicing(_ context.Context, id uuid.UUID) (bool, error) { return p[id], nil }

// fakeTokens makes numbered tokens of 64 characters; the hash is the token reversed, which is enough to tell them apart.
type fakeTokens struct{ n int }

func (t *fakeTokens) New() (string, string, error) {
	t.n++
	token := fmt.Sprintf("%064d", t.n)
	return token, t.Hash(token), nil
}

func (t *fakeTokens) Hash(token string) string {
	r := []byte(token)
	slices.Reverse(r)
	return "h" + string(r[:63])
}

type fakeSealer struct{ broken bool }

func (s *fakeSealer) Seal(plain string) (string, error) { return "sealed:" + plain, nil }

func (s *fakeSealer) Open(sealed string) (string, error) {
	if s.broken || !strings.HasPrefix(sealed, "sealed:") {
		return "", fmt.Errorf("cannot open")
	}
	return strings.TrimPrefix(sealed, "sealed:"), nil
}

// seqRandom draws a counter written in the alphabet, so numbers and tails are predictable and never repeat (unless fixed).
type seqRandom struct {
	calls int
	// fixed makes every draw the same.
	fixed bool
}

func (r *seqRandom) Pick(alphabet string, n int) (string, error) {
	r.calls++
	k := r.calls
	if r.fixed {
		k = 0
	}
	// The call count written in the alphabet's base: every call draws something different (until the space runs out).
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = alphabet[k%len(alphabet)]
		k /= len(alphabet)
	}
	return string(out), nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

type fakeTx struct{}

func (t *fakeTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// world wires everything.
type world struct {
	store    *fakeStore
	ledger   *fakeLedger
	rec      *recorder
	plans    fakePlans
	tokens   *fakeTokens
	sealer   *fakeSealer
	random   *seqRandom
	clock    *fakeClock
	deps     Deps
	opts     Options
	invoices *Invoices
	public   *Public
	auto     *Automation
	acting   *fakeActing
	landlord uuid.UUID
	tenant   uuid.UUID
}

// 2026-11-23 10:00 in Vietnam.
var testNow = time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)

func newWorld() *world {
	w := &world{
		store: newStore(testNow), ledger: &fakeLedger{totals: map[uuid.UUID]domain.Totals{}}, rec: &recorder{}, plans: fakePlans{},
		tokens: &fakeTokens{}, sealer: &fakeSealer{}, random: &seqRandom{}, clock: &fakeClock{t: testNow},
		landlord: id(), tenant: id(), acting: &fakeActing{},
	}
	w.opts = Options{Location: time.UTC, WebURL: "https://app.example.vn", ShareTTL: 90 * 24 * time.Hour}
	w.deps = Deps{
		Tx: &fakeTx{}, Invoices: w.store, Contracts: fakeContracts{w.store}, Charges: fakeCharges{w.store}, Ledger: w.ledger,
		Notifier: w.rec, Audit: w.rec, Mailer: w.rec, Plans: w.plans, Tokens: w.tokens, Sealer: w.sealer, Random: w.random,
		Clock: w.clock,
	}
	w.rebuild()
	return w
}

func (w *world) rebuild() {
	w.deps.Acting, w.deps.Managers = w.acting, fakeManagers{w.store}
	w.invoices, w.public, w.auto = NewInvoices(w.deps, w.opts), NewPublic(w.deps, w.opts), NewAutomation(w.deps, w.opts)
}

func (w *world) landlordView() domain.Viewer {
	return domain.Viewer{UserID: w.landlord, AsLandlord: true}
}
func (w *world) tenantView() domain.Viewer { return domain.Viewer{UserID: w.tenant} }

// fakeActing says whether the request acts as a manager.
type fakeActing struct{ on bool }

func (a *fakeActing) AsManager(context.Context) bool { return a.on }

// fakeManagers reads the grants kept in the store (room -> manager -> grant).
type fakeManagers struct{ *fakeStore }

func (f fakeManagers) Grants(_ context.Context, managerID uuid.UUID, rooms []uuid.UUID) (map[uuid.UUID]ManagerGrant, error) {
	out := map[uuid.UUID]ManagerGrant{}
	for _, r := range rooms {
		if g, ok := f.grants[r][managerID]; ok {
			out[r] = g
		}
	}
	return out, nil
}
