package payment

import (
	"context"
	"slices"
	"strconv"

	domain "rental-management/internal/domain/payment"
)

// Submission is what a tenant sends with a payment, besides the amount and the note.
type Submission struct {
	// ProofCount is how many proof pictures came with it.
	ProofCount int
}

// Driver is one way of paying a bill. Adding a payment method (an online gateway, another wallet) means writing a
// driver and registering it in NewDrivers; nothing else in the use cases has to know about the method.
type Driver interface {
	// Code is stored in the payments and in the landlord's saved methods.
	Code() string
	// CheckConfig checks the landlord's settings of the method (the account to pay into...) and returns them cleaned:
	// only the fields the method knows. A broken rule is a domain.Violations.
	CheckConfig(cfg domain.Config) (domain.Config, error)
	// CheckSubmission checks what a tenant sends when paying with the method.
	CheckSubmission(s Submission) error
	// RequiresReview says whether the landlord has to confirm each payment (false for a method that is verified by
	// itself).
	RequiresReview() bool
	// Instructions tells what the tenant needs to make a payment of amountVND on the bill.
	Instructions(cfg domain.Config, inv domain.InvoiceFacts, amountVND int64) domain.Instructions
	// Discard removes the files a payment of the method kept (its proof pictures and their small copies).
	Discard(ctx context.Context, files FileStore, keys []string) error
}

// Drivers are the payment methods the platform offers, by code, in their order.
type Drivers struct {
	list []Driver
}

// NewDrivers returns the registry with the methods built in: a bank transfer and a MoMo transfer, both checked by hand.
// maxProofs is how many proof pictures a payment may carry.
func NewDrivers(maxProofs int) *Drivers {
	return &Drivers{list: []Driver{
		manualProof{proofRules{max: maxProofs}},
		momo{proofRules{max: maxProofs}},
	}}
}

// Get returns the driver with the code (domain.ErrUnknownMethod when there is none).
func (d *Drivers) Get(code string) (Driver, error) {
	for _, drv := range d.list {
		if drv.Code() == code {
			return drv, nil
		}
	}
	return nil, domain.ErrUnknownMethod
}

// Codes lists the codes of the methods, in order.
func (d *Drivers) Codes() []string {
	codes := make([]string, len(d.list))
	for i, drv := range d.list {
		codes[i] = drv.Code()
	}
	return codes
}

// Has says whether there is a method with the code.
func (d *Drivers) Has(code string) bool { return slices.Contains(d.Codes(), code) }

// DefaultMethod is the method used when a tenant does not name one.
const DefaultMethod = domain.MethodManualProof

// proofRules is what the methods checked by hand share: a payment may carry up to max proof pictures, none required,
// and the landlord confirms each one.
type proofRules struct{ max int }

// CheckSubmission refuses more proof pictures than the limit.
func (r proofRules) CheckSubmission(s Submission) error {
	if s.ProofCount > r.max {
		return domain.Violations{{Field: domain.FieldProofs, Reason: domain.ReasonTooManyProofs, Params: map[string]string{"max": strconv.Itoa(r.max)}}}
	}
	return nil
}

// RequiresReview is true: the landlord checks that the money arrived.
func (proofRules) RequiresReview() bool { return true }

// Discard removes the proof pictures and their small copies.
func (proofRules) Discard(ctx context.Context, files FileStore, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	all := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		all = append(all, k, domain.ThumbKey(k))
	}
	return files.Delete(ctx, all...)
}

// manualProof is a bank transfer checked by hand: the tenant pays into the landlord's bank account, may send a
// screenshot of the transfer, and the landlord confirms the money arrived. The tenant is shown a VietQR code with the
// amount and the transfer text of the bill already filled in.
type manualProof struct{ proofRules }

func (manualProof) Code() string { return domain.MethodManualProof }

func (manualProof) CheckConfig(cfg domain.Config) (domain.Config, error) {
	return domain.CheckBankConfig(cfg)
}

func (manualProof) Instructions(cfg domain.Config, inv domain.InvoiceFacts, amountVND int64) domain.Instructions {
	return domain.BankInstructions(cfg, inv.PaymentCode, amountVND)
}

// momo is a MoMo wallet transfer checked by hand, like a bank transfer: the tenant sends money to the landlord's MoMo
// number (the QR code opens MoMo with the amount filled in), may send a screenshot, and the landlord confirms.
type momo struct{ proofRules }

func (momo) Code() string { return domain.MethodMomo }

func (momo) CheckConfig(cfg domain.Config) (domain.Config, error) { return domain.CheckMomoConfig(cfg) }

func (momo) Instructions(cfg domain.Config, inv domain.InvoiceFacts, amountVND int64) domain.Instructions {
	return domain.MoMoInstructions(cfg, inv.PaymentCode, amountVND)
}
