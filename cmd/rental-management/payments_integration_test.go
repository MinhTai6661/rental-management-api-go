//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/config"
	"rental-management/internal/adapter/http/httpx"
	invoicehttp "rental-management/internal/adapter/http/invoice"
	paymenthttp "rental-management/internal/adapter/http/payment"
	"rental-management/internal/adapter/http/testenv"
	invoiceuc "rental-management/internal/usecase/invoice"
	"rental-management/pkg/authz"
	"rental-management/pkg/storage"
)

// The payment context is tested through the real composition code: the invoice use cases get the payment ledger
// (what is paid, marking a bill paid by hand, deleting a bill), and the payment use cases move a bill to paid through
// the invoice use cases. Both contexts are on the HTTP routes, with the property and contract routes of the bridge
// environment to set the contract up.

// payStack is the bridge environment with the invoice and payment routes mounted on the real use cases.
type payStack struct {
	*bridgeEnv
	files    *storage.Memory
	contract uuid.UUID
	bill     invoiceRow
}

type invoiceRow struct {
	ID             uuid.UUID `json:"id"`
	Status         string    `json:"status"`
	TotalVND       int64     `json:"total_amount_vnd"`
	PaidAt         *string   `json:"paid_at"`
	PaymentSummary struct {
		PaidVND      int64 `json:"paid_vnd"`
		PendingVND   int64 `json:"pending_vnd"`
		RemainingVND int64 `json:"remaining_vnd"`
	} `json:"payment_summary"`
	Abilities struct {
		Update bool `json:"update"`
		Delete bool `json:"delete"`
	} `json:"abilities"`
}

type paymentRow struct {
	ID        uuid.UUID `json:"id"`
	Status    string    `json:"status"`
	Method    string    `json:"method"`
	AmountVND int64     `json:"amount_vnd"`
	Recorded  bool      `json:"recorded"`
	Proofs    []struct {
		URL          string `json:"url"`
		ThumbnailURL string `json:"thumbnail_url"`
	} `json:"proofs"`
}

// newPayStack mounts the routes, sets up the landlord's bank transfer and gives the demo tenant a running contract with a
// sent bill of 2,000,000 VND.
func newPayStack(t *testing.T) *payStack {
	t.Helper()
	e := &payStack{bridgeEnv: newBridgeEnv(t), files: storage.NewMemory("http://files.test/v1/media")}
	cfg := config.Config{
		DataEncryptionKey: testDataKey, APIPublicURL: "https://api.test", PaymentProofMaxFiles: 3, PaymentProofMaxSizeKB: 64,
		PaymentProofURLTTLMinutes: 120, MediaMaxDimension: 2048, MediaThumbnailMaxSize: 64, ImageProcessingConcurrency: 2,
	}
	sealer, err := newSealer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	billing, err := newInvoices(cfg, slog.New(slog.DiscardHandler), e.pool, e.plans, sealer, nil)
	if err != nil {
		t.Fatal(err)
	}
	paying, err := newPayments(cfg, slog.New(slog.DiscardHandler), e.pool, e.files, sealer, billing, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := fakeAuth{
		"landlord":  principalOf(e.landlord, authz.RoleLandlord),
		"landlord2": principalOf(e.landlord2, authz.RoleLandlord),
		"tenant":    principalOf(e.tenant, authz.RoleTenant),
	}
	binder := httpx.NewBinder()
	v1 := e.engine.Group("/v1")
	invoicehttp.MountInvoices(v1, invoicehttp.NewInvoiceHandler(billing.invoices, binder, invoiceuc.ClockFunc(time.Now), nil),
		invoicehttp.NewMeterHandler(billing.invoices), invoicehttp.NewPublicHandler(billing.public), auth, allowAll{})
	paymenthttp.MountPayments(v1,
		paymenthttp.NewPaymentHandler(paying.payments, binder, paymenthttp.Limits{MaxFiles: paying.limits.maxFiles, MaxBytes: paying.limits.maxBytes}),
		paymenthttp.NewMethodHandler(paying.methods, binder), paymenthttp.NewProofHandler(paying.payments), auth, allowAll{})

	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{
		"config": map[string]string{"bank_code": "VCB", "account_number": "0123456789", "account_name": "DEMO LANDLORD"},
	}, "landlord"), http.StatusOK)

	room := e.newRoom("R1", "available")
	e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format(time.DateOnly)}, "tenant")
	testenv.ExpectStatus(t, reg, http.StatusCreated)
	e.contract = testenv.DecodeData[contractRow](t, reg).ID
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/contracts/"+e.contract.String(), map[string]any{"status": "active"}, "landlord"), http.StatusOK)
	e.bill = e.sendBill()
	return e
}

// sendBill makes the next bill of the contract and sends it to the tenant.
func (e *payStack) sendBill() invoiceRow {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/contracts/"+e.contract.String()+"/invoices", map[string]any{}, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusCreated)
	draft := testenv.DecodeData[invoiceRow](e.t, w)
	w = e.call(http.MethodPost, "/v1/invoices/"+draft.ID.String()+"/send", nil, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[invoiceRow](e.t, w)
}

func (e *payStack) invoice(bearer string) invoiceRow {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/invoices/"+e.bill.ID.String(), nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[invoiceRow](e.t, w)
}

func (e *payStack) payments(bearer string, query string) []paymentRow {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/invoices/"+e.bill.ID.String()+"/payments"+query, nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[[]paymentRow](e.t, w)
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pay sends a multipart payment of the tenant on the bill, with the pictures as proofs.
func (e *payStack) pay(amount int64, pictures ...[]byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("amount_vnd", fmt.Sprint(amount))
	for i, p := range pictures {
		part, _ := w.CreateFormFile("proofs[]", fmt.Sprintf("p%d.png", i))
		_, _ = part.Write(p)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/invoices/"+e.bill.ID.String()+"/payments", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer tenant")
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func (e *payStack) confirm(id uuid.UUID) { e.t.Helper(); e.review(id, "confirm", nil) }

func (e *payStack) review(id uuid.UUID, action string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.call(http.MethodPost, "/v1/payments/"+id.String()+"/"+action, body, "landlord")
}

func (e *payStack) count(query string, args ...any) (n int) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestATenantPayingTheWholeBillSettlesItWhenTheLandlordConfirms(t *testing.T) {
	e := newPayStack(t)
	if e.bill.TotalVND != 2_000_000 || e.bill.Status != "sent" {
		t.Fatalf("the bill: %+v", e.bill)
	}
	w := e.pay(2_000_000, smallPNG(t))
	testenv.ExpectStatus(t, w, http.StatusCreated)
	p := testenv.DecodeData[paymentRow](t, w)

	// Waiting for review: the bill is still sent, the money is pending, nothing is left to ask, and it cannot be changed.
	inv := e.invoice("tenant")
	if inv.Status != "sent" || inv.PaymentSummary.PendingVND != 2_000_000 || inv.PaymentSummary.RemainingVND != 0 || inv.PaymentSummary.PaidVND != 0 {
		t.Errorf("pending: %+v", inv)
	}
	if lk := e.invoice("landlord"); lk.Abilities.Update || lk.Abilities.Delete {
		t.Errorf("a bill with money waiting is locked: %+v", lk.Abilities)
	}
	testenv.ExpectError(t, e.call(http.MethodPatch, "/v1/invoices/"+e.bill.ID.String(), map[string]any{"status": "paid"}, "landlord"), http.StatusConflict, "invoice.review_pending_first")
	testenv.ExpectError(t, e.call(http.MethodPut, "/v1/invoices/"+e.bill.ID.String(), map[string]any{}, "landlord"), http.StatusConflict, "invoice.locked")

	e.confirm(p.ID)
	inv = e.invoice("tenant")
	if inv.Status != "paid" || inv.PaidAt == nil || inv.PaymentSummary.PaidVND != 2_000_000 || inv.PaymentSummary.PendingVND != 0 {
		t.Errorf("confirmed money settles the bill: %+v", inv)
	}
	if e.count(`SELECT count(*) FROM payments WHERE status = 'confirmed' AND method = 'manual_proof'`) != 1 {
		t.Error("the tenant's payment is the one that counts (no extra recorded payment)")
	}
	testenv.ExpectError(t, e.pay(1), http.StatusConflict, "payment.invoice_not_payable")
}

func TestPartPaymentsAddUpAndARejectedOneFreesTheMoney(t *testing.T) {
	e := newPayStack(t)
	first := testenv.DecodeData[paymentRow](t, e.pay(800_000))
	second := testenv.DecodeData[paymentRow](t, e.pay(1_200_000))
	testenv.ExpectError(t, e.pay(1), http.StatusUnprocessableEntity, "validation.failed")

	e.confirm(first.ID)
	inv := e.invoice("landlord")
	if inv.Status != "sent" || inv.PaymentSummary.PaidVND != 800_000 || inv.PaymentSummary.PendingVND != 1_200_000 || inv.PaymentSummary.RemainingVND != 0 {
		t.Errorf("part confirmed: %+v", inv)
	}

	testenv.ExpectStatus(t, e.review(second.ID, "reject", map[string]any{"reason": "money did not arrive"}), http.StatusOK)
	inv = e.invoice("landlord")
	if inv.Status != "sent" || inv.PaymentSummary.PaidVND != 800_000 || inv.PaymentSummary.PendingVND != 0 || inv.PaymentSummary.RemainingVND != 1_200_000 {
		t.Errorf("the rejected money is due again: %+v", inv)
	}
	third := testenv.DecodeData[paymentRow](t, e.pay(1_200_000))
	e.confirm(third.ID)
	if inv = e.invoice("tenant"); inv.Status != "paid" || inv.PaymentSummary.PaidVND != 2_000_000 {
		t.Errorf("paid in two parts: %+v", inv)
	}
	if got := e.payments("landlord", ""); len(got) != 3 || got[0].ID != first.ID {
		t.Errorf("the history keeps the three, oldest first: %+v", got)
	}
}

func TestMarkingABillPaidByHandIsRecordedAndReopeningVoidsIt(t *testing.T) {
	e := newPayStack(t)
	patch := func(status string) *httptest.ResponseRecorder {
		return e.call(http.MethodPatch, "/v1/invoices/"+e.bill.ID.String(), map[string]any{"status": status}, "landlord")
	}
	testenv.ExpectStatus(t, patch("paid"), http.StatusOK)

	got := e.payments("landlord", "")
	if len(got) != 1 || got[0].Status != "confirmed" || got[0].Method != "recorded" || !got[0].Recorded || got[0].AmountVND != 2_000_000 {
		t.Fatalf("marking it paid records the money that came in: %+v", got)
	}
	if inv := e.invoice("tenant"); inv.Status != "paid" || inv.PaymentSummary.PaidVND != 2_000_000 {
		t.Errorf("it counts as collected: %+v", inv)
	}
	if w := e.call(http.MethodGet, "/v1/payments?status=confirmed", nil, "landlord"); !strings.Contains(w.Body.String(), `"total":1`) {
		t.Errorf("the landlord's confirmed list: %s", w.Body.String())
	}

	// Reopened: the recorded payment no longer counts and the tenant can pay again.
	testenv.ExpectStatus(t, patch("sent"), http.StatusOK)
	if got = e.payments("landlord", ""); len(got) != 1 || got[0].Status != "voided" {
		t.Errorf("a voided payment stays in the history: %+v", got)
	}
	if inv := e.invoice("tenant"); inv.Status != "sent" || inv.PaymentSummary.PaidVND != 0 || inv.PaymentSummary.RemainingVND != 2_000_000 {
		t.Errorf("reopened: %+v", inv)
	}
	testenv.ExpectStatus(t, e.pay(500_000), http.StatusCreated)
}

func TestDeletingABillWithOnlyRejectedPaymentsRemovesTheirProofs(t *testing.T) {
	e := newPayStack(t)
	p := testenv.DecodeData[paymentRow](t, e.pay(500_000, smallPNG(t), smallPNG(t)))
	if n := len(e.files.Keys()); n != 4 {
		t.Fatalf("two proofs and their small copies: %d", n)
	}
	testenv.ExpectError(t, e.call(http.MethodDelete, "/v1/invoices/"+e.bill.ID.String(), nil, "landlord"), http.StatusConflict, "invoice.locked")

	testenv.ExpectStatus(t, e.review(p.ID, "reject", map[string]any{"reason": "wrong amount"}), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/invoices/"+e.bill.ID.String(), nil, "landlord"), http.StatusNoContent)
	if e.count(`SELECT count(*) FROM payments`) != 0 {
		t.Error("the payments go with the bill")
	}
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("and so do their proof pictures: %v", keys)
	}
}

func TestProofsAreOnlyReachedThroughTheirSignedLinks(t *testing.T) {
	e := newPayStack(t)
	p := testenv.DecodeData[paymentRow](t, e.pay(500_000, smallPNG(t)))
	link := func(raw string) string {
		u, _ := url.Parse(raw)
		return u.RequestURI()
	}
	for _, raw := range []string{p.Proofs[0].URL, p.Proofs[0].ThumbnailURL} {
		w := e.call(http.MethodGet, link(raw), nil, "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
			t.Errorf("%s: %d", raw, w.Code)
		}
	}
	testenv.ExpectError(t, e.call(http.MethodGet, strings.Replace(link(p.Proofs[0].URL), "signature=", "signature=0", 1), nil, ""), http.StatusNotFound, "resource.not_found")

	// The public picture route serves property pictures only: a proof key is never reachable through it.
	for _, key := range e.files.Keys() {
		testenv.ExpectError(t, e.call(http.MethodGet, "/v1/media/"+key, nil, ""), http.StatusNotFound, "resource.not_found")
	}
}

func TestRecordingMoneyOnABlankContractThroughTheRealBridge(t *testing.T) {
	e := newPayStack(t)
	room := e.newRoom("R2", "rented") // a rented room nobody holds: a blank contract runs on it
	blank := e.contractsOf(room.ID)[0]
	w := e.call(http.MethodPost, "/v1/contracts/"+blank.ID.String()+"/invoices", map[string]any{}, "landlord")
	testenv.ExpectStatus(t, w, http.StatusCreated)
	draft := testenv.DecodeData[invoiceRow](t, w)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/invoices/"+draft.ID.String()+"/send", nil, "landlord"), http.StatusOK)

	path := "/v1/invoices/" + draft.ID.String() + "/record-payment"
	testenv.ExpectStatus(t, e.call(http.MethodPost, path, map[string]any{"amount_vnd": 500_000, "note": "cash"}, "landlord"), http.StatusCreated)
	testenv.ExpectStatus(t, e.call(http.MethodPost, path, map[string]any{"amount_vnd": draft.TotalVND - 500_000}, "landlord"), http.StatusCreated)
	w = e.call(http.MethodGet, "/v1/invoices/"+draft.ID.String(), nil, "landlord")
	if inv := testenv.DecodeData[invoiceRow](t, w); inv.Status != "paid" || inv.PaymentSummary.PaidVND != draft.TotalVND {
		t.Errorf("the recorded money settles the bill through the invoice use case: %+v", inv)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, path, map[string]any{"amount_vnd": 500_000}, "landlord2"), http.StatusNotFound, "resource.not_found")
}

func TestSeedDemoGivesTheDemoBillPaymentsAndIsSafeToRepeat(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	cfg := config.Config{
		DataEncryptionKey: testDataKey, APIPublicURL: "https://api.test", PaymentProofMaxFiles: 3, PaymentProofMaxSizeKB: 64,
		PaymentProofURLTTLMinutes: 120, MediaMaxDimension: 2048, MediaThumbnailMaxSize: 64, ImageProcessingConcurrency: 2,
	}
	sealer, err := newSealer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	billing, err := newInvoices(cfg, slog.New(slog.DiscardHandler), e.pool, e.plans, sealer, nil)
	if err != nil {
		t.Fatal(err)
	}
	paying, err := newPayments(cfg, slog.New(slog.DiscardHandler), e.pool, storage.NewMemory("http://files.test/v1/media"), sealer, billing, nil)
	if err != nil {
		t.Fatal(err)
	}
	made := 0
	for range 2 {
		if err := e.props.seeder.SeedDemo(ctx, e.landlord); err != nil {
			t.Fatal(err)
		}
		if _, err := e.offers.seeder.SeedDemo(ctx, e.landlord, &e.admin); err != nil {
			t.Fatal(err)
		}
		if err := e.rentals.seeder.SeedDemo(ctx, e.landlord, e.tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := billing.seeder.SeedDemo(ctx, e.landlord); err != nil {
			t.Fatal(err)
		}
		n, err := paying.seeder.SeedDemo(ctx, e.landlord, e.tenant)
		if err != nil {
			t.Fatal(err)
		}
		made += n
	}
	if made != 3 {
		t.Errorf("a bank transfer and two payments the first time, nothing the second: %d", made)
	}
	count := func(where string) (n int) {
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE `+where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The paid demo bill was settled through the ledger (recorded), the sent one has a confirmed and a pending part.
	if count("status = 'confirmed' AND method = 'recorded'") != 1 || count("status = 'confirmed' AND method = 'manual_proof'") != 1 || count("status = 'pending'") != 1 {
		t.Errorf("demo payments: recorded %d, confirmed %d, pending %d", count("status = 'confirmed' AND method = 'recorded'"),
			count("status = 'confirmed' AND method = 'manual_proof'"), count("status = 'pending'"))
	}
	var methods int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM landlord_payment_methods WHERE is_active`).Scan(&methods); err != nil || methods != 1 {
		t.Errorf("the demo landlord's bank transfer: %d, %v", methods, err)
	}
	var _ = json.Marshal
}

type rentalBilling struct {
	DueCount     int        `json:"due_count"`
	DueAmountVND int64      `json:"due_amount_vnd"`
	Partial      bool       `json:"partial"`
	ReviewCount  int        `json:"review_count"`
	Overdue      bool       `json:"overdue"`
	NextDueAt    *time.Time `json:"next_due_at"`
}

type rentalList struct {
	Data []struct {
		ID      uuid.UUID     `json:"id"`
		Billing rentalBilling `json:"billing"`
	} `json:"data"`
	Meta struct {
		Total         int `json:"total"`
		PaymentCounts struct {
			All     int `json:"all"`
			Due     int `json:"due"`
			Review  int `json:"review"`
			Settled int `json:"settled"`
		} `json:"payment_counts"`
	} `json:"meta"`
}

func (e *payStack) rentals(query string) rentalList {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/me/contracts"+query, nil, "tenant")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	var out rentalList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

type landlordList struct {
	Data []struct {
		ID      uuid.UUID `json:"id"`
		Billing struct {
			UnpaidInvoices int `json:"unpaid_invoices"`
		} `json:"billing"`
	} `json:"data"`
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
}

func (e *payStack) landlordContracts(query string) landlordList {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/contracts"+query, nil, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	var out landlordList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestTheRentersListCarriesWhatIsDueAndFiltersByTheStateOfMoney(t *testing.T) {
	e := newPayStack(t)

	all := e.rentals("?group=active")
	b := all.Data[0].Billing
	if len(all.Data) != 1 || b.DueCount != 1 || b.DueAmountVND != 2_000_000 || b.Partial || b.ReviewCount != 0 || b.Overdue || b.NextDueAt == nil {
		t.Fatalf("a sent bill is due: %+v", all.Data)
	}
	if c := all.Meta.PaymentCounts; c.All != 1 || c.Due != 1 || c.Review != 0 || c.Settled != 0 {
		t.Errorf("payment counts: %+v", c)
	}

	// 800,000 sent: part of the bill is still due, and a payment waits for the landlord.
	first := testenv.DecodeData[paymentRow](t, e.pay(800_000))
	b = e.rentals("?group=active").Data[0].Billing
	if b.DueCount != 1 || b.DueAmountVND != 1_200_000 || !b.Partial || b.ReviewCount != 1 {
		t.Errorf("after a part payment: %+v", b)
	}
	for query, want := range map[string]int{"?payment=due": 1, "?payment=review": 1, "?payment=settled": 0, "?group=history&payment=due": 0} {
		if got := e.rentals(query).Meta.Total; got != want {
			t.Errorf("%s: %d, want %d", query, got, want)
		}
	}
	if c := e.rentals("?payment=settled").Meta.PaymentCounts; c.All != 1 || c.Due != 1 || c.Review != 1 || c.Settled != 0 {
		t.Errorf("the counts ignore the filter: %+v", c)
	}

	// All the money sent and confirmed: the bill is paid and the contract is settled.
	e.confirm(first.ID)
	second := testenv.DecodeData[paymentRow](t, e.pay(1_200_000))
	if b = e.rentals("").Data[0].Billing; b.DueCount != 0 || b.DueAmountVND != 0 || b.Partial || b.ReviewCount != 1 || b.NextDueAt != nil {
		t.Errorf("everything is sent, waiting for the landlord: %+v", b)
	}
	e.confirm(second.ID)
	if b = e.rentals("").Data[0].Billing; b != (rentalBilling{}) {
		t.Errorf("nothing is due any more: %+v", b)
	}
	if got := e.rentals("?payment=settled"); got.Meta.Total != 1 || got.Meta.PaymentCounts.Settled != 1 || got.Meta.PaymentCounts.Due != 0 {
		t.Errorf("settled: %+v", got.Meta)
	}
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/me/contracts?payment=late", nil, "tenant"), http.StatusUnprocessableEntity, "validation.failed")

	// The detail of a rental carries the summary too.
	w := e.call(http.MethodGet, "/v1/me/contracts/"+e.contract.String(), nil, "tenant")
	if !strings.Contains(w.Body.String(), `"billing":{"due_count":0`) {
		t.Errorf("detail: %s", w.Body.String())
	}
}

func TestTheLandlordsListCountsUnpaidBillsAndFiltersByThem(t *testing.T) {
	e := newPayStack(t)
	if got := e.landlordContracts("?has_unpaid_invoice=true"); got.Meta.Total != 1 || got.Data[0].Billing.UnpaidInvoices != 1 {
		t.Fatalf("a sent bill is unpaid: %+v", got)
	}
	// Paid by hand: nothing is unpaid, so the filter keeps nothing while the plain list still shows the contract.
	testenv.ExpectStatus(t, e.call(http.MethodPatch, "/v1/invoices/"+e.bill.ID.String(), map[string]any{"status": "paid"}, "landlord"), http.StatusOK)
	if got := e.landlordContracts("?has_unpaid_invoice=true"); got.Meta.Total != 0 {
		t.Errorf("paid: %+v", got)
	}
	if got := e.landlordContracts(""); got.Meta.Total != 1 || got.Data[0].Billing.UnpaidInvoices != 0 {
		t.Errorf("the plain list: %+v", got)
	}
	// A draft is unpaid too (the landlord still has to send and collect it): on the blank contract of another room.
	blank := e.contractsOf(e.newRoom("R2", "rented").ID)[0]
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/contracts/"+blank.ID.String()+"/invoices", map[string]any{}, "landlord"), http.StatusCreated)
	if got := e.landlordContracts("?has_unpaid_invoice=true"); got.Meta.Total != 1 || got.Data[0].ID != blank.ID || got.Data[0].Billing.UnpaidInvoices != 1 {
		t.Errorf("a draft: %+v", got)
	}
	if got := e.landlordContracts("?has_unpaid_invoice=false"); got.Meta.Total != 2 {
		t.Errorf("false is no filter: %+v", got)
	}
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/contracts?has_unpaid_invoice=maybe", nil, "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	// Another landlord sees none of it.
	w := e.call(http.MethodGet, "/v1/contracts?has_unpaid_invoice=true", nil, "landlord2")
	if !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("another landlord: %s", w.Body.String())
	}
}
