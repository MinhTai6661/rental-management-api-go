package payment

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Totals is what the payments of a bill add up to.
type Totals struct {
	// ConfirmedVND is the money received, PendingVND the money waiting for the landlord's check.
	ConfirmedVND int64
	PendingVND   int64
}

// Remaining is what is still asked of the tenant: the total less what was received and what waits for review (money
// already sent is not asked for twice). It is never negative.
func Remaining(total int64, t Totals) int64 {
	return max(0, total-t.ConfirmedVND-t.PendingVND)
}

// Settles says whether the confirmed payments cover the bill.
func Settles(total, confirmed int64) bool { return confirmed >= total }

// CanPay says whether a bill takes a payment: it was sent and is not paid yet.
func CanPay(s InvoiceStatus) bool { return s == InvoiceSent }

// CheckAmount checks a payment of amount against what is still due on the bill: at least 1, at most the remaining.
func CheckAmount(amount, remaining int64) error {
	switch {
	case amount < 1:
		return violation(FieldAmount, ReasonAmountTooLow, map[string]string{"min": "1"})
	case amount > MaxMoneyVND:
		return violation(FieldAmount, ReasonAmountTooHigh, map[string]string{"max": strconv.FormatInt(MaxMoneyVND, 10)})
	case amount > remaining:
		return violation(FieldAmount, ReasonExceedsRemaining, map[string]string{"remaining": strconv.FormatInt(remaining, 10)})
	}
	return nil
}

// CheckNote checks the note of a payment (empty is no note).
func CheckNote(note string) error {
	if utf8.RuneCountInString(note) > MaxNoteLength {
		return violation("note", ReasonTooLong, map[string]string{"max": strconv.Itoa(MaxNoteLength)})
	}
	return nil
}

// NewPending is a payment a tenant sent: it waits for the landlord.
func NewPending(invoiceID, payerID uuid.UUID, method string, amount int64, note *string, proofKeys []string) Payment {
	return Payment{
		InvoiceID: invoiceID, PayerID: &payerID, Method: method, AmountVND: amount, Status: StatusPending, Note: note,
		ProofKeys: proofKeys,
	}
}

// NewRecorded is money the landlord says arrived outside the app: it counts as confirmed at once, on the day they say
// (paidAt) or when they wrote it down.
func NewRecorded(invoiceID, recorderID uuid.UUID, amount int64, note *string, paidAt, now time.Time) Payment {
	return Payment{
		InvoiceID: invoiceID, Method: MethodRecorded, AmountVND: amount, Status: StatusConfirmed, Note: note,
		ProofKeys: []string{}, RecordedByID: &recorderID, ReviewedByID: &recorderID, ReviewedAt: &now, PaidAt: &paidAt,
	}
}

// Confirm is the landlord saying the money arrived. reviewer is nil for a method that needs no review. Only a pending
// payment can be confirmed (ErrAlreadyReviewed).
func (p Payment) Confirm(reviewer *uuid.UUID, now time.Time) (Payment, error) {
	if p.Status != StatusPending {
		return p, ErrAlreadyReviewed
	}
	p.Status, p.ReviewedByID, p.ReviewedAt, p.PaidAt = StatusConfirmed, reviewer, &now, &now
	return p, nil
}

// Reject is the landlord not accepting the payment (money not received, wrong amount...). The reason is required.
func (p Payment) Reject(reviewer uuid.UUID, reason string, now time.Time) (Payment, error) {
	if p.Status != StatusPending {
		return p, ErrAlreadyReviewed
	}
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return p, violation(FieldReason, ReasonRequired, nil)
	case utf8.RuneCountInString(reason) > MaxReasonLength:
		return p, violation(FieldReason, ReasonTooLong, map[string]string{"max": strconv.Itoa(MaxReasonLength)})
	}
	p.Status, p.ReviewedByID, p.ReviewedAt, p.ReviewNote = StatusRejected, &reviewer, &now, &reason
	return p, nil
}

// CanWithdraw says whether the tenant can still take the payment back: before the landlord looked at it.
func (p Payment) CanWithdraw() bool { return p.Status == StatusPending }

// Counts says whether the payment counts towards the bill (confirmed money, or money waiting for review).
func (s Status) Counts() bool { return s == StatusConfirmed || s == StatusPending }
