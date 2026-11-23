package payment

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// SubmitInput is a tenant paying all or part of a bill.
type SubmitInput struct {
	InvoiceID uuid.UUID
	PayerID   uuid.UUID
	// Method is a payment method code; empty is DefaultMethod.
	Method    string
	AmountVND int64
	Note      *string
	// Proofs are the raw files of the proof pictures.
	Proofs [][]byte
}

// RecordInput is a landlord writing down money that came in outside the app.
type RecordInput struct {
	InvoiceID  uuid.UUID
	LandlordID uuid.UUID
	AmountVND  int64
	Note       *string
	// PaidOn is the day the money came in (nil: now). It may not be in the future.
	PaidOn *time.Time
}

// ProofLink is where a proof picture can be read until the link expires.
type ProofLink struct {
	Index        int
	URL          string
	ThumbnailURL string
}

// View is a payment with the signed links of its proof pictures. The links are made only for the people who may see
// the payment (every read here is scoped to them).
type View struct {
	domain.Detail
	Proofs []ProofLink
}

// Page is a page of payments and how many there are in all.
type Page struct {
	Items []View
	Total int
}

// ProofError says which proof picture of a submission was refused and why (the cause is one of the domain's
// image errors).
type ProofError struct {
	Index int
	Err   error
}

func (e *ProofError) Error() string { return "proof " + itoa(int64(e.Index)) + ": " + e.Err.Error() }

// Unwrap returns the reason.
func (e *ProofError) Unwrap() error { return e.Err }
