package payment

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// Seeder gives the demo landlord a bank account and the demo bills some payments.
type Seeder struct {
	*core
	uc     *Payments
	method *Methods
}

// NewSeeder returns a seeder on the payment use cases (so the rules apply to the demo data too).
func NewSeeder(d Deps, o Options) *Seeder {
	return &Seeder{core: newCore(d, o), uc: NewPayments(d, o), method: NewMethods(d, o)}
}

// demoBank is the bank account of the demo landlord.
func demoBank() domain.Config {
	return domain.Config{
		domain.ConfigBankCode: "VCB", domain.ConfigBankName: "Vietcombank", domain.ConfigAccountNumber: "0123456789",
		domain.ConfigAccountName: "DEMO LANDLORD",
	}
}

// SeedDemo sets up the landlord's bank transfer and gives the sent bill of the demo tenant's ended contract (room P103)
// a confirmed part payment and a pending one. It is safe to repeat: a landlord who already saved the method keeps it,
// and a bill that has payments is left alone. It returns how many things it made.
func (s *Seeder) SeedDemo(ctx context.Context, landlordID, tenantID uuid.UUID) (int, error) {
	made := 0
	if _, err := s.methods.Get(ctx, landlordID, domain.MethodManualProof); errors.Is(err, shared.ErrNotFound) {
		if _, err := s.method.Save(ctx, landlordID, domain.MethodManualProof, demoBank(), nil); err != nil {
			return made, err
		}
		made++
	} else if err != nil {
		return made, err
	}
	bills, err := s.invoices.SentOfRoom(ctx, landlordID, "P103")
	if err != nil {
		return made, err
	}
	for _, bill := range bills {
		if bill.TenantID == nil || *bill.TenantID != tenantID {
			continue
		}
		have, err := s.uc.OfInvoice(ctx, domain.Viewer{UserID: landlordID, AsLandlord: true}, bill.ID, 1, 0)
		if err != nil {
			return made, err
		}
		if have.Total > 0 {
			continue
		}
		n, err := s.payOne(ctx, landlordID, tenantID, bill)
		made += n
		if errors.Is(err, domain.ErrNotFound) {
			continue // the bill is not for the app (it was passed on by link): the tenant cannot pay it
		}
		if err != nil {
			return made, err
		}
		break
	}
	return made, nil
}

// payOne has the tenant pay 40% of the bill, the landlord confirm it, and the tenant send 20% more that stays pending.
func (s *Seeder) payOne(ctx context.Context, landlordID, tenantID uuid.UUID, bill domain.InvoiceFacts) (int, error) {
	first, err := s.uc.Submit(ctx, SubmitInput{InvoiceID: bill.ID, PayerID: tenantID, AmountVND: bill.TotalVND * 40 / 100})
	if err != nil {
		return 0, err
	}
	if _, err := s.uc.Confirm(ctx, landlordID, first.ID); err != nil {
		return 1, err
	}
	note := "Chuyển khoản đợt 2"
	if _, err := s.uc.Submit(ctx, SubmitInput{InvoiceID: bill.ID, PayerID: tenantID, AmountVND: bill.TotalVND * 20 / 100, Note: &note}); err != nil {
		return 1, err
	}
	return 2, nil
}
