package invoice

import (
	"errors"
	"fmt"
)

// The errors of the invoice use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code, the fields and the message parameters the client sees.
var (
	// ErrNotFound: the bill (or the contract, or the room) does not exist, or the caller may not see it.
	ErrNotFound = errors.New("invoice not found")
	// ErrContractNotRunning: only a running contract can be billed.
	ErrContractNotRunning = errors.New("contract is not running")
	// ErrAlreadyPaid: a bill that is paid cannot be sent.
	ErrAlreadyPaid = errors.New("invoice is already paid")
	// ErrSendFirst: a draft cannot change its payment status; it has to be sent first (or settled directly).
	ErrSendFirst = errors.New("send the invoice before changing its payment status")
	// ErrForbidden: the caller sees the bill but may not do this to it (a manager without the right).
	ErrForbidden = errors.New("not allowed to do this with the invoice")
	// ErrLocked: a bill that is paid, or that somebody paid or is paying, cannot be corrected or removed.
	ErrLocked = errors.New("invoice can no longer be changed")
	// ErrReviewPendingFirst: a payment still waits for the landlord's check, so the bill cannot be marked paid.
	ErrReviewPendingFirst = errors.New("review the pending payments first")
	// ErrLinkNeedsSent: the link of a bill is for a bill that was sent.
	ErrLinkNeedsSent = errors.New("a link is for an invoice that was sent")
	// ErrLinkInvalid: a share link that is wrong, expired, withdrawn or for a draft; they all say the same.
	ErrLinkInvalid = errors.New("link is not valid")
	// ErrPeriodBilled: the period already has a bill.
	ErrPeriodBilled = errors.New("period is already billed")
)

// The reasons a billing rule is broken, with the input each one belongs to.
const (
	FieldReadings    = "readings"
	FieldPeriodStart = "period_start"

	ReasonUnknownService           = "unknown_service"
	ReasonPreviousReadingRequired  = "previous_reading_required"
	ReasonReadingLowerThanPrevious = "reading_lower_than_previous"
	ReasonNewReadingRequired       = "new_reading_required"
	ReasonDayNeedsEnd              = "day_needs_end"
	ReasonNothingToBill            = "nothing_to_bill"
	ReasonInvalidPeriod            = "invalid_period"
)

// RuleViolation says that a billing rule was broken, with a reason code and its parameters instead of text, so the
// layer that talks to the user decides the wording and the language.
type RuleViolation struct {
	// Field is the input the problem belongs to ("readings", "period_start").
	Field string
	// Reason is one of the Reason* codes.
	Reason string
	Params map[string]string
}

func (v *RuleViolation) Error() string { return fmt.Sprintf("%s: %s", v.Field, v.Reason) }

func violation(field, reason string, params map[string]string) *RuleViolation {
	return &RuleViolation{Field: field, Reason: reason, Params: params}
}
