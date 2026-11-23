//go:build integration

package contractpg

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// The billing summary reads the bills and payments with plain SQL (the contract context never imports the invoice or the
// payment one), so these tests write those rows by hand.

// bill inserts a bill of the contract: its status, its total and how long until (or since) it falls due.
func (r *repos) bill(t *testing.T, contract uuid.UUID, month int, status string, total int64, dueIn time.Duration) uuid.UUID {
	t.Helper()
	start := time.Date(2026, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	draft := status == "draft"
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), `INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end,
		total_amount_vnd, due_at, status, sent_at, sent_via, paid_at, delivery)
		VALUES ($1, $2, 'Bill', 'HD', $3, $4, $5, $6, now() + $7::interval, $8,
		        CASE WHEN $9 THEN NULL ELSE now() END, CASE WHEN $9 THEN NULL ELSE 'manual' END, CASE WHEN $8 = 'paid' THEN now() END, 'app') RETURNING id`,
		contract, "INV-"+uuid.NewString()[:10], "HD "+uuid.NewString()[:8], start, start.AddDate(0, 1, -1), total,
		fmt.Sprintf("%d seconds", int(dueIn.Seconds())), status, draft).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// pay inserts a payment on a bill: confirmed ones count as paid, pending ones wait for review.
func (r *repos) pay(t *testing.T, invoice uuid.UUID, amount int64, status string) {
	t.Helper()
	_, err := r.pool.Exec(context.Background(), `INSERT INTO payments (invoice_id, method, amount_vnd, status, reviewed_at, paid_at)
		VALUES ($1, 'manual_proof', $2, $3, CASE WHEN $3 = 'pending' THEN NULL ELSE now() END, CASE WHEN $3 IN ('confirmed', 'voided') THEN now() END)`,
		invoice, amount, status)
	if err != nil {
		t.Fatal(err)
	}
}

// billingWorld has four running contracts of the tenant on four rooms: one due, one waiting for review, one settled and one
// with nothing billed at all.
type billingWorld struct {
	*repos
	due, review, settled, bare uuid.UUID
}

func newBillingWorld(t *testing.T) *billingWorld {
	t.Helper()
	r := newRepos(t)
	w := &billingWorld{repos: r}
	mk := func(name string, order int) uuid.UUID {
		room := r.newRoom(t, r.landlord, name)
		return r.insert(t, room, r.landlord, &r.tenant, domain.StatusActive, order)
	}
	w.due, w.review, w.settled, w.bare = mk("D1", 1), mk("D2", 2), mk("D3", 3), mk("D4", 4)

	// due: one overdue bill with 400,000 of 1,000,000 paid and 100,000 sent (500,000 left), a later one untouched
	// (2,000,000), a paid one and a draft that do not count as due, and a rejected payment that counts for nothing.
	a := r.bill(t, w.due, 9, "sent", 1_000_000, -48*time.Hour)
	r.pay(t, a, 400_000, "confirmed")
	r.pay(t, a, 100_000, "pending")
	r.pay(t, a, 50_000, "rejected")
	r.bill(t, w.due, 10, "sent", 2_000_000, 72*time.Hour)
	paid := r.bill(t, w.due, 8, "paid", 700_000, -500*time.Hour)
	r.pay(t, paid, 700_000, "confirmed")
	r.bill(t, w.due, 11, "draft", 5_000_000, 100*time.Hour)

	// review: a sent bill whose whole amount waits for the landlord.
	b := r.bill(t, w.review, 9, "sent", 800_000, 24*time.Hour)
	r.pay(t, b, 800_000, "pending")

	// settled: only a paid bill.
	c := r.bill(t, w.settled, 9, "paid", 900_000, -24*time.Hour)
	r.pay(t, c, 900_000, "confirmed")
	return w
}

func TestBillingSummaryOfEachContract(t *testing.T) {
	w := newBillingWorld(t)
	items, _, err := w.contracts.Search(context.Background(), domain.Viewer{UserID: w.tenant}, domain.Query{Limit: 20})
	if err != nil || len(items) != 4 {
		t.Fatalf("%d, %v", len(items), err)
	}
	by := map[uuid.UUID]domain.Billing{}
	for _, d := range items {
		by[d.ID] = d.Billing
	}

	due := by[w.due]
	if due.DueCount != 2 || due.DueAmountVND != 2_500_000 || !due.Partial || due.ReviewCount != 1 || !due.Overdue || due.NextDueAt == nil || due.UnpaidInvoices != 3 {
		t.Errorf("due: %+v", due)
	}
	if due.NextDueAt != nil && !due.NextDueAt.Before(time.Now()) {
		t.Errorf("the next due date is the overdue bill's: %v", due.NextDueAt)
	}
	review := by[w.review]
	if review.DueCount != 0 || review.DueAmountVND != 0 || review.Partial || review.ReviewCount != 1 || review.Overdue || review.NextDueAt != nil || review.UnpaidInvoices != 1 {
		t.Errorf("a bill whose money waits for review is not asked twice: %+v", review)
	}
	for name, id := range map[string]uuid.UUID{"settled": w.settled, "bare": w.bare} {
		if b := by[id]; b != (domain.Billing{}) {
			t.Errorf("%s: %+v", name, b)
		}
	}

	// The landlord sees the same summary on their contracts (the unpaid count includes drafts).
	mine, _, _ := w.contracts.Search(context.Background(), domain.Viewer{UserID: w.landlord, AsLandlord: true}, domain.Query{Limit: 20})
	for _, d := range mine {
		if d.ID == w.due && d.Billing.UnpaidInvoices != 3 {
			t.Errorf("landlord's unpaid count: %+v", d.Billing)
		}
	}
	got, err := w.contracts.Get(context.Background(), domain.Viewer{UserID: w.tenant}, w.due)
	if err != nil || got.Billing.DueCount != 2 {
		t.Errorf("the detail carries the summary too: %+v, %v", got.Billing, err)
	}
}

func TestPaymentStateFilterAndCounts(t *testing.T) {
	w := newBillingWorld(t)
	ctx := context.Background()
	viewer := domain.Viewer{UserID: w.tenant}
	ids := func(state domain.PaymentState) (got map[uuid.UUID]bool, total int) {
		t.Helper()
		items, total, err := w.contracts.Search(ctx, viewer, domain.Query{PaymentState: &state, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		got = map[uuid.UUID]bool{}
		for _, d := range items {
			got[d.ID] = true
		}
		return got, total
	}
	if got, total := ids(domain.PaymentDue); total != 1 || !got[w.due] {
		t.Errorf("due: %v of %d", got, total)
	}
	// The contract with the part payment is waiting for review too, as is the one whose money was all sent.
	if got, total := ids(domain.PaymentReview); total != 2 || !got[w.due] || !got[w.review] {
		t.Errorf("review: %v of %d", got, total)
	}
	if got, total := ids(domain.PaymentSettled); total != 2 || !got[w.settled] || !got[w.bare] {
		t.Errorf("settled: %v of %d", got, total)
	}

	counts, err := w.contracts.PaymentCounts(ctx, w.tenant, []domain.Status{domain.StatusActive})
	if err != nil || counts != (domain.PaymentCounts{All: 4, Due: 1, Review: 2, Settled: 2}) {
		t.Errorf("counts: %+v, %v", counts, err)
	}
	if counts, _ = w.contracts.PaymentCounts(ctx, w.tenant, []domain.Status{domain.StatusEnded}); counts != (domain.PaymentCounts{}) {
		t.Errorf("no ended contracts: %+v", counts)
	}
	if counts, _ = w.contracts.PaymentCounts(ctx, w.tenant2, []domain.Status{domain.StatusActive}); counts != (domain.PaymentCounts{}) {
		t.Errorf("another tenant has none: %+v", counts)
	}
}

func TestHasUnpaidInvoiceFilter(t *testing.T) {
	w := newBillingWorld(t)
	ctx := context.Background()
	landlord := domain.Viewer{UserID: w.landlord, AsLandlord: true}
	items, total, err := w.contracts.Search(ctx, landlord, domain.Query{HasUnpaidInvoice: true, Limit: 20})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("a draft or a sent bill is unpaid: %d, %v", total, err)
	}
	for _, d := range items {
		if d.ID != w.due && d.ID != w.review {
			t.Errorf("unexpected contract %s", d.ID)
		}
	}
	if _, total, _ = w.contracts.Search(ctx, landlord, domain.Query{Limit: 20}); total != 4 {
		t.Errorf("without the filter: %d", total)
	}
	// Combined with another filter.
	if _, total, _ = w.contracts.Search(ctx, landlord, domain.Query{HasUnpaidInvoice: true, Statuses: []domain.Status{domain.StatusEnded}, Limit: 20}); total != 0 {
		t.Errorf("combined: %d", total)
	}
}
