//go:build integration

package property

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/pkg/authz"
	"rental-management/pkg/httpx"
	"rental-management/pkg/i18n"
	"rental-management/pkg/imaging"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/storage"
	"rental-management/pkg/testdb"
)

var pg *testdb.Postgres

func TestMain(m *testing.M) {
	var err error
	pg, err = testdb.StartPostgres(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	pg.Terminate()
	os.Exit(code)
}

// fakeAuth maps a bearer string to a caller, so the tests need no login.
type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

// fakePlans overrides the limits of the default plan.
type fakePlans struct {
	UnlimitedPlans
	buildings *int
	rooms     *int
}

func (f fakePlans) MaxBuildings(context.Context, uuid.UUID) (*int, error) { return f.buildings, nil }
func (f fakePlans) MaxRooms(context.Context, uuid.UUID) (*int, error)     { return f.rooms, nil }

// allowAll is a rate limiter that never limits.
type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// fakeGuards reports the given blockers for rooms.
type fakeGuards map[uuid.UUID]Blocker

func (f fakeGuards) Blockers(context.Context, pgx.Tx, []uuid.UUID) (map[uuid.UUID]Blocker, error) {
	return f, nil
}

// recorder remembers what the observer was told.
type recorder struct {
	statuses []StatusChange
	content  [][]uuid.UUID
}

func (r *recorder) StatusChanged(_ context.Context, _ pgx.Tx, changes []StatusChange) error {
	r.statuses = append(r.statuses, changes...)
	return nil
}

func (r *recorder) ContentChanged(_ context.Context, _ pgx.Tx, ids []uuid.UUID) error {
	r.content = append(r.content, ids)
	return nil
}

// env is one test's stack: a fresh database, two landlords, a tenant and an admin, an in-memory file store and the
// HTTP router with the module mounted.
type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *Service
	files  *storage.Memory
	engine *gin.Engine
	ward   int32
	landlA uuid.UUID
	landlB uuid.UUID
}

func newEnv(t *testing.T, plans PlanLimits) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, pool: pg.NewDB(t), files: storage.NewMemory("http://files.test/v1/media")}
	if _, err := e.pool.Exec(ctx, `INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`); err != nil {
		t.Fatal(err)
	}
	e.ward = 4
	user := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.landlA, e.landlB = user("a@example.vn"), user("b@example.vn")
	tenant, admin := user("t@example.vn"), user("m@example.vn")

	landlordPerms := map[string]struct{}{"rooms.manage": {}, "buildings.manage": {}}
	auth := fakeAuth{
		"landlord-a": {UserID: e.landlA.String(), Role: authz.RoleLandlord, Permissions: landlordPerms},
		"landlord-b": {UserID: e.landlB.String(), Role: authz.RoleLandlord, Permissions: landlordPerms},
		"tenant":     {UserID: tenant.String(), Role: authz.RoleTenant, Permissions: map[string]struct{}{"rooms.view": {}}},
		"admin":      {UserID: admin.String(), Role: authz.RoleAdmin, Permissions: map[string]struct{}{"users.manage": {}}},
	}

	if plans == nil {
		plans = UnlimitedPlans{}
	}
	e.svc = NewService(Deps{
		Store: NewStore(e.pool), Storage: e.files, Images: imaging.NewProcessor(imaging.Limits{MaxBytes: 1 << 20, MaxDimension: 2048, ThumbMaxSize: 480}, 2),
		Plans: plans,
	}, Options{MaxMediaPerOwner: 10, MaxUploadBytes: 1 << 20, MaxDimension: 2048})

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	Mount(e.svc, RouteOptions{Auth: auth, Limiter: allowAll{}})(e.engine.Group("/v1"))
	return e
}

// call sends a JSON request (body nil for none) with an optional bearer token.
func (e *env) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
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

type errorBody struct {
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

func expectError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) errorBody {
	t.Helper()
	var body errorBody
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != status || body.Error.Code != code {
		t.Fatalf("want %d %s, got %d: %s", status, code, w.Code, w.Body.String())
	}
	return body
}

func expectStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("want %d, got %d: %s", status, w.Code, w.Body.String())
	}
}

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body.Data
}

type listBody struct {
	Data []BuildingSummary `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

// building creates a building through the API and returns it.
func (e *env) building(bearer, name string, extra map[string]any) BuildingDetail {
	e.t.Helper()
	body := map[string]any{"name": name, "address_detail": "12 Le Loi"}
	for k, v := range extra {
		body[k] = v
	}
	w := e.call(http.MethodPost, "/v1/buildings", body, bearer)
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[BuildingDetail](e.t, w)
}

// room inserts a room straight into the table (the rooms API comes with the next batch); followsPrice makes it
// follow the rent of its building.
func (e *env) room(landlord, building uuid.UUID, name string, followsPrice bool) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, building_id, name, address_detail, price_vnd, display_order, inherits_price)
		VALUES ($1, $2, $3, 'x', 1000000, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), $4) RETURNING id`,
		landlord, building, name, followsPrice).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}
