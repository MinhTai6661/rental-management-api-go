//go:build integration

package invoicehttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/http/httpx"
	invoicehttp "rental-management/internal/adapter/http/invoice"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/persistence/postgres"
	invoicepg "rental-management/internal/adapter/persistence/postgres/invoice"
	"rental-management/internal/adapter/security"
	"rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/invoice"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/secretbox"
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

// autoPlans switches automatic invoicing on for the landlords in it.
type autoPlans map[uuid.UUID]bool

func (p autoPlans) AutoInvoicing(_ context.Context, id uuid.UUID) (bool, error) { return p[id], nil }

type recorder struct{ notes []usecase.Notification }

func (r *recorder) Notify(_ context.Context, n usecase.Notification) error {
	r.notes = append(r.notes, n)
	return nil
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

// invoiceEnv is one test's stack: a fresh database, two landlords, two tenants and an admin, the real repositories and
// use cases, and the HTTP router with the invoice routes mounted.
type invoiceEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	engine   *gin.Engine
	plans    autoPlans
	notes    *recorder
	automate *usecase.Automation
	landlA   uuid.UUID
	landlB   uuid.UUID
	tenantT  uuid.UUID
	tenantU  uuid.UUID
}

func newInvoiceEnv(t *testing.T) *invoiceEnv {
	t.Helper()
	ctx := context.Background()
	e := &invoiceEnv{t: t, pool: pg.NewDB(t), plans: autoPlans{}, notes: &recorder{}}
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name, phone_number) VALUES ($1, $2, '0901234567') RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.landlA, e.landlB = user("a@example.vn", "Landlord A"), user("b@example.vn", "Landlord B")
	e.tenantT, e.tenantU = user("t@example.vn", "Tenant T"), user("u@example.vn", "Tenant U")
	admin := user("m@example.vn", "Admin")
	auth := fakeAuth{
		"landlord-a": principal(e.landlA, authz.RoleLandlord),
		"landlord-b": principal(e.landlB, authz.RoleLandlord),
		"tenant-t":   principal(e.tenantT, authz.RoleTenant),
		"tenant-u":   principal(e.tenantU, authz.RoleTenant),
		"admin":      principal(admin, authz.RoleAdmin),
	}

	box, err := secretbox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Invoices: invoicepg.NewInvoiceRepository(e.pool), Contracts: invoicepg.NewContractReader(e.pool),
		Charges: invoicepg.NewChargeReader(e.pool), Plans: e.plans, Notifier: e.notes, Tokens: security.ShareTokens{}, Sealer: box,
		Clock: usecase.ClockFunc(time.Now),
	}
	opts := usecase.Options{WebURL: "https://app.example.vn", ReminderDays: 3}
	uc := usecase.NewInvoices(deps, opts)
	e.automate = usecase.NewAutomation(deps, opts)

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	invoicehttp.MountInvoices(e.engine.Group("/v1"),
		invoicehttp.NewInvoiceHandler(uc, httpx.NewBinder(), usecase.ClockFunc(time.Now), nil),
		invoicehttp.NewMeterHandler(uc), invoicehttp.NewPublicHandler(usecase.NewPublic(deps, opts)), auth, allowAll{})
	return e
}

func (e *invoiceEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// room inserts a room of the landlord.
func (e *invoiceEnv) room(landlord uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, cover_key, cover_thumb_key)
		VALUES ($1, $2, 4, '12 Le Loi', 3000000, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), 'c.webp', 'c_t.webp')
		RETURNING id`, landlord, name).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// contract inserts a running monthly contract of the room that started `monthsAgo` months ago; a nil tenant makes a
// blank contract (nobody with an account holds it).
func (e *invoiceEnv) contract(landlord uuid.UUID, tenant *uuid.UUID, room uuid.UUID, monthsAgo int) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'active', 3000000, (current_date - make_interval(months => $4))::date, now(),
		        (SELECT coalesce(min(display_order), 0) - 1 FROM contracts))
		RETURNING id`, room, landlord, tenant, monthsAgo).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// charge adds a charge to the room.
func (e *invoiceEnv) charge(room uuid.UUID, name, kind, unit string, amount int64) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, $2, $3, $4, $5, 1) RETURNING id`,
		room, name, kind, amount, unit).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *invoiceEnv) post(bearer, path string, body any) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, path, body, bearer)
}

func (e *invoiceEnv) get(bearer, path string) *httptest.ResponseRecorder {
	return e.call(http.MethodGet, path, nil, bearer)
}

func decodeInto(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
}
