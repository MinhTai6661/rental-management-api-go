package contract

import "time"

// Billing is where a contract stands on its bills, read for the lists: the renter sees what is still to be paid and what
// waits for the landlord's check, the landlord how many bills are not paid yet. Only a bill that was sent counts as due;
// money already sent and waiting for review is not asked twice. Contracts only read the bills and payments (the invoice
// and payment contexts own them), so this is a summary and no rule.
type Billing struct {
	// DueCount and DueAmountVND are the sent bills that still have money to pay, and how much money is left on them.
	DueCount     int
	DueAmountVND int64
	// Partial is true when some of a due bill was paid or sent already.
	Partial bool
	// ReviewCount is how many payments on sent bills wait for the landlord's check.
	ReviewCount int
	// Overdue is true when a due bill is past its due date.
	Overdue bool
	// NextDueAt is the earliest due date among the due bills.
	NextDueAt *time.Time
	// UnpaidInvoices is how many bills are not paid yet (drafts and sent ones): what the landlord still has to collect.
	UnpaidInvoices int
}

// PaymentState is how a renter's contract stands on money, for the filter chips of "my rooms".
type PaymentState string

// Payment states.
const (
	// PaymentDue: a sent bill has money left to pay.
	PaymentDue PaymentState = "due"
	// PaymentReview: a payment on a sent bill waits for the landlord's check.
	PaymentReview PaymentState = "review"
	// PaymentSettled: neither.
	PaymentSettled PaymentState = "settled"
)

// ParsePaymentState reads a payment state code.
func ParsePaymentState(s string) (PaymentState, bool) {
	switch p := PaymentState(s); p {
	case PaymentDue, PaymentReview, PaymentSettled:
		return p, true
	}
	return "", false
}

// PaymentCounts is how many of a renter's running contracts are in each payment state (a contract can be both due and
// waiting for review).
type PaymentCounts struct {
	All     int
	Due     int
	Review  int
	Settled int
}
