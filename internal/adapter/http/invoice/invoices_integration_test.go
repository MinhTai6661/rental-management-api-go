//go:build integration

package invoicehttp_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	invoicehttp "rental-management/internal/adapter/http/invoice"
)

type (
	invoiceView = invoicehttp.InvoiceView
	linkView    = invoicehttp.LinkView
)

var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// fixture is a landlord A room with a running contract of tenant T that started two months ago (three periods), an
// electricity meter and a fixed internet charge.
type fixture struct {
	room, contract, power, internet uuid.UUID
}

func (e *invoiceEnv) fixture() fixture {
	e.t.Helper()
	room := e.room(e.landlA, "P101")
	return fixture{
		room: room, contract: e.contract(e.landlA, &e.tenantT, room, 2),
		power:    e.charge(room, "Electricity", "electricity", "per_kwh", 3500),
		internet: e.charge(room, "Internet", "internet", "per_period", 100000),
	}
}

func (f fixture) readings(value float64) []map[string]any {
	return []map[string]any{{"charge_id": f.power, "reading_value": value, "previous_value": 100}}
}

func TestABillGoesFromDraftToSentToPaidAndTheTenantSeesItOnlyWhenSent(t *testing.T) {
	e := newInvoiceEnv(t)
	f := e.fixture()
	path := "/v1/contracts/" + f.contract.String() + "/invoices"

	// The preview adds nothing: no bill, no meter history.
	w := e.post("landlord-a", path+"/preview", map[string]any{"readings": f.readings(150)})
	expectStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "00000000-0000") {
		t.Errorf("a preview has no ids: %s", w.Body.String())
	}
	if n := e.get("landlord-a", "/v1/rooms/"+f.room.String()+"/meter-readings").Body.String(); !strings.Contains(n, `"total":0`) {
		t.Errorf("a preview records nothing: %s", n)
	}

	// 3,000,000 rent + 50 kWh * 3,500 + 100,000 internet.
	w = e.post("landlord-a", path, map[string]any{"readings": f.readings(150), "note": "Thanks"})
	expectStatus(t, w, http.StatusCreated)
	draft := decodeData[invoiceView](t, w)
	if draft.Status != "draft" || draft.TotalVND != 3_275_000 || draft.Delivery != "app" || len(draft.Items) != 3 || draft.SentAt != nil {
		t.Fatalf("a new bill is a draft: %+v", draft)
	}
	id := draft.ID.String()

	// One bill per period, and a draft is the landlord's alone.
	expectError(t, e.post("landlord-a", path, map[string]any{"period_start": draft.PeriodStart, "readings": f.readings(150)}), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.get("tenant-t", "/v1/invoices/"+id), http.StatusNotFound, "resource.not_found")
	if body := e.get("tenant-t", path).Body.String(); strings.Contains(body, id) {
		t.Errorf("the tenant's list shows a draft: %s", body)
	}
	expectError(t, e.post("landlord-a", "/v1/invoices/"+id+"/share-link", nil), http.StatusConflict, "invoice.link_needs_sent")

	// Correct it, then send it.
	w = e.call(http.MethodPut, "/v1/invoices/"+id, map[string]any{"note": "Edited", "readings": f.readings(160)}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if v := decodeData[invoiceView](t, w); v.TotalVND != 3_310_000 || *v.Note != "Edited" {
		t.Errorf("the corrected bill: %+v", v)
	}
	expectStatus(t, e.post("landlord-a", "/v1/invoices/"+id+"/send", map[string]any{}), http.StatusOK)
	if got := decodeData[invoiceView](t, e.get("tenant-t", "/v1/invoices/"+id)); got.Status != "sent" || got.Abilities.Update {
		t.Errorf("the tenant sees it, and can do nothing with it: %+v", got)
	}
	if len(e.notes.notes) == 0 {
		t.Error("the tenant is told")
	}

	// Paid and unpaid again; a paid bill is not corrected or removed.
	expectStatus(t, e.call(http.MethodPatch, "/v1/invoices/"+id, map[string]any{"status": "paid"}, "landlord-a"), http.StatusOK)
	expectError(t, e.call(http.MethodPut, "/v1/invoices/"+id, map[string]any{"note": "x"}, "landlord-a"), http.StatusConflict, "invoice.locked")
	expectError(t, e.call(http.MethodDelete, "/v1/invoices/"+id, nil, "landlord-a"), http.StatusConflict, "invoice.locked")
	expectStatus(t, e.call(http.MethodPatch, "/v1/invoices/"+id, map[string]any{"status": "sent"}, "landlord-a"), http.StatusOK)

	// Deleting a bill takes its meter readings with it, so the period can be billed again.
	expectStatus(t, e.call(http.MethodDelete, "/v1/invoices/"+id, nil, "landlord-a"), http.StatusNoContent)
	expectError(t, e.get("landlord-a", "/v1/invoices/"+id), http.StatusNotFound, "resource.not_found")
}

func TestNobodyElseTouchesABill(t *testing.T) {
	e := newInvoiceEnv(t)
	f := e.fixture()
	w := e.post("landlord-a", "/v1/contracts/"+f.contract.String()+"/invoices", map[string]any{"readings": f.readings(120)})
	expectStatus(t, w, http.StatusCreated)
	id := decodeData[invoiceView](t, w).ID.String()
	expectStatus(t, e.post("landlord-a", "/v1/invoices/"+id+"/send", map[string]any{}), http.StatusOK)

	for name, bearer := range map[string]string{"another landlord": "landlord-b", "another tenant": "tenant-u"} {
		expectError(t, e.get(bearer, "/v1/invoices/"+id), http.StatusNotFound, "resource.not_found")
		expectError(t, e.get(bearer, "/v1/contracts/"+f.contract.String()+"/invoices"), http.StatusNotFound, "resource.not_found")
		_ = name
	}
	expectError(t, e.call(http.MethodPut, "/v1/invoices/"+id, map[string]any{"note": "x"}, "landlord-b"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.call(http.MethodDelete, "/v1/invoices/"+id, nil, "landlord-b"), http.StatusNotFound, "resource.not_found")
	expectError(t, e.post("landlord-b", "/v1/invoices/"+id+"/share-link", nil), http.StatusNotFound, "resource.not_found")
	expectError(t, e.get("landlord-b", "/v1/rooms/"+f.room.String()+"/meter-readings"), http.StatusNotFound, "resource.not_found")

	// A tenant reads but never writes; an admin has no bills at all; nobody anonymous gets in.
	expectError(t, e.call(http.MethodPut, "/v1/invoices/"+id, map[string]any{"note": "x"}, "tenant-t"), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.post("tenant-t", "/v1/invoices/"+id+"/send", map[string]any{}), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.post("tenant-t", "/v1/contracts/"+f.contract.String()+"/invoices", map[string]any{}), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.get("admin", "/v1/invoices/"+id), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.get("", "/v1/invoices/"+id), http.StatusUnauthorized, "auth.unauthenticated")
}

func TestTheLinkOfASentBillShowsItWithoutIdsAndCanBeReplacedOrWithdrawn(t *testing.T) {
	e := newInvoiceEnv(t)
	room := e.room(e.landlA, "P201")
	blank := e.contract(e.landlA, nil, room, 0) // nobody with an account holds it: the bill goes by link
	w := e.post("landlord-a", "/v1/contracts/"+blank.String()+"/invoices", map[string]any{})
	expectStatus(t, w, http.StatusCreated)
	bill := decodeData[invoiceView](t, w)
	if bill.Delivery != "link" || bill.HasAccount {
		t.Fatalf("a contract nobody holds is billed by link: %+v", bill)
	}
	id := bill.ID.String()
	expectStatus(t, e.post("landlord-a", "/v1/invoices/"+id+"/send", map[string]any{}), http.StatusOK)

	first := decodeData[linkView](t, e.post("landlord-a", "/v1/invoices/"+id+"/share-link", nil))
	again := decodeData[linkView](t, e.post("landlord-a", "/v1/invoices/"+id+"/share-link", nil))
	if first.URL != again.URL || !strings.HasPrefix(first.URL, "https://app.example.vn/bill?token=") {
		t.Fatalf("the same link comes back: %q %q", first.URL, again.URL)
	}
	token := strings.TrimPrefix(first.URL, "https://app.example.vn/bill?token=")

	// Nobody needs to sign in, and nothing in the page is an id.
	w = e.get("", "/v1/public/invoices/"+token)
	expectStatus(t, w, http.StatusOK)
	if uuidPattern.MatchString(w.Body.String()) {
		t.Errorf("the public bill must carry no ids: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), bill.InvoiceNumber) {
		t.Errorf("the public bill: %s", w.Body.String())
	}

	// A new link kills the old one; a withdrawn link works for nobody; a made-up one says the same as a dead one.
	fresh := decodeData[linkView](t, e.post("landlord-a", "/v1/invoices/"+id+"/share-link/regenerate", nil))
	expectError(t, e.get("", "/v1/public/invoices/"+token), http.StatusNotFound, "invoice.link_invalid")
	newToken := strings.TrimPrefix(fresh.URL, "https://app.example.vn/bill?token=")
	expectStatus(t, e.get("", "/v1/public/invoices/"+newToken), http.StatusOK)
	expectStatus(t, e.call(http.MethodDelete, "/v1/invoices/"+id+"/share-link", nil, "landlord-a"), http.StatusNoContent)
	expectError(t, e.get("", "/v1/public/invoices/"+newToken), http.StatusNotFound, "invoice.link_invalid")
	expectError(t, e.get("", "/v1/public/invoices/"+strings.Repeat("a", 64)), http.StatusNotFound, "invoice.link_invalid")
	expectError(t, e.get("", "/v1/public/invoices/short"), http.StatusNotFound, "invoice.link_invalid")
}

func TestBillsAreListedAPageAtATimeNewestFirst(t *testing.T) {
	e := newInvoiceEnv(t)
	f := e.fixture()
	path := "/v1/contracts/" + f.contract.String() + "/invoices"
	// Three periods: the first bill carries the meter's starting index, the next ones follow on.
	expectStatus(t, e.post("landlord-a", path, map[string]any{"readings": f.readings(110)}), http.StatusCreated)
	for _, v := range []float64{120, 130} {
		expectStatus(t, e.post("landlord-a", path, map[string]any{"readings": []map[string]any{{"charge_id": f.power, "reading_value": v}}}), http.StatusCreated)
	}
	// Everything is billed now: nothing is left to bill.
	expectError(t, e.post("landlord-a", path, map[string]any{}), http.StatusUnprocessableEntity, "validation.failed")

	var page struct {
		Data []invoiceView `json:"data"`
		Meta struct {
			Total   int  `json:"total"`
			HasMore bool `json:"has_more"`
		} `json:"meta"`
	}
	w := e.get("landlord-a", path+"?limit=2")
	expectStatus(t, w, http.StatusOK)
	decodeInto(t, w, &page)
	if len(page.Data) != 2 || page.Meta.Total != 3 || !page.Meta.HasMore || page.Data[0].PeriodStart < page.Data[1].PeriodStart {
		t.Fatalf("the newest two first: %+v", page)
	}
	decodeInto(t, e.get("landlord-a", path+"?limit=2&offset=2"), &page)
	if len(page.Data) != 1 || page.Meta.HasMore {
		t.Errorf("the last one: %+v", page)
	}

	var readings struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	decodeInto(t, e.get("landlord-a", "/v1/rooms/"+f.room.String()+"/meter-readings"), &readings)
	if readings.Meta.Total != 3 {
		t.Errorf("a reading for every bill: %d", readings.Meta.Total)
	}
	expectError(t, e.post("landlord-a", path+"/preview", map[string]any{"readings": []map[string]any{{"charge_id": f.power, "reading_value": 5}}}), http.StatusUnprocessableEntity, "validation.failed")
}

func TestTheDailyCommandsSendAndRemindThroughTheRealQueries(t *testing.T) {
	e := newInvoiceEnv(t)
	roomA := e.room(e.landlA, "P101")
	e.contract(e.landlA, &e.tenantT, roomA, 0)
	roomB := e.room(e.landlB, "Q101")
	e.contract(e.landlB, &e.tenantU, roomB, 0)
	e.plans[e.landlA] = true // only landlord A's plan has automatic invoicing
	ctx := context.Background()

	sent, err := e.automate.SendAutomatic(ctx)
	if err != nil || sent != 1 {
		t.Fatalf("sent %d: %v", sent, err)
	}
	if again, err := e.automate.SendAutomatic(ctx); err != nil || again != 0 {
		t.Errorf("a period is billed once: %d %v", again, err)
	}
	var status, via string
	if err := e.pool.QueryRow(ctx, `SELECT status, sent_via FROM invoices`).Scan(&status, &via); err != nil || status != "sent" || via != "automatic" {
		t.Fatalf("the bill: %s %s %v", status, via, err)
	}

	// Due in three days: one reminder, only once.
	if _, err := e.pool.Exec(ctx, `UPDATE invoices SET due_at = (date_trunc('day', now() AT TIME ZONE 'Asia/Ho_Chi_Minh') + interval '3 days 12 hours') AT TIME ZONE 'Asia/Ho_Chi_Minh'`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.automate.SendReminders(ctx); err != nil || n != 1 {
		t.Fatalf("reminders %d: %v", n, err)
	}
	if n, err := e.automate.SendReminders(ctx); err != nil || n != 0 {
		t.Errorf("a reminder goes out once: %d %v", n, err)
	}
}

func TestAutomaticSendingReadsEveryBatchOfContracts(t *testing.T) {
	e := newInvoiceEnv(t)
	e.plans[e.landlA] = true
	ctx := context.Background()
	// 230 running contracts (blank ones: nobody to tell), more than two batches of the scan.
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, cover_key, cover_thumb_key)
		SELECT $1, 'R' || g, 4, '12 Le Loi', 3000000, g, 'c.webp', 'c_t.webp' FROM generate_series(1, 230) g`, e.landlA); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO contracts (room_id, landlord_id, status, price_vnd, starts_on, confirmed_at, display_order)
		SELECT id, landlord_id, 'active', 3000000, current_date, now(), display_order FROM rooms`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.automate.SendAutomatic(ctx); err != nil || n != 230 {
		t.Fatalf("sent %d of 230: %v", n, err)
	}
}
