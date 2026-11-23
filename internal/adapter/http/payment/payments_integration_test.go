//go:build integration

package paymenthttp_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	paymenthttp "rental-management/internal/adapter/http/payment"
	domain "rental-management/internal/domain/payment"
)

type (
	paymentView      = paymenthttp.PaymentView
	methodView       = paymenthttp.MethodView
	instructionsView = paymenthttp.InstructionsView
)

func TestTenantPaysPartOfABillWithTwoProofsAndTheLandlordConfirmsIt(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	pic := pngBytes(t)

	w := e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "400000", "note": "first half", "payment_method": "manual_proof"}, pic, pic)
	expectStatus(t, w, http.StatusCreated)
	p := decodeData[paymentView](t, w)
	if p.Status != "pending" || p.AmountVND != 400_000 || p.Method != "manual_proof" || *p.Note != "first half" || len(p.Proofs) != 2 || p.Recorded ||
		p.Payer == nil || p.Payer.Name != "Tenant T" || p.Invoice.TotalVND != 1_000_000 || p.Invoice.RoomName != "P101" {
		t.Fatalf("payment: %+v", p)
	}
	if len(e.fileKeys()) != 4 {
		t.Errorf("two pictures and their small copies are stored: %v", e.fileKeys())
	}
	for _, key := range e.fileKeys() {
		if !strings.HasPrefix(key, "payments/") {
			t.Errorf("proofs live under the private prefix: %s", key)
		}
	}

	// The signed links open the picture and its small copy, with no sign in.
	for _, link := range []string{p.Proofs[0].URL, p.Proofs[1].ThumbnailURL} {
		w = e.get("", pathOf(t, link))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") || w.Body.Len() == 0 ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Cache-Control"), "private") {
			t.Errorf("proof %s: %d %s", link, w.Code, w.Header())
		}
	}

	// Another part payment is limited by what is left; the landlord sees both and confirms the first.
	expectError(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "700000"}), http.StatusUnprocessableEntity, "validation.failed")
	expectStatus(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "600000"}), http.StatusCreated)

	w = e.get("landlord-a", "/v1/payments?status=pending")
	expectStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"total":2`) {
		t.Errorf("two pending: %s", w.Body.String())
	}

	w = e.post("landlord-a", "/v1/payments/"+p.ID.String()+"/confirm", nil)
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[paymentView](t, w); got.Status != "confirmed" || got.PaidAt == nil || got.ReviewedAt == nil {
		t.Errorf("confirmed: %+v", got)
	}
	expectError(t, e.post("landlord-a", "/v1/payments/"+p.ID.String()+"/confirm", nil), http.StatusConflict, "payment.already_reviewed")
	expectError(t, e.post("landlord-a", "/v1/payments/"+p.ID.String()+"/reject", map[string]any{"reason": "late"}), http.StatusConflict, "payment.already_reviewed")
	expectError(t, e.call(http.MethodDelete, "/v1/payments/"+p.ID.String(), nil, "tenant-t"), http.StatusConflict, "payment.already_reviewed")
}

func (e *paymentEnv) fileKeys() []string {
	e.t.Helper()
	return e.memKeys("payments/")
}

func TestPaymentsCanBeSentAsJSONWithAmountOrAmountVND(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	w := e.post("tenant-t", "/v1/invoices/"+e.bill.String()+"/payments", map[string]any{"amount_vnd": 100_000, "note": "json"})
	expectStatus(t, w, http.StatusCreated)
	if p := decodeData[paymentView](t, w); p.AmountVND != 100_000 || len(p.Proofs) != 0 || p.Proofs == nil {
		t.Errorf("a payment with no proof has an empty list: %+v", p)
	}
	w = e.post("tenant-t", "/v1/invoices/"+e.bill.String()+"/payments", map[string]any{"amount": 200_000})
	expectStatus(t, w, http.StatusCreated)
	if p := decodeData[paymentView](t, w); p.AmountVND != 200_000 {
		t.Errorf("amount is accepted as amount_vnd: %+v", p)
	}
	expectError(t, e.post("tenant-t", "/v1/invoices/"+e.bill.String()+"/payments", map[string]any{"note": "no amount"}), http.StatusUnprocessableEntity, "validation.failed")
}

func TestSubmissionValidation(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	pic := pngBytes(t)
	field := func(w interface{ String() string }, name, rule string) {
		t.Helper()
		body := w.String()
		if !strings.Contains(body, `"`+name+`"`) {
			t.Errorf("the error names %s: %s", name, body)
		}
		_ = rule
	}

	w := e.pay("tenant-t", e.bill, map[string]string{})
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "amount_vnd", "required")

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "12.5"})
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "amount_vnd", "integer")

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "0"})
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "1000001"})
	body := expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	if msgs := body.Error.Fields["amount_vnd"]; len(msgs) != 1 || !strings.Contains(msgs[0], "1000000") {
		t.Errorf("the message says what is still due: %v", body.Error.Fields)
	}

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100", "payment_method": "bitcoin"})
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "payment_method", "")

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100", "payment_method": "momo"})
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "payment_method", "")

	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100"}, []byte("not a picture"))
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "proofs.0", "")

	expectError(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100"}, pic, pic, pic, pic), http.StatusUnprocessableEntity, "validation.failed")
	big := append(append([]byte{}, pic...), bytes.Repeat([]byte{0}, 70<<10)...)
	w = e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100"}, big)
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	field(w.Body, "proofs", "")

	expectError(t, e.do(http.MethodPost, "/v1/invoices/"+e.bill.String()+"/payments", "text/plain", strings.NewReader("x"), "tenant-t"), http.StatusBadRequest, "request.malformed")

	if len(e.fileKeys()) != 0 {
		t.Errorf("refused submissions leave no files: %v", e.fileKeys())
	}
	if w := e.get("landlord-a", "/v1/payments"); !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("and no payments: %s", w.Body.String())
	}
}

func TestAccessIsScopedToTheTwoSidesOfABill(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	p := decodeData[paymentView](t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100000"}))
	id := p.ID.String()
	billPath := "/v1/invoices/" + e.bill.String()

	// Another landlord, another tenant and the other roles never see or touch it.
	expectError(t, e.post("landlord-b", "/v1/payments/"+id+"/confirm", nil), http.StatusNotFound, "resource.not_found")
	expectError(t, e.post("landlord-b", "/v1/payments/"+id+"/reject", map[string]any{"reason": "x"}), http.StatusNotFound, "resource.not_found")
	expectError(t, e.call(http.MethodDelete, "/v1/payments/"+id, nil, "tenant-u"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.get("landlord-b", billPath+"/payments"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.get("tenant-u", billPath+"/payments"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.pay("tenant-u", e.bill, map[string]string{"amount_vnd": "100"}), http.StatusNotFound, "resource.not_found")
	expectError(t, e.get("tenant-u", billPath+"/payment-instructions"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.post("landlord-b", billPath+"/record-payment", map[string]any{"amount_vnd": 1}), http.StatusNotFound, "resource.not_found")
	if w := e.get("landlord-b", "/v1/payments"); !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("another landlord's list is empty: %s", w.Body.String())
	}

	// A role mismatch is a 403, a missing token a 401.
	expectError(t, e.post("tenant-t", "/v1/payments/"+id+"/confirm", nil), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.get("tenant-t", "/v1/payments"), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.pay("landlord-a", e.bill, map[string]string{"amount_vnd": "100"}), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.post("tenant-t", billPath+"/record-payment", map[string]any{"amount_vnd": 1}), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.get("admin", "/v1/payments"), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.get("", "/v1/payments"), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.get("tenant-t", "/v1/my/payment-methods"), http.StatusForbidden, "auth.forbidden")

	// The two sides see the payment.
	for _, who := range []string{"landlord-a", "tenant-t"} {
		if w := e.get(who, billPath+"/payments"); !strings.Contains(w.Body.String(), id) {
			t.Errorf("%s sees the payment: %s", who, w.Body.String())
		}
	}
}

func TestProofLinksAreSignedAndExpire(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	p := decodeData[paymentView](t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100000"}, pngBytes(t)))
	link := pathOf(t, p.Proofs[0].URL)
	if w := e.get("", link); w.Code != http.StatusOK {
		t.Fatalf("a valid link: %d", w.Code)
	}
	for name, path := range map[string]string{
		"tampered signature": strings.Replace(link, "signature=", "signature=00", 1),
		"another file":       strings.Replace(link, "0.webp", "1.webp", 1),
		"later expiry":       strings.Replace(link, "expires=", "expires=9", 1),
		"expired":            "/v1/payments/" + p.ID.String() + "/proofs/0.webp?expires=1&signature=" + strings.Repeat("a", 64),
		"no signature":       "/v1/payments/" + p.ID.String() + "/proofs/0.webp?expires=9999999999",
		"not a number":       "/v1/payments/" + p.ID.String() + "/proofs/0.webp?expires=x&signature=y",
		"another payment":    strings.Replace(link, p.ID.String(), uuid.NewString(), 1),
		"not a payment id":   "/v1/payments/nope/proofs/0.webp?expires=1&signature=a",
	} {
		expectError(t, e.get("", path), http.StatusNotFound, "resource.not_found")
		_ = name
	}
}

func TestRejectNeedsAReasonAndTheTenantCanWithdraw(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	a := decodeData[paymentView](t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100000"}, pngBytes(t)))
	b := decodeData[paymentView](t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "200000"}))

	expectError(t, e.post("landlord-a", "/v1/payments/"+a.ID.String()+"/reject", map[string]any{}), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.post("landlord-a", "/v1/payments/"+a.ID.String()+"/reject", map[string]any{"reason": "  "}), http.StatusUnprocessableEntity, "validation.failed")
	w := e.post("landlord-a", "/v1/payments/"+a.ID.String()+"/reject", map[string]any{"reason": "no money"})
	expectStatus(t, w, http.StatusOK)
	if got := decodeData[paymentView](t, w); got.Status != "rejected" || *got.ReviewNote != "no money" {
		t.Errorf("rejected: %+v", got)
	}

	w = e.call(http.MethodDelete, "/v1/payments/"+b.ID.String(), nil, "tenant-t")
	expectStatus(t, w, http.StatusNoContent)
	if w := e.get("tenant-t", "/v1/invoices/"+e.bill.String()+"/payments"); strings.Contains(w.Body.String(), b.ID.String()) {
		t.Errorf("a withdrawn payment is gone: %s", w.Body.String())
	}
	expectError(t, e.call(http.MethodDelete, "/v1/payments/"+uuid.NewString(), nil, "tenant-t"), http.StatusNotFound, "resource.not_found")
}

func TestListsAreCountedAndPaged(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	for range 3 {
		expectStatus(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "100000"}), http.StatusCreated)
	}
	for _, path := range []string{"/v1/payments?per_page=2", "/v1/invoices/" + e.bill.String() + "/payments?per_page=2"} {
		w := e.get("landlord-a", path)
		expectStatus(t, w, http.StatusOK)
		var body struct {
			Data []paymentView `json:"data"`
			Meta struct {
				Page    int `json:"page"`
				PerPage int `json:"per_page"`
				Total   int `json:"total"`
			} `json:"meta"`
		}
		decodeBody(t, w, &body)
		if len(body.Data) != 2 || body.Meta.Total != 3 || body.Meta.Page != 1 || body.Meta.PerPage != 2 {
			t.Errorf("%s: %d items, meta %+v", path, len(body.Data), body.Meta)
		}
	}
	expectError(t, e.get("landlord-a", "/v1/payments?status=paid"), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.get("landlord-a", "/v1/payments?per_page=1000"), http.StatusUnprocessableEntity, "validation.failed")
	if w := e.get("landlord-a", "/v1/payments?status=confirmed"); !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("none confirmed: %s", w.Body.String())
	}
}

func TestRecordingMoneyFromACashPaymentOrABlankContract(t *testing.T) {
	e := newPaymentEnv(t)
	path := "/v1/invoices/" + e.blank.String() + "/record-payment"
	w := e.post("landlord-a", path, map[string]any{"amount_vnd": 300_000, "note": "cash", "paid_on": time.Now().AddDate(0, 0, -1).Format(time.DateOnly)})
	expectStatus(t, w, http.StatusCreated)
	p := decodeData[paymentView](t, w)
	if p.Status != "confirmed" || !p.Recorded || p.Payer != nil || p.Method != "recorded" || p.AmountVND != 300_000 {
		t.Errorf("recorded: %+v", p)
	}
	expectError(t, e.post("landlord-a", path, map[string]any{"amount_vnd": 700_001}), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.post("landlord-a", path, map[string]any{"amount_vnd": 1, "paid_on": time.Now().AddDate(0, 0, 2).Format(time.DateOnly)}), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.post("landlord-a", path, map[string]any{"amount_vnd": 1, "paid_on": "yesterday"}), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.post("landlord-a", path, map[string]any{}), http.StatusUnprocessableEntity, "validation.failed")
	expectStatus(t, e.post("landlord-a", path, map[string]any{"amount": 700_000}), http.StatusCreated)
	var status string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1`, e.blank).Scan(&status); err != nil || status != "paid" {
		t.Errorf("the recorded money covers the bill: %s, %v", status, err)
	}
	expectError(t, e.post("landlord-a", path, map[string]any{"amount_vnd": 1}), http.StatusConflict, "payment.invoice_not_payable")

	// The record shows in the bill's payments of the landlord, and the tenant of the other bill sees nothing of it.
	if w := e.get("landlord-a", "/v1/invoices/"+e.blank.String()+"/payments"); !strings.Contains(w.Body.String(), `"total":2`) {
		t.Errorf("two recorded payments: %s", w.Body.String())
	}
}

func TestPaymentInstructionsForTheTenant(t *testing.T) {
	e := newPaymentEnv(t)
	path := "/v1/invoices/" + e.bill.String() + "/payment-instructions"
	if w := e.get("tenant-t", path); w.Body.String() != `{"data":[]}` {
		t.Errorf("no method accepted yet: %s", w.Body.String())
	}
	e.bank()
	expectStatus(t, e.call(http.MethodPut, "/v1/my/payment-methods/momo", map[string]any{
		"config": map[string]string{"phone": "0912345678", "account_name": "LANDLORD A"},
	}, "landlord-a"), http.StatusOK)

	list := decodeData[[]instructionsView](t, e.get("tenant-t", path))
	if len(list) != 2 || list[0].Method != "manual_proof" || list[0].AccountNumber != "0123456789" || list[0].AmountVND != 1_000_000 ||
		!strings.Contains(list[0].QRURL, "img.vietqr.io/image/VCB-0123456789-compact2.png") || !strings.HasPrefix(list[0].TransferContent, "HD ") ||
		list[1].Method != "momo" || list[1].Phone != "0912345678" || !strings.Contains(list[1].MoMoLink, "nhantien.momo.vn/0912345678/1000000") {
		t.Fatalf("instructions: %+v", list)
	}
	expectStatus(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "400000"}), http.StatusCreated)
	list = decodeData[[]instructionsView](t, e.get("tenant-t", path))
	if list[0].AmountVND != 600_000 {
		t.Errorf("the QR asks for what is left: %d", list[0].AmountVND)
	}
	if list = decodeData[[]instructionsView](t, e.get("tenant-t", path+"?amount_vnd=250000")); list[0].AmountVND != 250_000 {
		t.Errorf("or for the amount being paid: %d", list[0].AmountVND)
	}
	expectError(t, e.get("tenant-t", path+"?amount_vnd=600001"), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.get("tenant-t", path+"?amount_vnd=x"), http.StatusUnprocessableEntity, "validation.failed")
}

func TestPaymentMethodsOfALandlord(t *testing.T) {
	e := newPaymentEnv(t)
	list := decodeData[[]methodView](t, e.get("landlord-a", "/v1/my/payment-methods"))
	if len(list) != 2 || list[0].Method != "manual_proof" || list[0].Config != nil || list[0].IsActive || list[1].Method != "momo" {
		t.Fatalf("never saved: %+v", list)
	}

	cfg := map[string]string{"bank_code": "TCB", "bank_name": "Techcombank", "account_number": "19031234567890", "account_name": "NGUYEN VAN A", "ignored": "x"}
	w := e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{"config": cfg}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	saved := decodeData[methodView](t, w)
	if !saved.IsActive || saved.Config["account_number"] != "19031234567890" || saved.Config["ignored"] != "" {
		t.Errorf("saved: %+v", saved)
	}
	var sealed string
	if err := e.pool.QueryRow(context.Background(), `SELECT config_sealed FROM landlord_payment_methods WHERE landlord_id = $1`, e.landlA).Scan(&sealed); err != nil ||
		strings.Contains(sealed, "19031234567890") || strings.Contains(sealed, "TCB") {
		t.Errorf("the settings are encrypted at rest: %q, %v", sealed, err)
	}
	off := false
	w = e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{"config": cfg, "is_active": off}, "landlord-a")
	if got := decodeData[methodView](t, w); got.IsActive {
		t.Errorf("switched off: %+v", got)
	}
	// Another landlord's list is untouched by it.
	if other := decodeData[[]methodView](t, e.get("landlord-b", "/v1/my/payment-methods")); other[0].Config != nil {
		t.Errorf("settings are per landlord: %+v", other[0])
	}

	for name, body := range map[string]map[string]any{
		"empty config": {"config": map[string]string{}},
		"unknown bank": {"config": map[string]string{"bank_code": "XYZ", "account_number": "12345678", "account_name": "A"}},
		"bad account":  {"config": map[string]string{"bank_code": "VCB", "account_number": "12 34", "account_name": "A"}},
		"no config":    {},
	} {
		w := e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", body, "landlord-a")
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	w = e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{"config": map[string]string{}}, "landlord-a")
	body := expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	for _, f := range []string{"config.bank_code", "config.account_number", "config.account_name"} {
		if len(body.Error.Fields[f]) == 0 {
			t.Errorf("%s is reported: %v", f, body.Error.Fields)
		}
	}
	expectError(t, e.call(http.MethodPut, "/v1/my/payment-methods/bitcoin", map[string]any{"config": map[string]string{"a": "b"}}, "landlord-a"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.call(http.MethodDelete, "/v1/my/payment-methods/bitcoin", nil, "landlord-a"), http.StatusNotFound, "resource.not_found")

	expectStatus(t, e.call(http.MethodDelete, "/v1/my/payment-methods/manual_proof", nil, "landlord-a"), http.StatusNoContent)
	expectStatus(t, e.call(http.MethodDelete, "/v1/my/payment-methods/manual_proof", nil, "landlord-a"), http.StatusNoContent)
	if list = decodeData[[]methodView](t, e.get("landlord-a", "/v1/my/payment-methods")); list[0].Config != nil {
		t.Errorf("removed: %+v", list[0])
	}
	expectError(t, e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{"config": cfg}, "tenant-t"), http.StatusForbidden, "auth.forbidden")
}

func TestABillThatIsNotSentTakesNoPayment(t *testing.T) {
	e := newPaymentEnv(t)
	e.bank()
	draft := e.invoice(e.contractOf(e.bill), 2, "draft")
	expectError(t, e.pay("tenant-t", draft, map[string]string{"amount_vnd": "1"}), http.StatusNotFound, "resource.not_found")
	if _, err := e.pool.Exec(context.Background(), `UPDATE invoices SET status = 'paid', paid_at = now() WHERE id = $1`, e.bill); err != nil {
		t.Fatal(err)
	}
	expectError(t, e.pay("tenant-t", e.bill, map[string]string{"amount_vnd": "1"}), http.StatusConflict, "payment.invoice_not_payable")
	var _ = domain.StatusPending
}

func (e *paymentEnv) contractOf(invoice uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT contract_id FROM invoices WHERE id = $1`, invoice).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}
