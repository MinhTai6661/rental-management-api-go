package payment

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// Payments is the use cases of the payments on bills: a tenant pays (all or part of) a sent bill, the landlord
// confirms or rejects what was sent or writes down money that came in outside the app, and both sides read them.
type Payments struct{ *core }

// NewPayments builds the payment use cases.
func NewPayments(d Deps, o Options) *Payments { return &Payments{newCore(d, o)} }

// accepts checks that the landlord set up and switched on the method.
func (c *core) accepts(ctx context.Context, landlordID uuid.UUID, code string) error {
	m, err := c.methods.Get(ctx, landlordID, code)
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return domain.ErrMethodNotAccepted
	case err != nil:
		return err
	case !m.IsActive:
		return domain.ErrMethodNotAccepted
	}
	return nil
}

// Submit is the tenant of a bill paying all or part of it with a method the landlord accepts. The bill must be sent,
// and the amount at most what is still due (money already sent and waiting for review is not asked twice). The proof
// pictures are checked and stored before the bill is locked (the slow part) and removed again if the payment is
// refused. A method the landlord has to check leaves the payment pending; one that verifies itself confirms it at once.
func (s *Payments) Submit(ctx context.Context, in SubmitInput) (View, error) {
	method := in.Method
	if method == "" {
		method = DefaultMethod
	}
	drv, err := s.drivers.Get(method)
	if err != nil {
		return View{}, err
	}
	if in.Note != nil {
		if err := domain.CheckNote(*in.Note); err != nil {
			return View{}, err
		}
	}
	if err := drv.CheckSubmission(Submission{ProofCount: len(in.Proofs)}); err != nil {
		return View{}, err
	}
	payer := domain.Viewer{UserID: in.PayerID}
	// A quick look before any picture is processed; the check under the lock below is the one that counts.
	if err := s.canTake(ctx, payer, in, drv.Code()); err != nil {
		return View{}, err
	}
	keys, err := s.storeProofs(ctx, in.Proofs)
	if err != nil {
		return View{}, err
	}
	var id uuid.UUID
	var facts domain.InvoiceFacts
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// One submission at a time per bill, so two parallel ones cannot together pay more than the total.
		if facts, err = s.invoices.Lock(ctx, in.InvoiceID); err != nil {
			return notFound(err)
		}
		if facts.TenantID == nil || *facts.TenantID != in.PayerID {
			return domain.ErrNotFound
		}
		if !domain.CanPay(facts.Status) {
			return domain.ErrNotPayable
		}
		if err := s.accepts(ctx, facts.LandlordID, drv.Code()); err != nil {
			return err
		}
		totals, err := s.totalsOf(ctx, facts.ID)
		if err != nil {
			return err
		}
		if err := domain.CheckAmount(in.AmountVND, domain.Remaining(facts.TotalVND, totals)); err != nil {
			return err
		}
		if id, err = s.payments.Insert(ctx, domain.NewPending(facts.ID, in.PayerID, drv.Code(), in.AmountVND, in.Note, keys)); err != nil {
			return err
		}
		return s.record(ctx, AuditSubmitted, in.PayerID, facts, id, &in.PayerID, map[string]any{"amount_vnd": in.AmountVND, "method": drv.Code()})
	})
	if err != nil {
		s.removeFiles(ctx, keys)
		return View{}, err
	}
	if !drv.RequiresReview() {
		return s.confirmPayment(ctx, id, facts.ID, nil)
	}
	d, err := s.payments.Get(ctx, payer, id)
	if err != nil {
		return View{}, notFound(err)
	}
	s.notifyLandlord(ctx, d)
	return s.viewOf(d), nil
}

// canTake is the quick look at a submission: the tenant sees the bill, it takes payments, the landlord accepts the
// method and the amount is not above what is due.
func (s *Payments) canTake(ctx context.Context, payer domain.Viewer, in SubmitInput, method string) error {
	facts, err := s.invoices.Get(ctx, payer, in.InvoiceID)
	if err != nil {
		return notFound(err)
	}
	if !domain.CanPay(facts.Status) {
		return domain.ErrNotPayable
	}
	if err := s.accepts(ctx, facts.LandlordID, method); err != nil {
		return err
	}
	totals, err := s.totalsOf(ctx, facts.ID)
	if err != nil {
		return err
	}
	return domain.CheckAmount(in.AmountVND, domain.Remaining(facts.TotalVND, totals))
}
