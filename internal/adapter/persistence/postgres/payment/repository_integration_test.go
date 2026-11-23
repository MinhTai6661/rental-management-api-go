//go:build integration

package paymentpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

type world struct {
	pool     *pgxpool.Pool
	payments *PaymentRepository
	methods  *MethodRepository
	invoices *InvoiceReader
	landlord uuid.UUID
	other    uuid.UUID
	tenant   uuid.UUID
	room     uuid.UUID
	contract uuid.UUID
	bill     uuid.UUID // a sent bill of 1,000,000 VND of the tenant's contract
	n        int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		must(pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id))
		return id
	}
	w := &world{pool: pool, payments: NewPaymentRepository(pool), methods: NewMethodRepository(pool), invoices: NewInvoiceReader(pool)}
	w.landlord, w.other, w.tenant = user("l@example.vn", "Landlord"), user("o@example.vn", "Other"), user("t@example.vn", "Tenant")
	must(pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order) VALUES ($1, 'P101', 'x', 3000000, 1) RETURNING id`, w.landlord).Scan(&w.room))
	must(pool.QueryRow(ctx, `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'active', 3000000, '2026-09-23', now(), 1) RETURNING id`, w.room, w.landlord, w.tenant).Scan(&w.contract))
	w.bill = w.newBill(t, "sent", "app")
	return w
}

// newBill inserts a bill of 1,000,000 VND on the next month of the contract.
func (w *world) newBill(t *testing.T, status, delivery string) uuid.UUID {
	t.Helper()
	w.n++
	start := time.Date(2026, 9+time.Month(w.n), 1, 0, 0, 0, 0, time.UTC)
	sent := "now()"
	if status == "draft" {
		sent = "NULL"
	}
	via := "'manual'"
	if status == "draft" {
		via = "NULL"
	}
	paid := "NULL"
	if status == "paid" {
		paid = "now()"
	}
	var id uuid.UUID
	err := w.pool.QueryRow(context.Background(), `INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end,
		total_amount_vnd, due_at, status, sent_at, sent_via, paid_at, delivery)
		VALUES ($1, $2, $3, 'HD', $4, $5, $6, 1000000, now() + interval '3 days', $7, `+sent+`, `+via+`, `+paid+`, $8) RETURNING id`,
		w.contract, "INV-"+start.Format("200601")+"-AAAAAA", "Bill "+start.Format("01/2006"), "HD CODE"+start.Format("01"), start, start.AddDate(0, 1, -1), status, delivery).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *world) pending(t *testing.T, bill uuid.UUID, amount int64, proofs ...string) uuid.UUID {
	t.Helper()
	note := "paid"
	id, err := w.payments.Insert(context.Background(), domain.NewPending(bill, w.tenant, domain.MethodManualProof, amount, &note, proofs))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInsertAndGetRoundTripAPaymentForTheRightViewersOnly(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := w.pending(t, w.bill, 400_000, "payments/2026/11/aa.webp", "payments/2026/11/bb.webp")

	got, err := w.payments.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.AmountVND != 400_000 || got.Status != domain.StatusPending || got.Method != domain.MethodManualProof || *got.Note != "paid" ||
		len(got.ProofKeys) != 2 || got.ProofKeys[1] != "payments/2026/11/bb.webp" || got.ReviewedAt != nil || got.Recorded() {
		t.Fatalf("round trip: %+v", got.Payment)
	}
	if got.Payer == nil || got.Payer.Name != "Tenant" || *got.Payer.Email != "t@example.vn" || got.Invoice.RoomName != "P101" ||
		got.Invoice.TotalVND != 1_000_000 || got.Invoice.LandlordID != w.landlord || got.Invoice.TenantID == nil || got.Invoice.Status != domain.InvoiceSent {
		t.Errorf("payer and bill: %+v %+v", got.Payer, got.Invoice)
	}
	if _, err := w.payments.Get(ctx, domain.Viewer{UserID: w.tenant}, id); err != nil {
		t.Errorf("the payer sees it: %v", err)
	}
	for name, v := range map[string]domain.Viewer{
		"another landlord": {UserID: w.other, AsLandlord: true}, "the landlord as a tenant": {UserID: w.landlord},
		"another tenant": {UserID: w.other}, "the tenant as a landlord": {UserID: w.tenant, AsLandlord: true},
	} {
		if _, err := w.payments.Get(ctx, v, id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	keys, err := w.payments.ProofKeys(ctx, id)
	if err != nil || len(keys) != 2 {
		t.Errorf("proof keys: %v, %v", keys, err)
	}
	if _, err := w.payments.ProofKeys(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no such payment: %v", err)
	}
}

func TestReviewLifecycleAndTotals(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a := w.pending(t, w.bill, 300_000)
	b := w.pending(t, w.bill, 200_000)
	c := w.pending(t, w.bill, 100_000)

	totals, err := w.payments.Totals(ctx, []uuid.UUID{w.bill, uuid.New()})
	if err != nil || len(totals) != 1 || totals[w.bill] != (domain.Totals{PendingVND: 600_000}) {
		t.Fatalf("totals: %v, %v", totals, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	reviewer := w.landlord
	err = w.tx().WithinTx(ctx, func(ctx context.Context) error {
		p, err := w.payments.ForUpdate(ctx, a)
		if err != nil {
			return err
		}
		confirmed, err := p.Confirm(&reviewer, now)
		if err != nil {
			return err
		}
		if err := w.payments.SaveReview(ctx, confirmed); err != nil {
			return err
		}
		q, _ := w.payments.ForUpdate(ctx, b)
		rejected, err := q.Reject(reviewer, "no money", now)
		if err != nil {
			return err
		}
		return w.payments.SaveReview(ctx, rejected)
	})
	if err != nil {
		t.Fatal(err)
	}
	totals, _ = w.payments.Totals(ctx, []uuid.UUID{w.bill})
	if totals[w.bill] != (domain.Totals{ConfirmedVND: 300_000, PendingVND: 100_000}) {
		t.Errorf("a rejected payment counts for nothing: %v", totals)
	}
	got, _ := w.payments.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, b)
	if got.Status != domain.StatusRejected || *got.ReviewNote != "no money" || *got.ReviewedByID != reviewer || got.PaidAt != nil || !got.ReviewedAt.Equal(now) {
		t.Errorf("rejected: %+v", got.Payment)
	}

	// Reopened: the confirmed money no longer counts.
	if err := w.payments.VoidConfirmed(ctx, w.bill); err != nil {
		t.Fatal(err)
	}
	totals, _ = w.payments.Totals(ctx, []uuid.UUID{w.bill})
	if totals[w.bill] != (domain.Totals{PendingVND: 100_000}) {
		t.Errorf("voided: %v", totals)
	}
	if v, _ := w.payments.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, a); v.Status != domain.StatusVoided || v.PaidAt == nil {
		t.Errorf("a voided payment keeps its history: %+v", v.Payment)
	}

	if err := w.payments.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := w.payments.Get(ctx, domain.Viewer{UserID: w.tenant}, c); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("withdrawn: %v", err)
	}
	if _, err := w.payments.ForUpdate(ctx, c); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a deleted payment cannot be locked: %v", err)
	}
}

func (w *world) tx() *postgres.TxManager { return postgres.NewTxManager(w.pool) }

func TestRecordedPaymentHasNoPayerAndIsConfirmed(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	id, err := w.payments.Insert(ctx, domain.NewRecorded(w.bill, w.landlord, 250_000, nil, now, now))
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.payments.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, id)
	if err != nil || got.Status != domain.StatusConfirmed || got.PayerID != nil || got.Payer != nil || !got.Recorded() || got.Method != domain.MethodRecorded || len(got.ProofKeys) != 0 {
		t.Fatalf("%+v, %v", got.Payment, err)
	}
	if _, err := w.payments.Get(ctx, domain.Viewer{UserID: w.tenant}, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("the tenant did not pay it: %v", err)
	}
}

func TestListsPageNewestFirstAndOldestFirstPerBill(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	other := w.newBill(t, "sent", "app")
	var ids []uuid.UUID
	for i := range 5 {
		ids = append(ids, w.pending(t, w.bill, int64(100_000*(i+1))))
		time.Sleep(2 * time.Millisecond)
	}
	w.pending(t, other, 1)

	page, total, err := w.payments.ListOfLandlord(ctx, w.landlord, nil, 3, 0)
	if err != nil || total != 6 || len(page) != 3 || page[0].AmountVND != 1 {
		t.Fatalf("landlord: %d of %d, %v", len(page), total, err)
	}
	page, total, _ = w.payments.ListOfLandlord(ctx, w.landlord, nil, 3, 3)
	if total != 6 || len(page) != 3 || page[0].AmountVND != 300_000 {
		t.Errorf("second page: %d of %d first %d", len(page), total, page[0].AmountVND)
	}
	confirmed := domain.StatusConfirmed
	if page, total, _ = w.payments.ListOfLandlord(ctx, w.landlord, &confirmed, 10, 0); total != 0 || len(page) != 0 {
		t.Errorf("none confirmed: %d", total)
	}
	pending := domain.StatusPending
	if _, total, _ = w.payments.ListOfLandlord(ctx, w.landlord, &pending, 10, 0); total != 6 {
		t.Errorf("pending: %d", total)
	}
	if _, total, _ = w.payments.ListOfLandlord(ctx, w.other, nil, 10, 0); total != 0 {
		t.Errorf("another landlord sees none: %d", total)
	}

	page, total, err = w.payments.ListOfInvoice(ctx, w.bill, 2, 0)
	if err != nil || total != 5 || len(page) != 2 || page[0].ID != ids[0] || page[1].ID != ids[1] {
		t.Errorf("a bill's payments, oldest first: %d of %d, %v", len(page), total, err)
	}
}

func TestDeleteOfInvoiceReturnsTheProofKeys(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.pending(t, w.bill, 1, "payments/2026/11/aa.webp")
	w.pending(t, w.bill, 2, "payments/2026/11/bb.webp", "payments/2026/11/cc.webp")
	other := w.newBill(t, "sent", "app")
	w.pending(t, other, 3, "payments/2026/11/dd.webp")

	keys, err := w.payments.DeleteOfInvoice(ctx, w.bill)
	if err != nil || len(keys) != 3 {
		t.Fatalf("%v, %v", keys, err)
	}
	if totals, _ := w.payments.Totals(ctx, []uuid.UUID{w.bill, other}); totals[w.bill].PendingVND != 0 || totals[other].PendingVND != 3 {
		t.Errorf("only that bill's payments go: %v", totals)
	}
}

func TestPaymentConstraints(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	now := time.Now()
	for name, p := range map[string]domain.Payment{
		"zero amount":              {InvoiceID: w.bill, Method: "x", AmountVND: 0, Status: domain.StatusPending},
		"confirmed without a time": {InvoiceID: w.bill, Method: "x", AmountVND: 1, Status: domain.StatusConfirmed, ReviewedAt: &now},
		"pending but reviewed":     {InvoiceID: w.bill, Method: "x", AmountVND: 1, Status: domain.StatusPending, ReviewedAt: &now},
		"reviewed but pending not": {InvoiceID: w.bill, Method: "x", AmountVND: 1, Status: domain.StatusRejected},
		"unknown status":           {InvoiceID: w.bill, Method: "x", AmountVND: 1, Status: "paid"},
		"no such bill":             {InvoiceID: uuid.New(), Method: "x", AmountVND: 1, Status: domain.StatusPending},
	} {
		if _, err := w.payments.Insert(ctx, p); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

func TestPaymentMethods(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.methods.Get(ctx, w.landlord, domain.MethodManualProof); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("never saved: %v", err)
	}
	created, err := w.methods.Upsert(ctx, w.landlord, domain.StoredMethod{Method: domain.MethodMomo, ConfigSealed: "sealed-1", IsActive: true})
	if err != nil || !created {
		t.Fatalf("first save: %v, %v", created, err)
	}
	created, err = w.methods.Upsert(ctx, w.landlord, domain.StoredMethod{Method: domain.MethodMomo, ConfigSealed: "sealed-2", IsActive: false})
	if err != nil || created {
		t.Fatalf("second save replaces: %v, %v", created, err)
	}
	if _, err := w.methods.Upsert(ctx, w.landlord, domain.StoredMethod{Method: domain.MethodManualProof, ConfigSealed: "bank", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.methods.Upsert(ctx, w.other, domain.StoredMethod{Method: domain.MethodMomo, ConfigSealed: "theirs", IsActive: true}); err != nil {
		t.Fatal(err)
	}

	got, err := w.methods.Get(ctx, w.landlord, domain.MethodMomo)
	if err != nil || got.ConfigSealed != "sealed-2" || got.IsActive {
		t.Errorf("get: %+v, %v", got, err)
	}
	list, err := w.methods.ListOfLandlord(ctx, w.landlord)
	if err != nil || len(list) != 2 || list[0].Method != domain.MethodManualProof {
		t.Errorf("list: %+v, %v", list, err)
	}
	if removed, err := w.methods.Delete(ctx, w.landlord, domain.MethodMomo); err != nil || !removed {
		t.Errorf("delete: %v, %v", removed, err)
	}
	if removed, _ := w.methods.Delete(ctx, w.landlord, domain.MethodMomo); removed {
		t.Error("nothing left to delete")
	}
	if _, err := w.methods.Get(ctx, w.other, domain.MethodMomo); err != nil {
		t.Errorf("another landlord's method stays: %v", err)
	}
}

func TestInvoiceReaderScopesAndLocks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	draft := w.newBill(t, "draft", "app")
	byLink := w.newBill(t, "sent", "link")

	f, err := w.invoices.Get(ctx, domain.Viewer{UserID: w.tenant}, w.bill)
	if err != nil || f.TotalVND != 1_000_000 || f.PaymentCode == "" || f.Status != domain.InvoiceSent || f.LandlordID != w.landlord || *f.TenantID != w.tenant || f.RoomName != "P101" {
		t.Fatalf("tenant: %+v, %v", f, err)
	}
	if _, err := w.invoices.Get(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, draft); err != nil {
		t.Errorf("the landlord sees drafts: %v", err)
	}
	for name, c := range map[string]struct {
		v  domain.Viewer
		id uuid.UUID
	}{
		"a draft is not the tenant's":  {domain.Viewer{UserID: w.tenant}, draft},
		"a bill passed on by link":     {domain.Viewer{UserID: w.tenant}, byLink},
		"another landlord":             {domain.Viewer{UserID: w.other, AsLandlord: true}, w.bill},
		"another tenant":               {domain.Viewer{UserID: w.other}, w.bill},
		"the landlord is not a tenant": {domain.Viewer{UserID: w.landlord}, w.bill},
		"no such bill":                 {domain.Viewer{UserID: w.tenant}, uuid.New()},
	} {
		if _, err := w.invoices.Get(ctx, c.v, c.id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := w.invoices.ByID(ctx, byLink); err != nil {
		t.Errorf("the system sees every bill: %v", err)
	}
	if _, err := w.invoices.Lock(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("lock of nothing: %v", err)
	}
	err = w.tx().WithinTx(ctx, func(ctx context.Context) error {
		got, err := w.invoices.Lock(ctx, w.bill)
		if err != nil || got.ID != w.bill {
			t.Errorf("lock: %+v, %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sent, err := w.invoices.SentOfRoom(ctx, w.landlord, "p101")
	if err != nil || len(sent) != 2 || sent[0].ID != w.bill {
		t.Errorf("sent bills of a room, oldest first: %d, %v", len(sent), err)
	}
	if none, _ := w.invoices.SentOfRoom(ctx, w.other, "P101"); len(none) != 0 {
		t.Error("another landlord's room")
	}
}
