package payment

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// Confirm is the landlord saying the money arrived. When the confirmed payments cover the bill, it is marked paid.
// domain.ErrNotFound for a payment on somebody else's bill, domain.ErrAlreadyReviewed for one that was reviewed.
func (s *Payments) Confirm(ctx context.Context, landlordID, id uuid.UUID) (View, error) {
	d, err := s.payments.Get(ctx, landlordView(landlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	return s.confirmPayment(ctx, id, d.InvoiceID, &landlordID)
}

// confirmPayment confirms a payment and settles its bill when the confirmed money covers it; reviewer is nil for a
// method that verifies itself. The bill is locked first and the payment after it, the order the invoice use cases
// take their locks in, so the two cannot wait for each other.
func (c *core) confirmPayment(ctx context.Context, id, invoiceID uuid.UUID, reviewer *uuid.UUID) (View, error) {
	var inv domain.InvoiceFacts
	err := c.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if inv, err = c.invoices.Lock(ctx, invoiceID); err != nil {
			return notFound(err)
		}
		p, err := c.payments.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		confirmed, err := p.Confirm(reviewer, c.clock.Now())
		if err != nil {
			return err
		}
		if err := c.payments.SaveReview(ctx, confirmed); err != nil {
			return err
		}
		totals, err := c.totalsOf(ctx, inv.ID)
		if err != nil {
			return err
		}
		settles := inv.Status == domain.InvoiceSent && domain.Settles(inv.TotalVND, totals.ConfirmedVND)
		if settles {
			if err := c.status.MarkPaid(ctx, inv.LandlordID, inv.ID); err != nil {
				return err
			}
		}
		return c.record(ctx, AuditConfirmed, derefID(reviewer), inv, id, p.PayerID, map[string]any{
			"amount_vnd": p.AmountVND, "invoice_paid": settles, "automatic": reviewer == nil,
		})
	})
	if err != nil {
		return View{}, err
	}
	d, err := c.payments.Get(ctx, landlordView(inv.LandlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	c.notifyPayer(ctx, d, NotifyPaymentConfirmed)
	return c.viewOf(d), nil
}

// Reject is the landlord not accepting the payment (money not received, wrong amount...); the reason is required. The
// bill stays open. domain.ErrNotFound for a payment on somebody else's bill, domain.ErrAlreadyReviewed for one that was
// reviewed.
func (s *Payments) Reject(ctx context.Context, landlordID, id uuid.UUID, reason string) (View, error) {
	d, err := s.payments.Get(ctx, landlordView(landlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		p, err := s.payments.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		rejected, err := p.Reject(landlordID, reason, s.clock.Now())
		if err != nil {
			return err
		}
		if err := s.payments.SaveReview(ctx, rejected); err != nil {
			return err
		}
		return s.record(ctx, AuditRejected, landlordID, d.Invoice, id, p.PayerID, map[string]any{"amount_vnd": p.AmountVND, "reason": *rejected.ReviewNote})
	})
	if err != nil {
		return View{}, err
	}
	if d, err = s.payments.Get(ctx, landlordView(landlordID), id); err != nil {
		return View{}, notFound(err)
	}
	s.notifyPayer(ctx, d, NotifyPaymentRejected)
	return s.viewOf(d), nil
}

// Withdraw is the tenant taking back a payment the landlord has not looked at yet; its proof pictures go with it.
// domain.ErrNotFound for anybody else's payment, domain.ErrAlreadyReviewed once it was reviewed.
func (s *Payments) Withdraw(ctx context.Context, payerID, id uuid.UUID) error {
	d, err := s.payments.Get(ctx, domain.Viewer{UserID: payerID}, id)
	if err != nil {
		return notFound(err)
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		p, err := s.payments.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if !p.CanWithdraw() {
			return domain.ErrAlreadyReviewed
		}
		if err := s.record(ctx, AuditWithdrawn, payerID, d.Invoice, id, &payerID, map[string]any{"amount_vnd": p.AmountVND}); err != nil {
			return err
		}
		return s.payments.Delete(ctx, id)
	})
	if err != nil {
		return err
	}
	s.removeFiles(ctx, d.ProofKeys)
	return nil
}
