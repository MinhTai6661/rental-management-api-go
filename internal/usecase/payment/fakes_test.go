package payment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// world is the in-memory state the fakes below share: the bills, the payments, the saved methods and the files. The
// fakes implement the ports the way the real adapters do, so the rules are tested without a database.
type world struct {
	invoices map[uuid.UUID]*bill
	payments map[uuid.UUID]domain.Payment
	order    []uuid.UUID
	methods  map[string]domain.StoredMethod // landlord id + "/" + method
	files    map[string][]byte
	payers   map[uuid.UUID]domain.Person
	markPaid []uuid.UUID
	markErr  error
	notices  []Notification
	audits   []AuditEvent
	mails    []ReviewMail
	now      time.Time
}

// bill is a bill and how it was delivered (a tenant sees only what went by the app).
type bill struct {
	facts    domain.InvoiceFacts
	delivery string
}

func newWorld() *world {
	return &world{
		invoices: map[uuid.UUID]*bill{}, payments: map[uuid.UUID]domain.Payment{}, methods: map[string]domain.StoredMethod{},
		files: map[string][]byte{}, payers: map[uuid.UUID]domain.Person{}, now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
	}
}

// deps wires the world into the use cases.
func (w *world) deps() Deps {
	rec := &recorder{w: w}
	return Deps{
		Tx: txFake{}, Payments: paymentFake{w}, Methods: methodFake{w}, Invoices: invoiceFake{w}, Status: statusFake{w},
		Files: fileFake{w}, Images: imageFake{}, Links: linkFake{}, Sealer: sealFake{}, Notifier: rec, Audit: rec, Mailer: rec,
		Clock: clockFake{w},
	}
}

type clockFake struct{ w *world }

func (c clockFake) Now() time.Time { return c.w.now }

type txFake struct{}

func (txFake) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

// bill adds a sent bill of the landlord for the tenant (nil: a contract nobody holds) and returns its facts.
func (w *world) bill(landlord uuid.UUID, tenant *uuid.UUID, total int64) domain.InvoiceFacts {
	id := uuid.New()
	f := domain.InvoiceFacts{
		ID: id, ContractID: uuid.New(), RoomID: uuid.New(), RoomName: "P101", LandlordID: landlord, TenantID: tenant,
		Number: "INV-" + id.String()[:6], Name: "11/2026 - P101", PaymentCode: "HD T11-P101 AB23C", TotalVND: total,
		Status: domain.InvoiceSent, DueAt: w.now.AddDate(0, 0, 7),
	}
	w.invoices[id] = &bill{facts: f, delivery: "app"}
	return f
}

// accept saves an active bank transfer for the landlord.
func (w *world) accept(landlord uuid.UUID) {
	w.methods[methodKey(landlord, domain.MethodManualProof)] = domain.StoredMethod{
		Method: domain.MethodManualProof, ConfigSealed: "sealed:" + reverse(`{"bank_code":"VCB","account_number":"0123456789","account_name":"A B"}`), IsActive: true,
	}
}

// --- invoices ---------------------------------------------------------------------------------------------------

type invoiceFake struct{ w *world }

func (f invoiceFake) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.InvoiceFacts, error) {
	b, ok := f.w.invoices[id]
	if !ok {
		return domain.InvoiceFacts{}, shared.ErrNotFound
	}
	facts := b.facts
	switch {
	case v.AsLandlord && facts.LandlordID == v.UserID:
	case !v.AsLandlord && facts.TenantID != nil && *facts.TenantID == v.UserID && facts.Status != domain.InvoiceDraft && b.delivery == "app":
	default:
		return domain.InvoiceFacts{}, shared.ErrNotFound
	}
	return facts, nil
}

func (f invoiceFake) ByID(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error) {
	b, ok := f.w.invoices[id]
	if !ok {
		return domain.InvoiceFacts{}, shared.ErrNotFound
	}
	return b.facts, nil
}

func (f invoiceFake) Lock(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error) {
	return f.ByID(ctx, id)
}

func (f invoiceFake) SentOfRoom(ctx context.Context, landlordID uuid.UUID, room string) ([]domain.InvoiceFacts, error) {
	var out []domain.InvoiceFacts
	for _, b := range f.w.invoices {
		if b.facts.LandlordID == landlordID && b.facts.Status == domain.InvoiceSent && strings.EqualFold(b.facts.RoomName, room) {
			out = append(out, b.facts)
		}
	}
	return out, nil
}

// statusFake is what the invoice use case does when a payment covers a bill.
type statusFake struct{ w *world }

func (s statusFake) MarkPaid(ctx context.Context, landlordID, invoiceID uuid.UUID) error {
	if s.w.markErr != nil {
		return s.w.markErr
	}
	s.w.markPaid = append(s.w.markPaid, invoiceID)
	s.w.invoices[invoiceID].facts.Status = domain.InvoicePaid
	return nil
}

// --- payments ---------------------------------------------------------------------------------------------------

type paymentFake struct{ w *world }

func (r paymentFake) detail(p domain.Payment) domain.Detail {
	d := domain.Detail{Payment: p, Invoice: r.w.invoices[p.InvoiceID].facts}
	if p.PayerID != nil {
		if person, ok := r.w.payers[*p.PayerID]; ok {
			d.Payer = &person
		}
	}
	return d
}

func (r paymentFake) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	p, ok := r.w.payments[id]
	if !ok {
		return domain.Detail{}, shared.ErrNotFound
	}
	seen := p.PayerID != nil && *p.PayerID == v.UserID
	if v.AsLandlord {
		seen = r.w.invoices[p.InvoiceID].facts.LandlordID == v.UserID
	}
	if !seen {
		return domain.Detail{}, shared.ErrNotFound
	}
	return r.detail(p), nil
}

func (r paymentFake) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Payment, error) {
	p, ok := r.w.payments[id]
	if !ok {
		return domain.Payment{}, shared.ErrNotFound
	}
	return p, nil
}

func (r paymentFake) ProofKeys(ctx context.Context, id uuid.UUID) ([]string, error) {
	p, ok := r.w.payments[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return p.ProofKeys, nil
}

func pageOf(all []domain.Detail, limit, offset int) []domain.Detail {
	if offset >= len(all) {
		return nil
	}
	return all[offset:min(len(all), offset+limit)]
}

func (r paymentFake) ListOfInvoice(ctx context.Context, invoiceID uuid.UUID, limit, offset int) ([]domain.Detail, int, error) {
	var all []domain.Detail
	for _, id := range r.w.order {
		if p, ok := r.w.payments[id]; ok && p.InvoiceID == invoiceID {
			all = append(all, r.detail(p))
		}
	}
	return pageOf(all, limit, offset), len(all), nil
}

func (r paymentFake) ListOfLandlord(ctx context.Context, landlordID uuid.UUID, status *domain.Status, limit, offset int) ([]domain.Detail, int, error) {
	var all []domain.Detail
	for i := len(r.w.order) - 1; i >= 0; i-- {
		p, ok := r.w.payments[r.w.order[i]]
		if ok && r.w.invoices[p.InvoiceID].facts.LandlordID == landlordID && (status == nil || p.Status == *status) {
			all = append(all, r.detail(p))
		}
	}
	return pageOf(all, limit, offset), len(all), nil
}

func (r paymentFake) Insert(ctx context.Context, p domain.Payment) (uuid.UUID, error) {
	p.ID = uuid.New()
	r.w.payments[p.ID] = p
	r.w.order = append(r.w.order, p.ID)
	return p.ID, nil
}

func (r paymentFake) SaveReview(ctx context.Context, p domain.Payment) error {
	r.w.payments[p.ID] = p
	return nil
}

func (r paymentFake) Delete(ctx context.Context, id uuid.UUID) error {
	delete(r.w.payments, id)
	return nil
}

func (r paymentFake) Totals(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	out := map[uuid.UUID]domain.Totals{}
	for _, p := range r.w.payments {
		if !slices.Contains(ids, p.InvoiceID) {
			continue
		}
		t := out[p.InvoiceID]
		switch p.Status {
		case domain.StatusConfirmed:
			t.ConfirmedVND += p.AmountVND
		case domain.StatusPending:
			t.PendingVND += p.AmountVND
		}
		out[p.InvoiceID] = t
	}
	return out, nil
}

func (r paymentFake) VoidConfirmed(ctx context.Context, invoiceID uuid.UUID) error {
	for id, p := range r.w.payments {
		if p.InvoiceID == invoiceID && p.Status == domain.StatusConfirmed {
			p.Status = domain.StatusVoided
			r.w.payments[id] = p
		}
	}
	return nil
}

func (r paymentFake) DeleteOfInvoice(ctx context.Context, invoiceID uuid.UUID) ([]string, error) {
	var keys []string
	for id, p := range r.w.payments {
		if p.InvoiceID == invoiceID {
			keys = append(keys, p.ProofKeys...)
			delete(r.w.payments, id)
		}
	}
	return keys, nil
}

// --- methods ----------------------------------------------------------------------------------------------------

func methodKey(landlord uuid.UUID, method string) string { return landlord.String() + "/" + method }

type methodFake struct{ w *world }

func (r methodFake) ListOfLandlord(ctx context.Context, landlordID uuid.UUID) ([]domain.StoredMethod, error) {
	var out []domain.StoredMethod
	for k, m := range r.w.methods {
		if strings.HasPrefix(k, landlordID.String()+"/") {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r methodFake) Get(ctx context.Context, landlordID uuid.UUID, method string) (domain.StoredMethod, error) {
	m, ok := r.w.methods[methodKey(landlordID, method)]
	if !ok {
		return domain.StoredMethod{}, shared.ErrNotFound
	}
	return m, nil
}

func (r methodFake) Upsert(ctx context.Context, landlordID uuid.UUID, m domain.StoredMethod) (bool, error) {
	_, had := r.w.methods[methodKey(landlordID, m.Method)]
	r.w.methods[methodKey(landlordID, m.Method)] = m
	return !had, nil
}

func (r methodFake) Delete(ctx context.Context, landlordID uuid.UUID, method string) (bool, error) {
	_, had := r.w.methods[methodKey(landlordID, method)]
	delete(r.w.methods, methodKey(landlordID, method))
	return had, nil
}

// --- files, images, links, sealing ------------------------------------------------------------------------------

type fileFake struct{ w *world }

func (f fileFake) Put(ctx context.Context, key string, data []byte, contentType string) error {
	f.w.files[key] = data
	return nil
}

func (f fileFake) Open(ctx context.Context, key string) (io.ReadCloser, FileInfo, error) {
	data, ok := f.w.files[key]
	if !ok {
		return nil, FileInfo{}, domain.ErrFileNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), FileInfo{Size: int64(len(data)), ContentType: "image/webp"}, nil
}

func (f fileFake) Delete(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.w.files, k)
	}
	return nil
}

// imageFake accepts any bytes but "bad" (corrupt) and "gif" (a type we do not take).
type imageFake struct{}

func (imageFake) Process(ctx context.Context, data []byte) (ProcessedImage, error) {
	switch string(data) {
	case "bad":
		return ProcessedImage{}, domain.ErrImageCorrupt
	case "gif":
		return ProcessedImage{}, domain.ErrImageType
	}
	return ProcessedImage{Data: data, Ext: "webp", Thumb: append([]byte("thumb:"), data...)}, nil
}

// linkFake signs nothing: a link is valid with the signature "ok" until it expires.
type linkFake struct{}

func (linkFake) Link(paymentID uuid.UUID, file string, expires time.Time) string {
	return fmt.Sprintf("https://api.test/v1/payments/%s/proofs/%s?expires=%d&signature=ok", paymentID, file, expires.Unix())
}

func (linkFake) Valid(paymentID uuid.UUID, file string, expires int64, signature string, now time.Time) bool {
	return signature == "ok" && expires > now.Unix()
}

// sealFake "encrypts" by reversing the text behind a prefix: enough to tell that a stored value is not the plain one.
type sealFake struct{}

func (sealFake) Seal(plain string) (string, error) { return "sealed:" + reverse(plain), nil }

func (sealFake) Open(sealed string) (string, error) {
	rest, ok := strings.CutPrefix(sealed, "sealed:")
	if !ok {
		return "", errors.New("not sealed")
	}
	return reverse(rest), nil
}

func reverse(s string) string {
	r := []rune(s)
	slices.Reverse(r)
	return string(r)
}

// --- notifications, audit, mail ---------------------------------------------------------------------------------

type recorder struct{ w *world }

func (r *recorder) Notify(ctx context.Context, n Notification) error {
	r.w.notices = append(r.w.notices, n)
	return nil
}

func (r *recorder) Record(ctx context.Context, e AuditEvent) error {
	r.w.audits = append(r.w.audits, e)
	return nil
}

func (r *recorder) SendReview(ctx context.Context, m ReviewMail) error {
	r.w.mails = append(r.w.mails, m)
	return nil
}

func (w *world) actions() []string {
	out := make([]string, len(w.audits))
	for i, a := range w.audits {
		out[i] = a.Action
	}
	return out
}
