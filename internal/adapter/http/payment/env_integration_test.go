//go:build integration

package paymenthttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/http/httpx"
	paymenthttp "rental-management/internal/adapter/http/payment"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/persistence/postgres"
	paymentpg "rental-management/internal/adapter/persistence/postgres/payment"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	"rental-management/internal/adapter/security"
	"rental-management/internal/domain/identity"
	domain "rental-management/internal/domain/payment"
	usecase "rental-management/internal/usecase/payment"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/imaging"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/secretbox"
	"rental-management/pkg/storage"
)

var (
	expectError  = testenv.ExpectError
	expectStatus = testenv.ExpectStatus
)

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	return testenv.DecodeData[T](t, w)
}

type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

// payStatus moves a bill to paid the way the invoice use case does (the real bridge is tested in cmd).
type payStatus struct{ pool *pgxpool.Pool }

func (s payStatus) MarkPaid(ctx context.Context, _, invoiceID uuid.UUID) error {
	now, via := time.Now(), "manual"
	return postgres.Queries(ctx, s.pool).UpdateInvoiceState(ctx, sqlcgen.UpdateInvoiceStateParams{
		ID: invoiceID, Status: "paid", SentAt: &now, SentVia: &via, PaidAt: &now, Delivery: "app",
	})
}

// files adapts the in-memory byte store to the file port.
type files struct{ s *storage.Memory }

func (f files) Put(ctx context.Context, key string, data []byte, ct string) error {
	return f.s.Put(ctx, key, data, ct)
}

func (f files) Open(ctx context.Context, key string) (io.ReadCloser, usecase.FileInfo, error) {
	rc, info, err := f.s.Open(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, usecase.FileInfo{}, domain.ErrFileNotFound
	}
	return rc, usecase.FileInfo{Size: info.Size, ContentType: info.ContentType}, err
}

func (f files) Delete(ctx context.Context, keys ...string) error { return f.s.Delete(ctx, keys...) }

// images adapts the real image checks to the image port.
type images struct{ p *imaging.Processor }

func (i images) Process(ctx context.Context, data []byte) (usecase.ProcessedImage, error) {
	res, err := i.p.Process(ctx, data)
	switch {
	case errors.Is(err, imaging.ErrUnsupported), errors.Is(err, imaging.ErrAnimated):
		return usecase.ProcessedImage{}, domain.ErrImageType
	case errors.Is(err, imaging.ErrCorrupt):
		return usecase.ProcessedImage{}, domain.ErrImageCorrupt
	case errors.Is(err, imaging.ErrTooLarge):
		return usecase.ProcessedImage{}, domain.ErrImageTooLarge
	case err != nil:
		return usecase.ProcessedImage{}, err
	}
	return usecase.ProcessedImage{Data: res.Data, ContentType: res.ContentType, Ext: res.Ext, Thumb: res.Thumb}, nil
}

// png is a small valid picture.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for x := range 16 {
		img.Set(x, x, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// paymentEnv is one test's stack: a fresh database, two landlords, two tenants and an admin, the real repositories and
// use cases, and the HTTP router with the payment routes mounted.
type paymentEnv struct {
	t       *testing.T
	pool    *pgxpool.Pool
	engine  *gin.Engine
	uc      *usecase.Payments
	files   *storage.Memory
	landlA  uuid.UUID
	landlB  uuid.UUID
	tenantT uuid.UUID
	tenantU uuid.UUID
	// bill is a sent bill of 1,000,000 VND of landlord A and tenant T; blank is one of a contract nobody holds.
	bill, blank uuid.UUID
}

func newPaymentEnv(t *testing.T) *paymentEnv {
	t.Helper()
	ctx := context.Background()
	e := &paymentEnv{t: t, pool: pg.NewDB(t), files: storage.NewMemory("http://files.test/v1/media")}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		must(e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id))
		return id
	}
	e.landlA, e.landlB = user("a@example.vn", "Landlord A"), user("b@example.vn", "Landlord B")
	e.tenantT, e.tenantU = user("t@example.vn", "Tenant T"), user("u@example.vn", "Tenant U")
	admin := user("m@example.vn", "Admin")
	auth := fakeAuth{
		"landlord-a": principal(e.landlA, authz.RoleLandlord), "landlord-b": principal(e.landlB, authz.RoleLandlord),
		"tenant-t": principal(e.tenantT, authz.RoleTenant), "tenant-u": principal(e.tenantU, authz.RoleTenant),
		"admin": principal(admin, authz.RoleAdmin),
	}

	room := e.room(e.landlA, "P101")
	e.bill = e.invoice(e.contract(e.landlA, &e.tenantT, room), 1, "sent")
	e.blank = e.invoice(e.contract(e.landlA, nil, e.room(e.landlA, "P102")), 1, "sent")

	key := bytes.Repeat([]byte{7}, 32)
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	links, err := security.NewProofLinks(key, "https://api.test")
	if err != nil {
		t.Fatal(err)
	}
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Payments: paymentpg.NewPaymentRepository(e.pool), Methods: paymentpg.NewMethodRepository(e.pool),
		Invoices: paymentpg.NewInvoiceReader(e.pool), Status: payStatus{e.pool}, Files: files{e.files},
		Images: images{imaging.NewProcessor(imaging.Limits{MaxBytes: 1 << 20, MaxDimension: 2048, ThumbMaxSize: 64}, 2)},
		Links:  links, Sealer: box, Clock: usecase.ClockFunc(time.Now),
	}
	opts := usecase.Options{MaxProofFiles: 3}
	e.uc = usecase.NewPayments(deps, opts)

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	binder := httpx.NewBinder()
	paymenthttp.MountPayments(e.engine.Group("/v1"),
		paymenthttp.NewPaymentHandler(e.uc, binder, paymenthttp.Limits{MaxFiles: 3, MaxBytes: 64 << 10}),
		paymenthttp.NewMethodHandler(usecase.NewMethods(deps, opts), binder), paymenthttp.NewProofHandler(e.uc), auth, allowAll{})
	return e
}

func (e *paymentEnv) room(landlord uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order)
		VALUES ($1, $2, 'x', 3000000, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms)) RETURNING id`, landlord, name).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *paymentEnv) contract(landlord uuid.UUID, tenant *uuid.UUID, room uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'active', 3000000, '2026-09-23', now(), (SELECT coalesce(min(display_order), 0) - 1 FROM contracts)) RETURNING id`,
		room, landlord, tenant).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// invoice inserts a bill of 1,000,000 VND for the n-th month of the contract.
func (e *paymentEnv) invoice(contract uuid.UUID, n int, status string) uuid.UUID {
	e.t.Helper()
	start := time.Date(2026, 9+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end,
		total_amount_vnd, due_at, status, sent_at, sent_via, delivery)
		VALUES ($1, $2, 'Bill', 'HD', $3, $4, $5, 1000000, now() + interval '3 days', $6,
		        CASE WHEN $6 = 'draft' THEN NULL ELSE now() END, CASE WHEN $6 = 'draft' THEN NULL ELSE 'manual' END, 'app') RETURNING id`,
		contract, "INV-"+uuid.NewString()[:12], "HD "+uuid.NewString()[:8], start, start.AddDate(0, 1, -1), status).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// bank saves a bank transfer for landlord A through the API.
func (e *paymentEnv) bank() {
	e.t.Helper()
	w := e.call(http.MethodPut, "/v1/my/payment-methods/manual_proof", map[string]any{
		"config": map[string]string{"bank_code": "VCB", "account_number": "0123456789", "account_name": "LANDLORD A"},
	}, "landlord-a")
	expectStatus(e.t, w, http.StatusOK)
}

func (e *paymentEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	return e.do(method, path, "application/json", bytes.NewReader(raw), bearer)
}

func (e *paymentEnv) do(method, path, contentType string, body io.Reader, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept-Language", "en")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *paymentEnv) get(bearer, path string) *httptest.ResponseRecorder {
	return e.call(http.MethodGet, path, nil, bearer)
}

func (e *paymentEnv) post(bearer, path string, body any) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, path, body, bearer)
}

// multipartBody builds a multipart payment: fields, then the pictures under "proofs[]".
func multipartBody(t *testing.T, fields map[string]string, pictures ...[]byte) (string, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range pictures {
		part, err := w.CreateFormFile("proofs[]", fmt.Sprintf("proof%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(p)
	}
	_ = w.Close()
	return w.FormDataContentType(), &b
}

// pay sends a multipart payment as the tenant.
func (e *paymentEnv) pay(bearer string, invoice uuid.UUID, fields map[string]string, pictures ...[]byte) *httptest.ResponseRecorder {
	e.t.Helper()
	ct, body := multipartBody(e.t, fields, pictures...)
	return e.do(http.MethodPost, "/v1/invoices/"+invoice.String()+"/payments", ct, body, bearer)
}

// pathOf is the part of an absolute link the engine routes.
func pathOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

// memKeys lists the keys of the in-memory store under a prefix.
func (e *paymentEnv) memKeys(prefix string) []string {
	var out []string
	for _, k := range e.files.Keys() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
}
