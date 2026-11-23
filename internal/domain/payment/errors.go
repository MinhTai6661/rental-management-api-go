package payment

import (
	"errors"
	"fmt"
	"strings"
)

// The errors of the payment use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code, the fields and the message parameters the client sees.
var (
	// ErrNotFound: the payment (or the bill, or the method) does not exist, or the caller may not see it.
	ErrNotFound = errors.New("payment not found")
	// ErrNotPayable: only a bill that was sent and is not paid yet takes a payment.
	ErrNotPayable = errors.New("invoice cannot take a payment")
	// ErrMethodNotAccepted: the landlord does not accept this payment method.
	ErrMethodNotAccepted = errors.New("payment method is not accepted")
	// ErrAlreadyReviewed: the payment was already confirmed, rejected or voided (or was withdrawn).
	ErrAlreadyReviewed = errors.New("payment was already reviewed")
	// ErrUnknownMethod: no driver has that code.
	ErrUnknownMethod = errors.New("unknown payment method")
	// ErrFileNotFound: a proof picture is not in the store.
	ErrFileNotFound = errors.New("file not found")
	// ErrImageType, ErrImageCorrupt, ErrImageDimensions and ErrImageTooLarge are the reasons a proof picture is refused.
	ErrImageType       = errors.New("proof is not a supported image")
	ErrImageCorrupt    = errors.New("proof cannot be read as an image")
	ErrImageDimensions = errors.New("proof image dimensions are too large")
	ErrImageTooLarge   = errors.New("proof file is too large")
)

// The reasons a payment rule is broken, with the input each one belongs to.
const (
	FieldAmount = "amount_vnd"
	FieldReason = "reason"
	FieldProofs = "proofs"

	ReasonRequired         = "required"
	ReasonTooLong          = "too_long"
	ReasonAmountTooLow     = "amount_too_low"
	ReasonAmountTooHigh    = "amount_too_high"
	ReasonExceedsRemaining = "amount_exceeds_remaining"
	ReasonDateInFuture     = "date_in_future"
	ReasonUnknownBank      = "unknown_bank"
	ReasonBadAccountNumber = "bad_account_number"
	ReasonBadPhone         = "bad_phone"
	ReasonTooManyProofs    = "too_many_proofs"
)

// RuleViolation says that a payment rule was broken, with a reason code and its parameters instead of text, so the
// layer that talks to the user decides the wording and the language.
type RuleViolation struct {
	// Field is the input the problem belongs to ("amount", "config.bank_code").
	Field string
	// Reason is one of the Reason* codes.
	Reason string
	Params map[string]string
}

func (v *RuleViolation) Error() string { return fmt.Sprintf("%s: %s", v.Field, v.Reason) }

func violation(field, reason string, params map[string]string) Violations {
	return Violations{{Field: field, Reason: reason, Params: params}}
}

// Violations are several broken rules found at once (a settings form with two wrong fields).
type Violations []RuleViolation

// Error lists the broken rules.
func (vs Violations) Error() string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.Error()
	}
	return strings.Join(parts, "; ")
}

// IsRefusal says whether err is one of the domain's reasons for refusing a request (the others are failures of the
// system).
func IsRefusal(err error) bool {
	var many Violations
	for _, target := range []error{
		ErrNotFound, ErrNotPayable, ErrMethodNotAccepted, ErrAlreadyReviewed, ErrUnknownMethod, ErrImageType,
		ErrImageCorrupt, ErrImageDimensions, ErrImageTooLarge,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return errors.As(err, &many)
}
