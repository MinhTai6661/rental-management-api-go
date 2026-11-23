package payment

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// Ledger answers the questions the invoice context has about the money of its bills: what was received, and what is
// done to the payments when a landlord marks a bill paid, unpaid again or deletes it. The invoice context sets the
// status of a bill itself; the ledger only records money, so it never calls back into the invoices.
type Ledger struct{ *core }

// NewLedger builds the ledger.
func NewLedger(d Deps, o Options) *Ledger { return &Ledger{newCore(d, o)} }

// Totals returns the confirmed and the pending money of each bill (zero for a bill with no payment).
func (l *Ledger) Totals(ctx context.Context, invoiceIDs []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	totals, err := l.payments.Totals(ctx, invoiceIDs)
	if err != nil {
		return nil, err
	}
	if totals == nil {
		totals = make(map[uuid.UUID]domain.Totals, len(invoiceIDs))
	}
	return totals, nil
}

// Settlement is money a landlord says arrived, written down when a bill is created paid or marked paid by hand.
type Settlement struct {
	InvoiceID uuid.UUID
	// ActorID is the landlord who settled it.
	ActorID   uuid.UUID
	AmountVND int64
	At        time.Time
}

// Settle records a confirmed payment of the money still owed, so a bill paid by hand counts as collected. It runs in
// the transaction of the invoice use case, which holds the bill and sets its status.
func (l *Ledger) Settle(ctx context.Context, s Settlement) error {
	inv, err := l.invoices.ByID(ctx, s.InvoiceID)
	if err != nil {
		return notFound(err)
	}
	id, err := l.payments.Insert(ctx, domain.NewRecorded(s.InvoiceID, s.ActorID, s.AmountVND, nil, s.At, s.At))
	if err != nil {
		return err
	}
	return l.record(ctx, AuditRecorded, s.ActorID, inv, id, nil, map[string]any{"amount_vnd": s.AmountVND, "invoice_paid": true, "by": "marked_paid"})
}

// Void takes back the confirmed payments of a bill that is marked unpaid again: they stay in the history but no longer
// count, so the tenant can pay the bill again.
func (l *Ledger) Void(ctx context.Context, invoiceID uuid.UUID) error {
	return l.payments.VoidConfirmed(ctx, invoiceID)
}

// Purge removes the payments of a bill that is deleted, with their proof pictures (only rejected or voided ones can be
// left: a bill with money waiting or received cannot be deleted).
func (l *Ledger) Purge(ctx context.Context, invoiceID uuid.UUID) error {
	keys, err := l.payments.DeleteOfInvoice(ctx, invoiceID)
	if err != nil {
		return err
	}
	l.removeFiles(ctx, keys)
	return nil
}
