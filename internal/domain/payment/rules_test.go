package payment

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRemaining(t *testing.T) {
	tests := []struct {
		name  string
		total int64
		t     Totals
		want  int64
	}{
		{"nothing paid", 1_000_000, Totals{}, 1_000_000},
		{"part confirmed", 1_000_000, Totals{ConfirmedVND: 400_000}, 600_000},
		{"money waiting for review is not asked twice", 1_000_000, Totals{ConfirmedVND: 400_000, PendingVND: 300_000}, 300_000},
		{"fully covered", 1_000_000, Totals{ConfirmedVND: 1_000_000}, 0},
		{"never negative", 1_000_000, Totals{ConfirmedVND: 900_000, PendingVND: 300_000}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Remaining(tt.total, tt.t); got != tt.want {
				t.Errorf("Remaining = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSettles(t *testing.T) {
	if !Settles(1_000_000, 1_000_000) || !Settles(1_000_000, 1_000_001) {
		t.Error("confirmed money that covers the total settles the bill")
	}
	if Settles(1_000_000, 999_999) {
		t.Error("a short payment does not settle the bill")
	}
}

func TestCanPayOnlyASentBill(t *testing.T) {
	for s, want := range map[InvoiceStatus]bool{InvoiceDraft: false, InvoiceSent: true, InvoicePaid: false} {
		if CanPay(s) != want {
			t.Errorf("CanPay(%s) = %v", s, !want)
		}
	}
}

func reasonOf(t *testing.T, err error) (string, map[string]string) {
	t.Helper()
	var vs Violations
	if !errors.As(err, &vs) || len(vs) == 0 {
		t.Fatalf("want violations, got %v", err)
	}
	return vs[0].Reason, vs[0].Params
}

func TestCheckAmount(t *testing.T) {
	if err := CheckAmount(500_000, 500_000); err != nil {
		t.Errorf("paying exactly what is due: %v", err)
	}
	if err := CheckAmount(1, 500_000); err != nil {
		t.Errorf("paying a little: %v", err)
	}
	for name, tt := range map[string]struct {
		amount, remaining int64
		reason            string
	}{
		"zero":            {0, 500_000, ReasonAmountTooLow},
		"negative":        {-5, 500_000, ReasonAmountTooLow},
		"above remaining": {500_001, 500_000, ReasonExceedsRemaining},
		"absurd":          {MaxMoneyVND + 1, MaxMoneyVND + 5, ReasonAmountTooHigh},
	} {
		t.Run(name, func(t *testing.T) {
			reason, _ := reasonOf(t, CheckAmount(tt.amount, tt.remaining))
			if reason != tt.reason {
				t.Errorf("reason = %s, want %s", reason, tt.reason)
			}
		})
	}
	_, params := reasonOf(t, CheckAmount(10, 5))
	if params["remaining"] != "5" {
		t.Errorf("the refusal must say what is left, got %v", params)
	}
}

func TestCheckNote(t *testing.T) {
	if CheckNote(strings.Repeat("a", MaxNoteLength)) != nil {
		t.Error("a note at the limit is fine")
	}
	if CheckNote(strings.Repeat("a", MaxNoteLength+1)) == nil {
		t.Error("a note over the limit is refused")
	}
}

func pending() Payment {
	return NewPending(uuid.New(), uuid.New(), MethodManualProof, 100, nil, nil)
}

func TestPendingTransitions(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	reviewer := uuid.New()

	confirmed, err := pending().Confirm(&reviewer, now)
	if err != nil || confirmed.Status != StatusConfirmed || confirmed.PaidAt == nil || !confirmed.PaidAt.Equal(now) || *confirmed.ReviewedByID != reviewer {
		t.Fatalf("confirm: %+v, %v", confirmed, err)
	}
	auto, err := pending().Confirm(nil, now)
	if err != nil || auto.ReviewedByID != nil || auto.ReviewedAt == nil {
		t.Errorf("a method that needs no review confirms by itself: %+v, %v", auto, err)
	}

	rejected, err := pending().Reject(reviewer, "  money did not arrive ", now)
	if err != nil || rejected.Status != StatusRejected || *rejected.ReviewNote != "money did not arrive" || rejected.PaidAt != nil {
		t.Fatalf("reject: %+v, %v", rejected, err)
	}
}

func TestOnlyAPendingPaymentIsReviewedOrWithdrawn(t *testing.T) {
	now := time.Now()
	reviewer := uuid.New()
	for _, st := range []Status{StatusConfirmed, StatusRejected, StatusVoided} {
		p := pending()
		p.Status = st
		if _, err := p.Confirm(&reviewer, now); !errors.Is(err, ErrAlreadyReviewed) {
			t.Errorf("confirm a %s payment: %v", st, err)
		}
		if _, err := p.Reject(reviewer, "no", now); !errors.Is(err, ErrAlreadyReviewed) {
			t.Errorf("reject a %s payment: %v", st, err)
		}
		if p.CanWithdraw() {
			t.Errorf("a %s payment cannot be withdrawn", st)
		}
	}
	if !pending().CanWithdraw() {
		t.Error("a pending payment can be withdrawn")
	}
}

func TestRejectNeedsAReason(t *testing.T) {
	for _, reason := range []string{"", "   "} {
		_, err := pending().Reject(uuid.New(), reason, time.Now())
		if r, _ := reasonOf(t, err); r != ReasonRequired {
			t.Errorf("reason %q: %v", reason, err)
		}
	}
	if _, err := pending().Reject(uuid.New(), strings.Repeat("x", MaxReasonLength+1), time.Now()); err == nil {
		t.Error("a reason over the limit is refused")
	}
}

func TestRecordedPaymentIsConfirmedAtOnce(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	paidOn := time.Date(2026, 11, 18, 23, 59, 59, 0, time.UTC)
	rec := uuid.New()
	p := NewRecorded(uuid.New(), rec, 250_000, nil, paidOn, now)
	if p.Status != StatusConfirmed || p.Method != MethodRecorded || p.PayerID != nil || !p.Recorded() || !p.PaidAt.Equal(paidOn) || !p.ReviewedAt.Equal(now) {
		t.Errorf("recorded payment: %+v", p)
	}
	if !p.Status.Counts() || StatusRejected.Counts() || StatusVoided.Counts() || !StatusPending.Counts() {
		t.Error("only confirmed and pending money counts")
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range []string{"pending", "confirmed", "rejected", "voided"} {
		if _, ok := ParseStatus(s); !ok {
			t.Errorf("%s should parse", s)
		}
	}
	if _, ok := ParseStatus("paid"); ok {
		t.Error("paid is a bill status, not a payment one")
	}
}
