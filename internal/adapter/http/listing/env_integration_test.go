//go:build integration

package listinghttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/http/httpx"
	listinghttp "rental-management/internal/adapter/http/listing"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/persistence/postgres"
	listingpg "rental-management/internal/adapter/persistence/postgres/listing"
	"rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/listing"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

var (
	expectError  = testenv.ExpectError
	expectStatus = testenv.ExpectStatus
)

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	return testenv.DecodeData[T](t, w)
}

// fakeAuth maps a bearer string to a caller, so the tests need no login.
type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

// limiter allows `allowed` requests per key and refuses the rest (0: never limits).
type limiter struct {
	mu      sync.Mutex
	allowed int
	seen    map[string]int
}

func (l *limiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen[key]++
	if l.allowed > 0 && l.seen[key] > l.allowed {
		return ratelimit.Result{Allowed: false, RetryAfter: time.Minute}, nil
	}
	return ratelimit.Result{Allowed: true}, nil
}

type fakeURLs struct{}

func (fakeURLs) URL(key string) string { return "http://files.test/" + key }

type notices struct {
	mu   sync.Mutex
	sent []usecase.Notification
}

func (n *notices) Notify(_ context.Context, m usecase.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, m)
	return nil
}

func (n *notices) kinds() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.sent))
	for i, m := range n.sent {
		out[i] = m.Kind
	}
	return out
}

// The private details the public side must never show.
const (
	secretEmail   = "chu.hoa.secret@example.vn"
	secretPhone   = "0911222333"
	secretAddress = "12 Secret Street"
	tenantEmail   = "tenant.secret@example.vn"
)

// listingEnv is one test's stack: a fresh database, two landlords, a tenant, an admin and a super admin, the real
// repositories and use cases, and the HTTP router with the listing and search routes mounted.
type listingEnv struct {
	t       *testing.T
	pool    *pgxpool.Pool
	engine  *gin.Engine
	notices *notices
	limit   *limiter
	landlA  uuid.UUID
	landlB  uuid.UUID
	tenantT uuid.UUID
	admin   uuid.UUID
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

func newListingEnv(t *testing.T) *listingEnv {
	t.Helper()
	ctx := context.Background()
	e := &listingEnv{t: t, pool: pg.NewDB(t), notices: &notices{}, limit: &limiter{seen: map[string]int{}}}
	e.exec(`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`)
	e.exec(`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh'), (5, 1, 'Cau Giay', 'ward', 'cau-giay')`)
	e.exec(`INSERT INTO roles (code, seniority, is_staff) VALUES ('landlord', 3, false), ('tenant', 5, false)`)
	user := func(email, name, phone string, roles ...string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name, phone_number, address_detail) VALUES ($1, $2, $3, $4) RETURNING id`,
			email, name, phone, secretAddress).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, r := range roles {
			e.exec(`INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r)
		}
		return id
	}
	e.landlA = user(secretEmail, "Chu Hoa", secretPhone, "landlord", "tenant")
	e.landlB = user("b@example.vn", "Chu Lan", "0900000002", "landlord", "tenant")
	e.tenantT = user(tenantEmail, "Tenant T", "0900000003", "tenant")
	e.admin = user("m@example.vn", "Admin", "0900000004")
	superAdmin := user("s@example.vn", "Super", "0900000005")

	auth := fakeAuth{
		"landlord-a": principal(e.landlA, authz.RoleLandlord),
		"landlord-b": principal(e.landlB, authz.RoleLandlord),
		"tenant-t":   principal(e.tenantT, authz.RoleTenant),
		// Landlord A acting as a tenant (a landlord also holds the tenant role).
		"landlord-a-as-tenant": principal(e.landlA, authz.RoleTenant),
		"admin":                principal(e.admin, authz.RoleAdmin),
		"super-admin":          principal(superAdmin, authz.RoleSuperAdmin),
		// A tenant whose role grants no permission at all.
		"tenant-nothing": {UserID: e.tenantT.String(), Role: authz.RoleTenant, Permissions: map[string]struct{}{}},
	}
	rooms := listingpg.NewRoomReader(e.pool)
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Listings: listingpg.NewListingRepository(e.pool), Rooms: rooms,
		Offers: listingpg.NewOfferRepository(e.pool), Seed: rooms, Notifier: e.notices, Clock: usecase.ClockFunc(time.Now),
	}
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	binder := httpx.NewBinder()
	v1 := e.engine.Group("/v1")
	listinghttp.MountListings(v1,
		listinghttp.NewListingHandler(usecase.NewListings(deps), fakeURLs{}, binder),
		listinghttp.NewReviewHandler(usecase.NewReviews(deps), fakeURLs{}, binder), auth, e.limit)
	listinghttp.MountSearch(v1, listinghttp.NewOfferHandler(usecase.NewOffers(deps), fakeURLs{}), auth, e.limit)
	return e
}

func (e *listingEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// call sends a JSON request (body nil for none) with an optional bearer token and language.
func (e *listingEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	return e.callIn("en", method, path, body, bearer)
}

func (e *listingEnv) callIn(lang, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", lang)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// room inserts an available room of the landlord straight into the table.
func (e *listingEnv) room(landlord uuid.UUID, name string, price int64) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, area_square_meters, display_order, cover_key, cover_thumb_key)
		VALUES ($1, $2, 4, $3, $4, 20, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), 'c.webp', 'c_t.webp')
		RETURNING id`, landlord, name, secretAddress+", flat 4", price).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// listed inserts a listing in the status for the room and returns its id.
func (e *listingEnv) listed(room, landlord uuid.UUID, title, status string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO listings (room_id, landlord_id, title, description, status, submitted_at, published_at)
		VALUES ($1, $2, $3, 'Thoáng mát', $4, CASE WHEN $4 = 'draft' THEN NULL ELSE now() END, CASE WHEN $4 = 'approved' THEN now() END)
		RETURNING id`, room, landlord, title, status).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *listingEnv) status(id uuid.UUID) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM listings WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// The response bodies under the names the listing tests use.
type (
	ListingView       = listinghttp.ListingView
	ListingDetailView = listinghttp.ListingDetailView
	BulkView          = listinghttp.BulkView
	OfferView         = listinghttp.OfferView
	PublicOfferView   = listinghttp.PublicOfferView
	LandlordView      = listinghttp.LandlordView
)

type page[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		Page    int            `json:"page"`
		PerPage int            `json:"per_page"`
		Total   int            `json:"total"`
		Counts  map[string]int `json:"counts"`
	} `json:"meta"`
}

func listOf[T any](t *testing.T, w *httptest.ResponseRecorder) page[T] {
	t.Helper()
	expectStatus(t, w, http.StatusOK)
	var out page[T]
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out
}
