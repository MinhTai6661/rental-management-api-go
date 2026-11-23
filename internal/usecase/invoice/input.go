package invoice

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// Opt is an optional field of a partial update: Set says the request carried it, Value is what it said (a nil
// pointer inside means "clear the field").
type Opt[T any] struct {
	Set   bool
	Value T
}

// Some builds an Opt that carries v.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: v} }

// BillInput is what the landlord fills in for a bill: the same fields when it is previewed, created and corrected.
type BillInput struct {
	// RentUnitPriceVND is the rent per period on this bill; nil uses the price of the contract.
	RentUnitPriceVND *int64
	Readings         []domain.ReadingInput
	Extras           []domain.Extra
}

// PreviewInput previews a new bill.
type PreviewInput struct {
	// PeriodStart picks the period; nil takes the oldest one without a bill.
	PeriodStart *time.Time
	BillInput
}

// CreateInput creates a bill (a draft, or one that is already paid).
type CreateInput struct {
	ContractID  uuid.UUID
	PeriodStart *time.Time
	BillInput
	// DueOn is the last day to pay; nil is a week from now (INVOICE_DUE_DAYS).
	DueOn *time.Time
	Note  *string
	// Name and PaymentLabel are what the bill is called and the words of its transfer text; nil takes the usual.
	Name         *string
	PaymentLabel *string
	// Delivery is how it reaches the person; nil takes the usual.
	Delivery *domain.Delivery
	// AlreadyPaid: the money was already received, so the bill is created sent and paid, with nothing sent to anybody.
	AlreadyPaid bool
}

// UpdateInput corrects an unpaid bill the way it was made: the lines are worked out again, the period stays.
type UpdateInput struct {
	BillInput
	DueOn *time.Time
	Note  Opt[*string]
	// Name and PaymentLabel left nil keep what the bill has.
	Name         *string
	PaymentLabel *string
	// ResendEmail emails the corrected bill to the tenant again (a draft is emailed when it is sent).
	ResendEmail bool
}

// View is a bill with what the viewer may do with it and how much of it is paid.
type View struct {
	domain.Detail
	Summary   domain.Summary
	Abilities domain.Abilities
}

// Page is a page of bills in "load more" style.
type Page struct {
	Items   []View
	Total   int
	HasMore bool
}

// Link is the address that shows a bill without signing in.
type Link struct {
	URL       string
	ExpiresAt time.Time
}

// ReadingPage is a page of the meter history of a room.
type ReadingPage struct {
	Items []domain.Reading
	Total int
}
