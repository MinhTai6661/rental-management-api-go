package invoice

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// Send sends a draft to the person it is for, in the app or by link (delivery nil keeps the bill's own, the usual when
// it has none). Sending a bill that was already sent only sends it again: the email goes out once more. A bill that is
// paid cannot be sent (domain.ErrAlreadyPaid).
func (s *Invoices) Send(ctx context.Context, landlordID, id uuid.UUID, delivery *domain.Delivery) (View, error) {
	var wasDraft bool
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.invoices.ForUpdate(ctx, landlordView(landlordID), id)
		if err != nil {
			return notFound(err)
		}
		if !domain.CanSend(d.Status) {
			return domain.ErrAlreadyPaid
		}
		wasDraft = d.Status == domain.StatusDraft
		asked := delivery
		if asked == nil {
			asked = &d.Delivery
		}
		d.Delivery = domain.ResolveDelivery(d.Contract.HasAccount(), asked)
		if wasDraft {
			now, via := s.clock.Now(), domain.SourceManual
			d.Status, d.SentAt, d.SentVia, d.SentByID = domain.StatusSent, &now, &via, &landlordID
		}
		if err := s.invoices.SaveState(ctx, d.Invoice); err != nil {
			return err
		}
		// Passed on by link: the link is made now, so it is there to copy.
		if !d.Delivery.ReachesAccount() {
			if _, err := s.linkFor(ctx, &d, landlordID); err != nil {
				return err
			}
		}
		return s.record(ctx, AuditSent, landlordID, d.Invoice, d.Contract, map[string]any{"via": "manual", "first_time": wasDraft})
	})
	if err != nil {
		return View{}, err
	}
	d, err := s.invoices.Get(ctx, landlordView(landlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	s.mail(ctx, d)
	if wasDraft {
		s.notifyTenant(ctx, d, NotifyInvoiceSent)
	}
	return s.view(ctx, landlordView(landlordID), d)
}

// SetStatus moves a bill between unpaid (sent) and paid, as a landlord does when money arrives or was entered by
// mistake. A draft cannot change its payment status, except that one whose money is already in hand goes straight to
// paid without being sent to anybody (domain.ErrSendFirst otherwise). Marking a bill paid while a payment waits for
// review is refused (domain.ErrReviewPendingFirst).
func (s *Invoices) SetStatus(ctx context.Context, landlordID, id uuid.UUID, to domain.Status) (View, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.invoices.ForUpdate(ctx, landlordView(landlordID), id)
		if err != nil {
			return notFound(err)
		}
		return s.applyStatus(ctx, &d, to, landlordID)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, landlordView(landlordID), id)
}

// applyStatus changes the payment status of a locked bill inside the caller's transaction.
func (s *Invoices) applyStatus(ctx context.Context, d *domain.Detail, to domain.Status, actor uuid.UUID) error {
	settleDraft, err := domain.PlanStatusChange(d.Status, to)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	// A draft whose money is already in hand is settled directly: it becomes sent without telling the person.
	if settleDraft {
		via := domain.SourceManual
		d.Status, d.SentAt, d.SentVia, d.SentByID = domain.StatusSent, &now, &via, &actor
		if err := s.invoices.SaveState(ctx, d.Invoice); err != nil {
			return err
		}
		if err := s.record(ctx, AuditSent, actor, d.Invoice, d.Contract, map[string]any{"via": "manual", "first_time": true, "already_paid": true}); err != nil {
			return err
		}
	}
	before := d.Status
	switch {
	case before == domain.StatusPaid && to == domain.StatusSent:
		// Reopened: what was paid no longer counts, so the tenant can pay the bill again.
		if err := s.ledger.Void(ctx, d.ID); err != nil {
			return err
		}
	case before == domain.StatusSent && to == domain.StatusPaid:
		// Marking a bill paid by hand writes down the money still owed, so it counts as collected.
		totals, err := s.totalsOf(ctx, []uuid.UUID{d.ID})
		if err != nil {
			return err
		}
		t := totals[d.ID]
		if t.PendingVND > 0 {
			// A payment still waiting for review must be confirmed or rejected first, or it would be left hanging.
			return domain.ErrReviewPendingFirst
		}
		if remaining := d.TotalVND - t.PaidVND; remaining > 0 {
			if err := s.ledger.Settle(ctx, Settlement{InvoiceID: d.ID, ActorID: actor, AmountVND: remaining, At: now}); err != nil {
				return err
			}
		}
	}
	if before == to {
		return nil
	}
	d.Status, d.PaidAt = to, nil
	if to == domain.StatusPaid {
		d.PaidAt = &now
	}
	if err := s.invoices.SaveState(ctx, d.Invoice); err != nil {
		return err
	}
	return s.record(ctx, AuditStatusChanged, actor, d.Invoice, d.Contract, map[string]any{"from": string(before), "to": string(to)})
}
