package payment

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// MaxPageSize is the most payments a page of a list may hold.
const MaxPageSize = 100

// OfInvoice returns a page of the payments of a bill, oldest first, for a landlord of its room or the tenant it was
// sent to. domain.ErrNotFound for anybody else.
func (s *Payments) OfInvoice(ctx context.Context, v domain.Viewer, invoiceID uuid.UUID, limit, offset int) (Page, error) {
	if _, err := s.invoices.Get(ctx, v, invoiceID); err != nil {
		return Page{}, notFound(err)
	}
	details, total, err := s.payments.ListOfInvoice(ctx, invoiceID, min(max(limit, 1), MaxPageSize), max(offset, 0))
	if err != nil {
		return Page{}, err
	}
	return Page{Items: s.viewsOf(details), Total: total}, nil
}

// ForLandlord returns a page of the payments on the bills of the landlord's rooms, newest first (nil status keeps them
// all): what they have to look at, or have looked at.
func (s *Payments) ForLandlord(ctx context.Context, landlordID uuid.UUID, status *domain.Status, limit, offset int) (Page, error) {
	details, total, err := s.payments.ListOfLandlord(ctx, landlordID, status, min(max(limit, 1), MaxPageSize), max(offset, 0))
	if err != nil {
		return Page{}, err
	}
	return Page{Items: s.viewsOf(details), Total: total}, nil
}

// Instructions tells the tenant of a bill how to pay what is still due, one entry for each payment method the landlord
// switched on. There is nothing to show for a bill that is not sent, is paid, or whose remaining money is all sent
// already. amount is what the tenant is about to pay (nil: all that is due); it fills the QR code.
func (s *Payments) Instructions(ctx context.Context, tenantID, invoiceID uuid.UUID, amount *int64) ([]domain.Instructions, error) {
	facts, err := s.invoices.Get(ctx, domain.Viewer{UserID: tenantID}, invoiceID)
	if err != nil {
		return nil, notFound(err)
	}
	totals, err := s.totalsOf(ctx, facts.ID)
	if err != nil {
		return nil, err
	}
	remaining := domain.Remaining(facts.TotalVND, totals)
	if !domain.CanPay(facts.Status) || remaining == 0 {
		return []domain.Instructions{}, nil
	}
	pay := remaining
	if amount != nil {
		if err := domain.CheckAmount(*amount, remaining); err != nil {
			return nil, err
		}
		pay = *amount
	}
	saved, err := s.methods.ListOfLandlord(ctx, facts.LandlordID)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]domain.StoredMethod, len(saved))
	for _, m := range saved {
		byCode[m.Method] = m
	}
	out := []domain.Instructions{}
	for _, code := range s.drivers.Codes() {
		m, ok := byCode[code]
		if !ok || !m.IsActive {
			continue
		}
		cfg, err := s.openConfig(m)
		if err != nil {
			s.log.Warn("open the settings of a payment method", "error", err, "method", code, "landlord_id", facts.LandlordID)
			continue
		}
		drv, _ := s.drivers.Get(code)
		out = append(out, drv.Instructions(cfg, facts, pay))
	}
	return out, nil
}
