package payment

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// Record is the landlord writing down money that reached them outside the app: the person who paid has no account to
// pay with (a blank contract) or paid in cash. It counts as confirmed at once, and the bill is paid when the confirmed
// payments cover it. The bill must be sent (domain.ErrNotPayable) and the amount at most what is still due.
func (s *Payments) Record(ctx context.Context, in RecordInput) (View, error) {
	if in.Note != nil {
		if err := domain.CheckNote(*in.Note); err != nil {
			return View{}, err
		}
	}
	now := s.clock.Now()
	paidAt := now
	if in.PaidOn != nil {
		if in.PaidOn.After(s.today()) {
			return View{}, domain.Violations{{Field: "paid_on", Reason: domain.ReasonDateInFuture}}
		}
		paidAt = s.endOfDay(*in.PaidOn)
	}
	if _, err := s.invoices.Get(ctx, landlordView(in.LandlordID), in.InvoiceID); err != nil {
		return View{}, notFound(err)
	}
	var id uuid.UUID
	var inv domain.InvoiceFacts
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if inv, err = s.invoices.Lock(ctx, in.InvoiceID); err != nil {
			return notFound(err)
		}
		if !domain.CanPay(inv.Status) {
			return domain.ErrNotPayable
		}
		totals, err := s.totalsOf(ctx, inv.ID)
		if err != nil {
			return err
		}
		if err := domain.CheckAmount(in.AmountVND, domain.Remaining(inv.TotalVND, totals)); err != nil {
			return err
		}
		if id, err = s.payments.Insert(ctx, domain.NewRecorded(inv.ID, in.LandlordID, in.AmountVND, in.Note, paidAt, now)); err != nil {
			return err
		}
		settles := domain.Settles(inv.TotalVND, totals.ConfirmedVND+in.AmountVND)
		if settles {
			if err := s.status.MarkPaid(ctx, inv.LandlordID, inv.ID); err != nil {
				return err
			}
		}
		return s.record(ctx, AuditRecorded, in.LandlordID, inv, id, nil, map[string]any{"amount_vnd": in.AmountVND, "invoice_paid": settles})
	})
	if err != nil {
		return View{}, err
	}
	d, err := s.payments.Get(ctx, landlordView(in.LandlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	return s.viewOf(d), nil
}
