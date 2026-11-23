package payment

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

var ctx = context.Background()

// scene is a landlord who accepts bank transfers, a tenant and a sent bill of 1,000,000 VND between them.
type scene struct {
	w                *world
	landlord, tenant uuid.UUID
	bill             domain.InvoiceFacts
	pay              *Payments
	methods          *Methods
	ledger           *Ledger
}

func newScene(t *testing.T) *scene {
	t.Helper()
	w := newWorld()
	s := &scene{w: w, landlord: uuid.New(), tenant: uuid.New()}
	s.bill = w.bill(s.landlord, &s.tenant, 1_000_000)
	w.accept(s.landlord)
	email := "tenant@example.test"
	w.payers[s.tenant] = domain.Person{ID: s.tenant, Name: "Tenant T", Email: &email}
	s.pay, s.methods, s.ledger = NewPayments(w.deps(), Options{}), NewMethods(w.deps(), Options{}), NewLedger(w.deps(), Options{})
	return s
}

func (s *scene) submit(amount int64, proofs ...string) (View, error) {
	in := SubmitInput{InvoiceID: s.bill.ID, PayerID: s.tenant, AmountVND: amount}
	for _, p := range proofs {
		in.Proofs = append(in.Proofs, []byte(p))
	}
	return s.pay.Submit(ctx, in)
}

func reasons(t *testing.T, err error) map[string]string {
	t.Helper()
	var vs domain.Violations
	if !errors.As(err, &vs) {
		t.Fatalf("want violations, got %v", err)
	}
	out := map[string]string{}
	for _, v := range vs {
		out[v.Field] = v.Reason
	}
	return out
}

func TestTenantPaysPartOfABillWithProofsAndTheLandlordIsTold(t *testing.T) {
	s := newScene(t)
	v, err := s.submit(400_000, "one", "two")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != domain.StatusPending || v.AmountVND != 400_000 || v.Method != domain.MethodManualProof || *v.PayerID != s.tenant {
		t.Errorf("payment: %+v", v.Payment)
	}
	if len(v.Proofs) != 2 || !strings.Contains(v.Proofs[0].URL, "/proofs/0.webp") || !strings.Contains(v.Proofs[1].ThumbnailURL, "/proofs/1_thumb.webp") {
		t.Errorf("links: %+v", v.Proofs)
	}
	for _, key := range v.ProofKeys {
		if !strings.HasPrefix(key, domain.ProofKeyPrefix) || s.w.files[key] == nil || s.w.files[domain.ThumbKey(key)] == nil {
			t.Errorf("proof %s and its small copy must be stored", key)
		}
	}
	if len(s.w.notices) != 1 || s.w.notices[0].Kind != NotifyPaymentSubmitted || s.w.notices[0].UserID != s.landlord ||
		s.w.notices[0].Params["remaining_vnd"] != "600000" || s.w.notices[0].Audience != AudienceLandlord {
		t.Errorf("landlord notice: %+v", s.w.notices)
	}
	if len(s.w.markPaid) != 0 || s.w.invoices[s.bill.ID].facts.Status != domain.InvoiceSent {
		t.Error("a pending payment does not settle the bill")
	}
	if got := s.w.actions(); len(got) != 1 || got[0] != AuditSubmitted {
		t.Errorf("audit: %v", got)
	}
}

func TestPaymentsMayCarryNoProof(t *testing.T) {
	s := newScene(t)
	v, err := s.submit(100_000)
	if err != nil || len(v.Proofs) != 0 {
		t.Fatalf("%+v, %v", v, err)
	}
}

func TestPendingMoneyIsNotAskedTwice(t *testing.T) {
	s := newScene(t)
	if _, err := s.submit(700_000); err != nil {
		t.Fatal(err)
	}
	_, err := s.submit(400_000)
	if got := reasons(t, err)["amount_vnd"]; got != domain.ReasonExceedsRemaining {
		t.Errorf("the 300,000 left cannot be 400,000: %v", err)
	}
	if _, err := s.submit(300_000); err != nil {
		t.Errorf("paying exactly what is left: %v", err)
	}
}

func TestRefusedSubmissionsLeaveNothingBehind(t *testing.T) {
	s := newScene(t)
	var vs domain.Violations
	if _, err := s.submit(2_000_000, "p"); !errors.As(err, &vs) || vs[0].Reason != domain.ReasonExceedsRemaining {
		t.Fatalf("over the remaining: %v", err)
	}
	if _, err := s.submit(0); !errors.As(err, &vs) || vs[0].Reason != domain.ReasonAmountTooLow {
		t.Errorf("zero: %v", err)
	}
	if _, err := s.submit(10_000, "a", "b", "c", "d"); !errors.As(err, &vs) || vs[0].Reason != domain.ReasonTooManyProofs {
		t.Errorf("four proofs with a limit of three: %v", err)
	}
	var perr *ProofError
	if _, err := s.submit(10_000, "good", "bad"); !errors.As(err, &perr) || perr.Index != 1 || !errors.Is(err, domain.ErrImageCorrupt) {
		t.Errorf("a corrupt second picture: %v", err)
	}
	if _, err := s.submit(10_000, "gif"); !errors.Is(err, domain.ErrImageType) {
		t.Errorf("a picture of the wrong type: %v", err)
	}
	if len(s.w.files) != 0 || len(s.w.payments) != 0 || len(s.w.notices) != 0 {
		t.Errorf("a refused payment keeps no files (%d), payments (%d) or notices (%d)", len(s.w.files), len(s.w.payments), len(s.w.notices))
	}
}

func TestOnlyTheTenantOfASentBillPaysIt(t *testing.T) {
	s := newScene(t)
	stranger := uuid.New()
	if _, err := s.pay.Submit(ctx, SubmitInput{InvoiceID: s.bill.ID, PayerID: stranger, AmountVND: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("somebody else's bill is not found: %v", err)
	}
	if _, err := s.pay.Submit(ctx, SubmitInput{InvoiceID: s.bill.ID, PayerID: s.landlord, AmountVND: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the landlord is not the payer: %v", err)
	}
	s.w.invoices[s.bill.ID].facts.Status = domain.InvoicePaid
	if _, err := s.submit(1); !errors.Is(err, domain.ErrNotPayable) {
		t.Errorf("a paid bill takes no payment: %v", err)
	}
	s.w.invoices[s.bill.ID].facts.Status = domain.InvoiceDraft
	if _, err := s.submit(1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a draft is the landlord's work in progress: %v", err)
	}
	if _, err := s.pay.Submit(ctx, SubmitInput{InvoiceID: uuid.New(), PayerID: s.tenant, AmountVND: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no such bill: %v", err)
	}
}

func TestTheLandlordMustAcceptTheMethod(t *testing.T) {
	s := newScene(t)
	if _, err := s.pay.Submit(ctx, SubmitInput{InvoiceID: s.bill.ID, PayerID: s.tenant, AmountVND: 1, Method: domain.MethodMomo}); !errors.Is(err, domain.ErrMethodNotAccepted) {
		t.Errorf("momo was never set up: %v", err)
	}
	if _, err := s.pay.Submit(ctx, SubmitInput{InvoiceID: s.bill.ID, PayerID: s.tenant, AmountVND: 1, Method: "bitcoin"}); !errors.Is(err, domain.ErrUnknownMethod) {
		t.Errorf("no such method: %v", err)
	}
	m := s.w.methods[methodKey(s.landlord, domain.MethodManualProof)]
	m.IsActive = false
	s.w.methods[methodKey(s.landlord, domain.MethodManualProof)] = m
	if _, err := s.submit(1); !errors.Is(err, domain.ErrMethodNotAccepted) {
		t.Errorf("a method switched off: %v", err)
	}
}

func TestConfirmingTheLastPaymentSettlesTheBill(t *testing.T) {
	s := newScene(t)
	first, _ := s.submit(400_000)
	second, _ := s.submit(600_000, "p")

	got, err := s.pay.Confirm(ctx, s.landlord, first.ID)
	if err != nil || got.Status != domain.StatusConfirmed || got.PaidAt == nil || *got.ReviewedByID != s.landlord {
		t.Fatalf("confirm: %+v, %v", got.Payment, err)
	}
	if len(s.w.markPaid) != 0 {
		t.Fatal("part of the money does not settle the bill")
	}
	if _, err := s.pay.Confirm(ctx, s.landlord, second.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.w.markPaid) != 1 || s.w.markPaid[0] != s.bill.ID || s.w.invoices[s.bill.ID].facts.Status != domain.InvoicePaid {
		t.Errorf("the confirmed money covers the bill: %v", s.w.markPaid)
	}
	var kinds []string
	for _, n := range s.w.notices[2:] {
		kinds = append(kinds, n.Kind)
		if n.UserID != s.tenant || n.Audience != AudienceTenant {
			t.Errorf("the tenant is told: %+v", n)
		}
	}
	if len(kinds) != 2 || kinds[0] != NotifyPaymentConfirmed {
		t.Errorf("notices: %v", kinds)
	}
	if len(s.w.mails) != 2 || !s.w.mails[0].Confirmed || s.w.mails[0].To != "tenant@example.test" || s.w.mails[0].AmountVND != 400_000 {
		t.Errorf("mails: %+v", s.w.mails)
	}
	if !strings.Contains(strings.Join(s.w.actions(), ","), AuditConfirmed) {
		t.Error("a confirmation is on record")
	}
	if last := s.w.audits[len(s.w.audits)-1]; last.Meta["invoice_paid"] != true {
		t.Errorf("the audit says the bill was settled: %v", last.Meta)
	}
}

func TestAPaymentIsReviewedOnceAndOnlyByTheLandlordOfTheBill(t *testing.T) {
	s := newScene(t)
	p, _ := s.submit(100_000)
	if _, err := s.pay.Confirm(ctx, uuid.New(), p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := s.pay.Reject(ctx, s.tenant, p.ID, "no"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the tenant does not review: %v", err)
	}
	if _, err := s.pay.Confirm(ctx, s.landlord, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pay.Confirm(ctx, s.landlord, p.ID); !errors.Is(err, domain.ErrAlreadyReviewed) {
		t.Errorf("a second confirmation: %v", err)
	}
	if _, err := s.pay.Reject(ctx, s.landlord, p.ID, "late"); !errors.Is(err, domain.ErrAlreadyReviewed) {
		t.Errorf("rejecting a confirmed payment: %v", err)
	}
}

func TestRejectingNeedsAReasonAndFreesTheMoney(t *testing.T) {
	s := newScene(t)
	p, _ := s.submit(1_000_000)
	if _, err := s.pay.Reject(ctx, s.landlord, p.ID, " "); reasons(t, err)["reason"] != domain.ReasonRequired {
		t.Errorf("a reason is required: %v", err)
	}
	got, err := s.pay.Reject(ctx, s.landlord, p.ID, "money did not arrive")
	if err != nil || got.Status != domain.StatusRejected || *got.ReviewNote != "money did not arrive" {
		t.Fatalf("reject: %+v, %v", got.Payment, err)
	}
	if s.w.invoices[s.bill.ID].facts.Status != domain.InvoiceSent {
		t.Error("the bill stays open")
	}
	if n := s.w.notices[len(s.w.notices)-1]; n.Kind != NotifyPaymentRejected || n.UserID != s.tenant {
		t.Errorf("the tenant is told: %+v", n)
	}
	if m := s.w.mails[0]; m.Confirmed || m.Reason == nil || *m.Reason != "money did not arrive" {
		t.Errorf("mail: %+v", m)
	}
	if _, err := s.submit(1_000_000); err != nil {
		t.Errorf("the rejected money can be sent again: %v", err)
	}
}

func TestTheTenantWithdrawsOnlyAPendingPayment(t *testing.T) {
	s := newScene(t)
	p, _ := s.submit(100_000, "a")
	if err := s.pay.Withdraw(ctx, uuid.New(), p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("somebody else's payment: %v", err)
	}
	if err := s.pay.Withdraw(ctx, s.landlord, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the landlord does not withdraw: %v", err)
	}
	if err := s.pay.Withdraw(ctx, s.tenant, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.w.payments) != 0 || len(s.w.files) != 0 {
		t.Errorf("the payment and its proofs go together: %d payments, %d files", len(s.w.payments), len(s.w.files))
	}
	if got := s.w.actions(); got[len(got)-1] != AuditWithdrawn {
		t.Errorf("audit: %v", got)
	}

	q, _ := s.submit(100_000)
	if _, err := s.pay.Confirm(ctx, s.landlord, q.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.pay.Withdraw(ctx, s.tenant, q.ID); !errors.Is(err, domain.ErrAlreadyReviewed) {
		t.Errorf("a confirmed payment stays: %v", err)
	}
}

func TestRecordedPaymentOnABlankContract(t *testing.T) {
	s := newScene(t)
	blank := s.w.bill(s.landlord, nil, 500_000)
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	note := "cash"
	got, err := s.pay.Record(ctx, RecordInput{InvoiceID: blank.ID, LandlordID: s.landlord, AmountVND: 200_000, Note: &note, PaidOn: &day})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusConfirmed || got.Method != domain.MethodRecorded || got.PayerID != nil || !got.Recorded() || got.PaidAt.UTC().Day() != 18 || got.PaidAt.UTC().Hour() != 16 {
		t.Errorf("recorded: %+v (paid at %v)", got.Payment, got.PaidAt)
	}
	if len(s.w.markPaid) != 0 {
		t.Error("part of the bill is still due")
	}
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: blank.ID, LandlordID: s.landlord, AmountVND: 300_001}); reasons(t, err)["amount_vnd"] != domain.ReasonExceedsRemaining {
		t.Errorf("more than is due: %v", err)
	}
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: blank.ID, LandlordID: s.landlord, AmountVND: 300_000}); err != nil {
		t.Fatal(err)
	}
	if len(s.w.markPaid) != 1 || s.w.invoices[blank.ID].facts.Status != domain.InvoicePaid {
		t.Error("the recorded money covers the bill")
	}
	if len(s.w.notices) != 0 {
		t.Error("there is nobody to tell")
	}
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: blank.ID, LandlordID: s.landlord, AmountVND: 1}); !errors.Is(err, domain.ErrNotPayable) {
		t.Errorf("a paid bill takes nothing more: %v", err)
	}
}

func TestRecordRules(t *testing.T) {
	s := newScene(t)
	future := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: s.bill.ID, LandlordID: s.landlord, AmountVND: 1, PaidOn: &future}); reasons(t, err)["paid_on"] != domain.ReasonDateInFuture {
		t.Errorf("a day in the future: %v", err)
	}
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: s.bill.ID, LandlordID: uuid.New(), AmountVND: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	s.w.invoices[s.bill.ID].facts.Status = domain.InvoiceDraft
	if _, err := s.pay.Record(ctx, RecordInput{InvoiceID: s.bill.ID, LandlordID: s.landlord, AmountVND: 1}); !errors.Is(err, domain.ErrNotPayable) {
		t.Errorf("a draft: %v", err)
	}
}

func TestListsAreScopedAndPaged(t *testing.T) {
	s := newScene(t)
	for range 3 {
		if _, err := s.submit(100_000); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.pay.OfInvoice(ctx, domain.Viewer{UserID: s.tenant}, s.bill.ID, 2, 0)
	if err != nil || len(page.Items) != 2 || page.Total != 3 {
		t.Fatalf("tenant: %d of %d, %v", len(page.Items), page.Total, err)
	}
	if _, err := s.pay.OfInvoice(ctx, landlordView(s.landlord), s.bill.ID, 50, 2); err != nil {
		t.Errorf("landlord: %v", err)
	}
	if _, err := s.pay.OfInvoice(ctx, landlordView(uuid.New()), s.bill.ID, 50, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := s.pay.OfInvoice(ctx, domain.Viewer{UserID: uuid.New()}, s.bill.ID, 50, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another tenant: %v", err)
	}

	pending := domain.StatusPending
	all, err := s.pay.ForLandlord(ctx, s.landlord, &pending, 10, 0)
	if err != nil || all.Total != 3 {
		t.Fatalf("landlord list: %+v, %v", all.Total, err)
	}
	confirmed := domain.StatusConfirmed
	if none, _ := s.pay.ForLandlord(ctx, s.landlord, &confirmed, 10, 0); none.Total != 0 {
		t.Error("nothing is confirmed yet")
	}
	if other, _ := s.pay.ForLandlord(ctx, uuid.New(), nil, 10, 0); other.Total != 0 {
		t.Error("another landlord sees none")
	}
}

func TestPaymentInstructionsOnlyWhileMoneyIsOwed(t *testing.T) {
	s := newScene(t)
	got, err := s.pay.Instructions(ctx, s.tenant, s.bill.ID, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %v", got, err)
	}
	i := got[0]
	if i.Method != domain.MethodManualProof || i.AmountVND != 1_000_000 || i.TransferContent != "HD T11-P101 AB23C" || i.AccountNumber != "0123456789" ||
		!strings.Contains(i.QRURL, "amount=1000000") {
		t.Errorf("instructions: %+v", i)
	}

	// Money already sent is not asked for again.
	if _, err := s.submit(400_000); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.pay.Instructions(ctx, s.tenant, s.bill.ID, nil); got[0].AmountVND != 600_000 {
		t.Errorf("the QR asks for what is left: %d", got[0].AmountVND)
	}
	part := int64(250_000)
	if got, _ = s.pay.Instructions(ctx, s.tenant, s.bill.ID, &part); got[0].AmountVND != 250_000 {
		t.Errorf("the QR asks for the amount being paid: %d", got[0].AmountVND)
	}
	tooMuch := int64(600_001)
	if _, err = s.pay.Instructions(ctx, s.tenant, s.bill.ID, &tooMuch); reasons(t, err)["amount_vnd"] != domain.ReasonExceedsRemaining {
		t.Errorf("more than is due: %v", err)
	}
	if _, err := s.submit(600_000); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.pay.Instructions(ctx, s.tenant, s.bill.ID, nil); len(got) != 0 {
		t.Errorf("everything is sent already: %v", got)
	}
	if _, err := s.pay.Instructions(ctx, s.landlord, s.bill.ID, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the landlord is not the payer: %v", err)
	}
	s.w.methods[methodKey(s.landlord, domain.MethodManualProof)] = domain.StoredMethod{Method: domain.MethodManualProof, ConfigSealed: "garbage", IsActive: true}
	if got, err = s.pay.Instructions(ctx, s.tenant, s.bill.ID, nil); err != nil || len(got) != 0 {
		t.Errorf("settings that cannot be read show nothing: %v, %v", got, err)
	}
}

func TestMethodSettingsAreSealedAndListed(t *testing.T) {
	s := newScene(t)
	s.w.methods = map[string]domain.StoredMethod{}
	cfg := domain.Config{
		domain.ConfigBankCode: "TCB", domain.ConfigAccountNumber: "19031234567890", domain.ConfigAccountName: " NGUYEN A ", "extra": "dropped",
	}
	saved, err := s.methods.Save(ctx, s.landlord, domain.MethodManualProof, cfg, nil)
	if err != nil || !saved.IsActive || saved.Config[domain.ConfigAccountName] != "NGUYEN A" || saved.Config["extra"] != "" {
		t.Fatalf("save: %+v, %v", saved, err)
	}
	stored := s.w.methods[methodKey(s.landlord, domain.MethodManualProof)]
	if strings.Contains(stored.ConfigSealed, "19031234567890") || !strings.HasPrefix(stored.ConfigSealed, "sealed:") {
		t.Errorf("the account number must not be stored in the clear: %q", stored.ConfigSealed)
	}

	list, err := s.methods.List(ctx, s.landlord)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v, %v", list, err)
	}
	if list[0].Method != domain.MethodManualProof || list[0].Config[domain.ConfigAccountNumber] != "19031234567890" || !list[0].IsActive {
		t.Errorf("the saved method comes back: %+v", list[0])
	}
	if list[1].Method != domain.MethodMomo || list[1].Config != nil || list[1].IsActive {
		t.Errorf("a method never saved is off: %+v", list[1])
	}

	off := false
	if got, err := s.methods.Save(ctx, s.landlord, domain.MethodManualProof, cfg, &off); err != nil || got.IsActive {
		t.Errorf("switching off: %+v, %v", got, err)
	}
	if err := s.methods.Remove(ctx, s.landlord, domain.MethodManualProof); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.methods.List(ctx, s.landlord); list[0].Config != nil {
		t.Error("a removed method shows as never saved")
	}
	if err := s.methods.Remove(ctx, s.landlord, domain.MethodManualProof); err != nil {
		t.Errorf("removing twice is harmless: %v", err)
	}
	if err := s.methods.Remove(ctx, s.landlord, "bitcoin"); !errors.Is(err, domain.ErrUnknownMethod) {
		t.Errorf("no such method: %v", err)
	}
}

func TestSavingMethodsChecksAndAuditsWithMaskedNumbers(t *testing.T) {
	s := newScene(t)
	s.w.methods = map[string]domain.StoredMethod{}
	if _, err := s.methods.Save(ctx, s.landlord, domain.MethodManualProof, domain.Config{}, nil); len(reasons(t, err)) != 3 {
		t.Errorf("every missing field is reported: %v", err)
	}
	if _, err := s.methods.Save(ctx, s.landlord, "bitcoin", domain.Config{}, nil); !errors.Is(err, domain.ErrUnknownMethod) {
		t.Errorf("no such method: %v", err)
	}
	cfg := domain.Config{domain.ConfigBankCode: "VCB", domain.ConfigAccountNumber: "0123456789", domain.ConfigAccountName: "A"}
	if _, err := s.methods.Save(ctx, s.landlord, domain.MethodManualProof, cfg, nil); err != nil {
		t.Fatal(err)
	}
	cfg[domain.ConfigAccountNumber] = "9999888877"
	if _, err := s.methods.Save(ctx, s.landlord, domain.MethodManualProof, cfg, nil); err != nil {
		t.Fatal(err)
	}
	ev := s.w.audits[len(s.w.audits)-1]
	changes := ev.Meta["changes"].(map[string]map[string]any)
	if ev.Action != AuditMethodSaved || ev.Meta["created"] != false || changes[domain.ConfigAccountNumber]["from"] != "******6789" || changes[domain.ConfigAccountNumber]["to"] != "******8877" {
		t.Errorf("audit: %+v", ev)
	}
	for _, e := range s.w.audits {
		if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(fmtMeta(e.Meta), "*", ""), " ", ""), "0123456789") {
			t.Errorf("a whole account number reached the audit trail: %v", e.Meta)
		}
	}
	if _, err := s.methods.Save(ctx, s.landlord, domain.MethodMomo, domain.Config{domain.ConfigPhone: "0912345678", domain.ConfigAccountName: "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.pay.Instructions(ctx, s.tenant, s.bill.ID, nil); len(got) != 2 || got[0].Method != domain.MethodManualProof || got[1].Method != domain.MethodMomo {
		t.Errorf("both methods are offered in order: %+v", got)
	}
}

func fmtMeta(m map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				b.WriteString(k)
				walk(e)
			}
		case map[string]map[string]any:
			for k, e := range x {
				b.WriteString(k)
				walk(e)
			}
		case string:
			b.WriteString(x)
		}
	}
	walk(m)
	return b.String()
}

func TestLedger(t *testing.T) {
	s := newScene(t)
	other := s.w.bill(s.landlord, &s.tenant, 300_000)

	if got, err := s.ledger.Totals(ctx, []uuid.UUID{s.bill.ID, other.ID}); err != nil || len(got) != 0 || got[s.bill.ID].PendingVND != 0 {
		t.Errorf("empty ledger: %v, %v", got, err)
	}
	p, _ := s.submit(100_000, "proof")
	if _, err := s.submit(200_000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pay.Confirm(ctx, s.landlord, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ledger.Totals(ctx, []uuid.UUID{s.bill.ID, other.ID})
	if got[s.bill.ID] != (domain.Totals{ConfirmedVND: 100_000, PendingVND: 200_000}) || got[other.ID] != (domain.Totals{}) {
		t.Errorf("totals: %v", got)
	}

	// Marking the bill paid by hand writes down the money still owed.
	at := s.w.now
	if err := s.ledger.Settle(ctx, Settlement{InvoiceID: other.ID, ActorID: s.landlord, AmountVND: 300_000, At: at}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ledger.Totals(ctx, []uuid.UUID{other.ID})
	if got[other.ID].ConfirmedVND != 300_000 {
		t.Errorf("settled: %v", got)
	}
	if len(s.w.markPaid) != 0 || s.w.invoices[other.ID].facts.Status != domain.InvoiceSent {
		t.Error("the ledger only records money: the invoice sets its own status")
	}
	if last := s.w.audits[len(s.w.audits)-1]; last.Action != AuditRecorded || last.InvoiceID != other.ID || last.OwnerID != s.landlord {
		t.Errorf("audit: %+v", last)
	}

	// Reopened: the confirmed payment stays in the history but no longer counts.
	if err := s.ledger.Void(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ledger.Totals(ctx, []uuid.UUID{other.ID}); got[other.ID].ConfirmedVND != 0 {
		t.Errorf("voided money does not count: %v", got)
	}
	if n := countStatus(s.w, other.ID, domain.StatusVoided); n != 1 {
		t.Errorf("the history keeps it: %d", n)
	}

	// A deleted bill takes its payments and their proofs with it.
	rej, _ := s.submit(50_000, "x")
	_ = rej
	if err := s.ledger.Purge(ctx, s.bill.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.w.files) != 0 || countStatus(s.w, s.bill.ID, domain.StatusPending)+countStatus(s.w, s.bill.ID, domain.StatusConfirmed) != 0 {
		t.Errorf("purged: %d files", len(s.w.files))
	}
}

func countStatus(w *world, invoiceID uuid.UUID, st domain.Status) int {
	n := 0
	for _, p := range w.payments {
		if p.InvoiceID == invoiceID && p.Status == st {
			n++
		}
	}
	return n
}

func TestOpenProofNeedsAValidLinkAndAKnownFile(t *testing.T) {
	s := newScene(t)
	p, _ := s.submit(100_000, "picture")
	future := s.w.now.Add(time.Hour).Unix()

	rc, info, err := s.pay.OpenProof(ctx, p.ID, "0.webp", future, "ok")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "picture" || info.Size != 7 {
		t.Errorf("full picture: %q", body)
	}
	rc, _, err = s.pay.OpenProof(ctx, p.ID, "0_thumb.webp", future, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ = io.ReadAll(rc); string(body) != "thumb:picture" {
		t.Errorf("small copy: %q", body)
	}

	for name, call := range map[string]func() error{
		"tampered": func() error { _, _, err := s.pay.OpenProof(ctx, p.ID, "0.webp", future, "forged"); return err },
		"expired": func() error {
			_, _, err := s.pay.OpenProof(ctx, p.ID, "0.webp", s.w.now.Add(-time.Second).Unix(), "ok")
			return err
		},
		"no index":   func() error { _, _, err := s.pay.OpenProof(ctx, p.ID, "1.webp", future, "ok"); return err },
		"bad name":   func() error { _, _, err := s.pay.OpenProof(ctx, p.ID, "../0.webp", future, "ok"); return err },
		"no payment": func() error { _, _, err := s.pay.OpenProof(ctx, uuid.New(), "0.webp", future, "ok"); return err },
	} {
		if err := call(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A payment from before small copies existed shows the full picture instead.
	delete(s.w.files, domain.ThumbKey(p.ProofKeys[0]))
	rc, _, err = s.pay.OpenProof(ctx, p.ID, "0_thumb.webp", future, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ = io.ReadAll(rc); string(body) != "picture" {
		t.Errorf("fallback: %q", body)
	}
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	s := newScene(t)
	s.w.methods = map[string]domain.StoredMethod{}
	s.w.invoices[s.bill.ID].facts.RoomName = "P103"
	seeder := NewSeeder(s.w.deps(), Options{})

	made, err := seeder.SeedDemo(ctx, s.landlord, s.tenant)
	if err != nil || made != 3 {
		t.Fatalf("made %d, %v", made, err)
	}
	if countStatus(s.w, s.bill.ID, domain.StatusConfirmed) != 1 || countStatus(s.w, s.bill.ID, domain.StatusPending) != 1 {
		t.Error("one confirmed and one pending payment")
	}
	if made, err = seeder.SeedDemo(ctx, s.landlord, s.tenant); err != nil || made != 0 {
		t.Errorf("a second run does nothing: %d, %v", made, err)
	}
}

func TestAMarkPaidFailureUndoesNothingSilently(t *testing.T) {
	s := newScene(t)
	p, _ := s.submit(1_000_000)
	s.w.markErr = errors.New("boom")
	if _, err := s.pay.Confirm(ctx, s.landlord, p.ID); err == nil {
		t.Error("the failure of the bill's status change must surface, so the transaction is undone")
	}
}
