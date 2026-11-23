//go:build integration

package invoicepg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

type world struct {
	pool     *pgxpool.Pool
	repo     *InvoiceRepository
	contract *ContractReader
	charges  *ChargeReader
	tx       *postgres.TxManager
	landlord uuid.UUID
	other    uuid.UUID
	tenant   uuid.UUID
	room     uuid.UUID
	cid      uuid.UUID // a running contract of the landlord with the tenant
	power    uuid.UUID // an electricity charge of the room
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	w := &world{pool: pool, repo: NewInvoiceRepository(pool), contract: NewContractReader(pool), charges: NewChargeReader(pool), tx: postgres.NewTxManager(pool)}
	w.landlord, w.other, w.tenant = user("l@example.vn", "Landlord"), user("o@example.vn", "Other"), user("t@example.vn", "Tenant")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order) VALUES ($1, 'P101', 'x', 3000000, 1) RETURNING id`, w.landlord).Scan(&w.room))
	must(pool.QueryRow(ctx, `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'active', 3000000, '2026-09-23', now(), 1) RETURNING id`, w.room, w.landlord, w.tenant).Scan(&w.cid))
	must(pool.QueryRow(ctx, `INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Electricity', 'electricity', 3500, 'per_kwh', 1) RETURNING id`, w.room).Scan(&w.power))
	return w
}

func day(s string) time.Time {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (w *world) bill(t *testing.T, start string, status domain.Status, delivery domain.Delivery, number string, readings ...domain.Reading) domain.Invoice {
	t.Helper()
	now := time.Now()
	inv := domain.Invoice{
		ContractID: w.cid, Number: number, Name: "Bill " + number, PaymentLabel: "P101", PaymentCode: "P101 " + number[len(number)-5:],
		PeriodStart: day(start), PeriodEnd: day(start).AddDate(0, 1, -1), TotalVND: 3_350_000, DueAt: now.Add(72 * time.Hour), Status: status,
		Delivery: delivery, CreatedByID: &w.landlord,
		Items: []domain.Item{
			{Kind: "rent", Description: "Rent", Quantity: 1, UnitPriceVND: 3_000_000, AmountVND: 3_000_000, DisplayOrder: 1},
			{Kind: "electricity", Description: "Electricity (1000 → 1100)", Quantity: 100, UnitPriceVND: 3500, AmountVND: 350_000, DisplayOrder: 2},
		},
		Readings: readings,
	}
	if status != domain.StatusDraft {
		via := domain.SourceManual
		inv.SentAt, inv.SentVia = &now, &via
	}
	if status == domain.StatusPaid {
		inv.PaidAt = &now
	}
	id, err := w.repo.Insert(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	inv.ID = id
	return inv
}

func (w *world) reading(value, used float64, on string) domain.Reading {
	return domain.Reading{ChargeID: w.power, Value: value, Consumption: used, UnitPriceVND: 3500, ReadOn: day(on)}
}

func TestInsertAndGetRoundTripAWholeBillForTheRightViewersOnly(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	inv := w.bill(t, "2026-09-23", domain.StatusDraft, domain.DeliveryApp, "INV-202609-AAAAAA", w.reading(1100, 100, "2026-09-25"))
	landlord := domain.Viewer{UserID: w.landlord, AsLandlord: true}

	got, err := w.repo.Get(ctx, landlord, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Number != inv.Number || got.TotalVND != 3_350_000 || got.Status != domain.StatusDraft || len(got.Items) != 2 || got.Items[1].Quantity != 100 ||
		len(got.Readings) != 1 || got.Readings[0].Value != 1100 || got.Readings[0].ChargeName != "Electricity" || got.Readings[0].Previous() != 1000 {
		t.Fatalf("round trip: %+v", got)
	}
	if got.Contract.RoomName != "P101" || got.Contract.LandlordID != w.landlord || got.Contract.TenantID == nil || got.Contract.BilledByDay || got.CreatedBy == nil || got.CreatedBy.Name != "Landlord" {
		t.Errorf("contract facts and author: %+v %+v", got.Contract, got.CreatedBy)
	}

	// A draft is the landlord's: the tenant, another landlord and a stranger do not see it.
	for name, v := range map[string]domain.Viewer{
		"the tenant": {UserID: w.tenant}, "another landlord": {UserID: w.other, AsLandlord: true},
		"the landlord as a tenant": {UserID: w.landlord}, "a stranger": {UserID: uuid.New()},
	} {
		if _, err := w.repo.Get(ctx, v, inv.ID); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Sent by the app it reaches the tenant; sent by link it does not.
	sent := w.bill(t, "2026-10-23", domain.StatusSent, domain.DeliveryApp, "INV-202610-BBBBBB")
	byLink := w.bill(t, "2026-11-23", domain.StatusSent, domain.DeliveryLink, "INV-202611-CCCCCC")
	if _, err := w.repo.Get(ctx, domain.Viewer{UserID: w.tenant}, sent.ID); err != nil {
		t.Errorf("the tenant's bill: %v", err)
	}
	if _, err := w.repo.Get(ctx, domain.Viewer{UserID: w.tenant}, byLink.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a bill passed on by link is not in the app: %v", err)
	}
}

func TestListIsNewestFirstScopedPagedAndCountsWhatTheViewerSees(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.bill(t, "2026-09-23", domain.StatusPaid, domain.DeliveryApp, "INV-202609-AAAAAA")
	w.bill(t, "2026-10-23", domain.StatusDraft, domain.DeliveryApp, "INV-202610-BBBBBB")
	w.bill(t, "2026-11-23", domain.StatusSent, domain.DeliveryApp, "INV-202611-CCCCCC")

	landlord := domain.Viewer{UserID: w.landlord, AsLandlord: true}
	page, total, err := w.repo.List(ctx, landlord, w.cid, 2, 0)
	if err != nil || total != 3 || len(page) != 2 || page[0].PeriodStart.Format(domain.DateLayout) != "2026-11-23" || len(page[0].Items) != 2 {
		t.Fatalf("landlord page one: %d %d %v", total, len(page), err)
	}
	rest, _, _ := w.repo.List(ctx, landlord, w.cid, 2, 2)
	if len(rest) != 1 || rest[0].PeriodStart.Format(domain.DateLayout) != "2026-09-23" {
		t.Errorf("page two: %+v", rest)
	}
	tenant, total, err := w.repo.List(ctx, domain.Viewer{UserID: w.tenant}, w.cid, 20, 0)
	if err != nil || total != 2 || len(tenant) != 2 {
		t.Errorf("the tenant misses the draft: %d %d %v", total, len(tenant), err)
	}
	if other, total, _ := w.repo.List(ctx, domain.Viewer{UserID: w.other, AsLandlord: true}, w.cid, 20, 0); total != 0 || len(other) != 0 {
		t.Errorf("another landlord sees nothing: %d", total)
	}
}

func TestOneBillPerContractAndPeriodAndUniqueNumbersAndCodes(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	first := w.bill(t, "2026-09-23", domain.StatusDraft, domain.DeliveryApp, "INV-202609-AAAAAA")

	dup := first
	dup.Number, dup.PaymentCode = "INV-202609-ZZZZZZ", "P101 ZZZZZ"
	_, err := w.repo.Insert(ctx, dup)
	var ce *shared.ConstraintError
	if !errors.As(err, &ce) || ce.Constraint != "invoices_contract_period_key" {
		t.Errorf("a period is billed once: %v", err)
	}
	next := first
	next.PeriodStart, next.PeriodEnd, next.Number = day("2026-10-23"), day("2026-11-22"), first.Number
	if _, err := w.repo.Insert(ctx, next); !shared.IsUnique(err) {
		t.Errorf("the number is unique: %v", err)
	}
	next.Number, next.PaymentCode = "INV-202610-BBBBBB", first.PaymentCode
	if _, err := w.repo.Insert(ctx, next); !shared.IsUnique(err) {
		t.Errorf("the transfer text is unique: %v", err)
	}

	if taken, _ := w.repo.NumberTaken(ctx, first.Number); !taken {
		t.Error("number taken")
	}
	if taken, _ := w.repo.NumberTaken(ctx, "INV-NOPE"); taken {
		t.Error("number free")
	}
	if taken, _ := w.repo.CodeTaken(ctx, first.PaymentCode, uuid.Nil); !taken {
		t.Error("code taken")
	}
	if taken, _ := w.repo.CodeTaken(ctx, first.PaymentCode, first.ID); taken {
		t.Error("a bill does not clash with itself")
	}
	billed, err := w.repo.BilledPeriods(ctx, w.cid)
	if err != nil || len(billed) != 1 || billed[0].InvoiceID != first.ID {
		t.Errorf("billed periods: %+v %v", billed, err)
	}
	if n, _ := w.repo.CountOfContract(ctx, w.cid); n != 1 {
		t.Errorf("count: %d", n)
	}
}

func TestReplaceRebuildsLinesKeepsThePlaceOfAMeterReadAgainAndResetsReminders(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	inv := w.bill(t, "2026-09-23", domain.StatusSent, domain.DeliveryApp, "INV-202609-AAAAAA", w.reading(1100, 100, "2026-09-25"))
	var before uuid.UUID
	_ = w.pool.QueryRow(ctx, `SELECT id FROM meter_readings WHERE invoice_id = $1`, inv.ID).Scan(&before)
	if err := w.repo.MarkReminded(ctx, domain.ReminderDueSoon, []uuid.UUID{inv.ID}, day("2026-11-23")); err != nil {
		t.Fatal(err)
	}

	inv.TotalVND, inv.Name, inv.Note = 2_700_000, "Corrected", ptr("note")
	inv.Items = []domain.Item{{Kind: "rent", Description: "Rent", Quantity: 1, UnitPriceVND: 2_500_000, AmountVND: 2_500_000, DisplayOrder: 1}, {Kind: "other", Description: "Repair", Quantity: 1, UnitPriceVND: 200_000, AmountVND: 200_000, DisplayOrder: 2}}
	inv.Readings = []domain.Reading{w.reading(1200, 200, "2026-11-23")}
	if err := w.repo.Replace(ctx, inv); err != nil {
		t.Fatal(err)
	}
	got, err := w.repo.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, inv.ID)
	if err != nil || got.TotalVND != 2_700_000 || got.Name != "Corrected" || *got.Note != "note" || len(got.Items) != 2 || got.Items[1].Description != "Repair" {
		t.Fatalf("replaced: %+v %v", got, err)
	}
	if len(got.Readings) != 1 || got.Readings[0].ID != before || got.Readings[0].Value != 1200 || got.Readings[0].ReadOn.Format(domain.DateLayout) != "2026-09-25" {
		t.Errorf("the reading keeps its id and its day: %+v", got.Readings)
	}
	// Dropping the meter removes its reading.
	inv.Readings = nil
	if err := w.repo.Replace(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.repo.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, inv.ID); len(got.Readings) != 0 {
		t.Errorf("readings: %+v", got.Readings)
	}
	var marked *time.Time
	_ = w.pool.QueryRow(ctx, `SELECT due_soon_reminded_on FROM invoices WHERE id = $1`, inv.ID).Scan(&marked)
	if marked == nil {
		t.Error("the same due date keeps the reminder")
	}
	inv.DueAt = inv.DueAt.Add(48 * time.Hour)
	if err := w.repo.Replace(ctx, inv); err != nil {
		t.Fatal(err)
	}
	_ = w.pool.QueryRow(ctx, `SELECT due_soon_reminded_on FROM invoices WHERE id = $1`, inv.ID).Scan(&marked)
	if marked != nil {
		t.Error("a moved due date starts the reminders again")
	}
}

func ptr[T any](v T) *T { return &v }

func TestLastReadingsCountFromBeforeABillBeingCorrected(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	one := w.bill(t, "2026-09-23", domain.StatusSent, domain.DeliveryApp, "INV-202609-AAAAAA", w.reading(1100, 100, "2026-09-25"))
	two := w.bill(t, "2026-10-23", domain.StatusSent, domain.DeliveryApp, "INV-202610-BBBBBB", w.reading(1300, 200, "2026-10-25"))
	three := w.bill(t, "2026-11-23", domain.StatusDraft, domain.DeliveryApp, "INV-202611-CCCCCC", w.reading(1350, 50, "2026-11-25"))
	ids := []uuid.UUID{w.power}

	last, err := w.repo.LastReadings(ctx, ids, nil)
	if err != nil || last[w.power] != 1350 {
		t.Fatalf("the newest: %v %v", last, err)
	}
	for _, tc := range []struct {
		name    string
		editing uuid.UUID
		want    float64
		has     bool
	}{{"the first bill has nothing before it", one.ID, 0, false}, {"the second counts from the first", two.ID, 1100, true}, {"the last counts from the second", three.ID, 1300, true}} {
		got, err := w.repo.LastReadings(ctx, ids, &tc.editing)
		v, ok := got[w.power]
		if err != nil || ok != tc.has || v != tc.want {
			t.Errorf("%s: %v %v", tc.name, got, err)
		}
	}
	if none, _ := w.repo.LastReadings(ctx, nil, nil); len(none) != 0 {
		t.Errorf("no meters: %v", none)
	}

	// The history of the room, newest first, paged, and filtered by meter.
	page, total, err := w.repo.ReadingHistory(ctx, w.room, nil, 2, 0)
	if err != nil || total != 3 || len(page) != 2 || page[0].Value != 1350 {
		t.Fatalf("history: %d %d %v", total, len(page), err)
	}
	if page, total, _ := w.repo.ReadingHistory(ctx, w.room, &w.power, 10, 2); total != 3 || len(page) != 1 || page[0].Value != 1100 {
		t.Errorf("page two: %d %+v", total, page)
	}
	if _, total, _ := w.repo.ReadingHistory(ctx, w.room, ptr(uuid.New()), 10, 0); total != 0 {
		t.Errorf("another meter: %d", total)
	}

	// Deleting a bill takes its readings with it.
	if err := w.repo.Delete(ctx, three.ID); err != nil {
		t.Fatal(err)
	}
	if last, _ := w.repo.LastReadings(ctx, ids, nil); last[w.power] != 1300 {
		t.Errorf("after the delete: %v", last)
	}
	if n, _ := w.repo.CountOfContract(ctx, w.cid); n != 2 {
		t.Errorf("count: %d", n)
	}
}

func TestStateShareAndShareLookup(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	inv := w.bill(t, "2026-09-23", domain.StatusSent, domain.DeliveryApp, "INV-202609-AAAAAA")

	hash := "h" + string(make([]byte, 0)) + "0123456789012345678901234567890123456789012345678901234567890123"
	hash = hash[:64]
	expires := time.Now().Add(time.Hour)
	if err := w.repo.SaveShare(ctx, inv.ID, domain.Share{TokenHash: hash, Sealed: "sealed", ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	found, err := w.repo.FindByShareHash(ctx, hash)
	if err != nil || found.ID != inv.ID || found.Share.Sealed != "sealed" || !found.Share.Live(time.Now()) || found.Contract.LandlordName != "Landlord" {
		t.Fatalf("by hash: %+v %v", found, err)
	}
	if _, err := w.repo.FindByShareHash(ctx, "nope"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	if err := w.repo.SaveShare(ctx, inv.ID, domain.Share{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.repo.FindByShareHash(ctx, hash); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a withdrawn link: %v", err)
	}

	paid := time.Now()
	inv.Status, inv.PaidAt = domain.StatusPaid, &paid
	if err := w.repo.SaveState(ctx, inv); err != nil {
		t.Fatal(err)
	}
	got, _ := w.repo.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, inv.ID)
	if got.Status != domain.StatusPaid || got.PaidAt == nil {
		t.Errorf("state: %+v", got.Invoice)
	}
	// The database refuses a state that contradicts itself (paid without a time).
	inv.PaidAt = nil
	if err := w.repo.SaveState(ctx, inv); err == nil {
		t.Error("paid without paid_at must be refused")
	}
}

func TestMonthCountsDueWindowsAndReminderMarks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.bill(t, "2026-09-23", domain.StatusPaid, domain.DeliveryApp, "INV-202609-AAAAAA")
	soon := w.bill(t, "2026-10-23", domain.StatusSent, domain.DeliveryApp, "INV-202610-BBBBBB")
	w.bill(t, "2026-11-23", domain.StatusDraft, domain.DeliveryApp, "INV-202611-CCCCCC")

	counts, err := w.repo.MonthCounts(ctx, w.landlord, day("2026-09-01"), day("2026-10-31"))
	if err != nil || counts["2026-09"] != 1 || counts["2026-10"] != 1 || len(counts) != 2 {
		t.Errorf("counts: %v %v", counts, err)
	}
	if other, _ := w.repo.MonthCounts(ctx, w.other, day("2026-09-01"), day("2026-12-31")); len(other) != 0 {
		t.Errorf("another landlord: %v", other)
	}

	from := time.Now().Add(71 * time.Hour)
	to := time.Now().Add(73 * time.Hour)
	due, err := w.repo.Due(ctx, from, to, to.Add(24*time.Hour), to.Add(48*time.Hour), uuid.Nil, 10)
	if err != nil || len(due) != 1 || due[0].ID != soon.ID || due[0].Contract.RoomName != "P101" {
		t.Fatalf("only the sent one in the window: %+v %v", due, err)
	}
	if err := w.repo.MarkReminded(ctx, domain.ReminderDueSoon, []uuid.UUID{soon.ID}, day("2026-11-23")); err != nil {
		t.Fatal(err)
	}
	if due, _ := w.repo.Due(ctx, from, to, to.Add(24*time.Hour), to.Add(48*time.Hour), uuid.Nil, 10); len(due) != 0 {
		t.Errorf("reminded once: %d", len(due))
	}
	if err := w.repo.MarkReminded(ctx, domain.ReminderOverdue, nil, day("2026-11-23")); err != nil {
		t.Errorf("nothing to mark: %v", err)
	}
}

func TestContractAndChargeReaders(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	landlord := domain.Viewer{UserID: w.landlord, AsLandlord: true}

	f, err := w.contract.Get(ctx, landlord, w.cid)
	if err != nil || f.RoomName != "P101" || !f.Running() || f.PriceVND != 3_000_000 || f.OccupantCount != 1 || f.EndsOn != nil || f.TenantEmail == nil {
		t.Fatalf("facts: %+v %v", f, err)
	}
	if _, err := w.contract.Get(ctx, domain.Viewer{UserID: w.other, AsLandlord: true}, w.cid); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := w.contract.Get(ctx, domain.Viewer{UserID: w.tenant}, w.cid); err != nil {
		t.Errorf("the tenant sees their contract: %v", err)
	}

	err = w.tx.WithinTx(ctx, func(ctx context.Context) error {
		locked, err := w.contract.Lock(ctx, w.cid)
		if err != nil || locked.ID != w.cid {
			t.Errorf("lock: %+v %v", locked, err)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.contract.Lock(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("lock of a missing contract: %v", err)
	}
	if byName, err := w.contract.FindByRoomName(ctx, w.landlord, "p101"); err != nil || byName.ID != w.cid {
		t.Errorf("by name: %v", err)
	}
	if _, err := w.contract.FindByRoomName(ctx, w.other, "P101"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's room name: %v", err)
	}

	// The scan reads running contracts by id; an ended or a not yet confirmed one is left out.
	var ended uuid.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO contracts (room_id, landlord_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, 'ended', 1, '2026-01-01', now(), 2) RETURNING id`, w.room, w.landlord).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	billable, err := w.contract.Billable(ctx, uuid.Nil, 10)
	if err != nil || len(billable) != 1 || billable[0].ID != w.cid {
		t.Errorf("billable: %+v %v", billable, err)
	}
	if after, _ := w.contract.Billable(ctx, w.cid, 10); len(after) != 0 {
		t.Errorf("keyset: %+v", after)
	}

	charges, err := w.charges.ChargesOf(ctx, w.room)
	if err != nil || len(charges) != 1 || !charges[0].IsMetered() || charges[0].AmountVND != 3500 {
		t.Errorf("charges: %+v %v", charges, err)
	}
	if name, err := w.charges.RoomOf(ctx, w.landlord, w.room); err != nil || name != "P101" {
		t.Errorf("room of: %q %v", name, err)
	}
	if _, err := w.charges.RoomOf(ctx, w.other, w.room); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's room: %v", err)
	}
}

func TestTheSchemaRefusesContradictoryBills(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	inv := w.bill(t, "2026-09-23", domain.StatusDraft, domain.DeliveryApp, "INV-202609-AAAAAA")
	for name, stmt := range map[string]string{
		"a draft with a sending time":  `UPDATE invoices SET sent_at = now() WHERE id = $1`,
		"a label with accents":         `UPDATE invoices SET payment_label = 'Tiền phòng' WHERE id = $1`,
		"a label over 14 characters":   `UPDATE invoices SET payment_label = 'AAAAAAAAAAAAAAA' WHERE id = $1`,
		"a period that ends before it": `UPDATE invoices SET period_end = '2026-09-01' WHERE id = $1`,
		"an unknown status":            `UPDATE invoices SET status = 'void' WHERE id = $1`,
		"a link without an expiry":     `UPDATE invoices SET share_token_hash = repeat('a', 64) WHERE id = $1`,
		"a negative total":             `UPDATE invoices SET total_amount_vnd = -1 WHERE id = $1`,
	} {
		if _, err := w.pool.Exec(ctx, stmt, inv.ID); err == nil {
			t.Errorf("%s must be refused by the database", name)
		}
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO meter_readings (charge_id, invoice_id, read_on, reading_value, consumption, unit_price_vnd) VALUES ($1, $2, '2026-09-25', 1, 1, 1), ($1, $2, '2026-09-26', 2, 1, 1)`, w.power, inv.ID); err == nil {
		t.Error("one reading per meter and bill")
	}
	// A contract with bills cannot be removed from under them.
	if _, err := w.pool.Exec(ctx, `DELETE FROM contracts WHERE id = $1`, w.cid); err == nil {
		t.Error("a contract with bills must not be deleted")
	}
}
