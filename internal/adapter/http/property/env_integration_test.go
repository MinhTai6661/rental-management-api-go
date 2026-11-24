//go:build integration

package propertyhttp_test

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

	"rental-management/internal/adapter/filestore"
	"rental-management/internal/adapter/http/httpx"
	propertyhttp "rental-management/internal/adapter/http/property"
	"rental-management/internal/adapter/imageproc"
	"rental-management/internal/adapter/persistence/postgres"
	propertypg "rental-management/internal/adapter/persistence/postgres/property"
	domain "rental-management/internal/domain/property"
	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/imaging"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/storage"
)

// The response bodies under the names the property tests use.
type (
	BuildingSummary = propertyhttp.BuildingSummaryView
	BuildingDetail  = propertyhttp.BuildingDetailView
	RoomSummary     = propertyhttp.RoomSummaryView
	RoomDetail      = propertyhttp.RoomDetailView
	MediaView       = propertyhttp.MediaView
	ChargeView      = propertyhttp.ChargeView
	CopyResult      = propertyhttp.CopyView
	RunResult       = propertyhttp.RunView
	bulkResultView  = propertyhttp.BulkView
)

// fakeAuth maps a bearer string to a caller, so the tests need no login.
type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

// allowAll is a rate limiter that never limits.
type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// fakePlans overrides the limits of the default plan.
type fakePlans struct {
	usecase.UnlimitedPlans
	buildings *int
	rooms     *int
}

func (f fakePlans) MaxBuildings(context.Context, uuid.UUID) (*int, error) { return f.buildings, nil }
func (f fakePlans) MaxRooms(context.Context, uuid.UUID) (*int, error)     { return f.rooms, nil }

// fakeGuards reports the given blockers for rooms.
type fakeGuards map[uuid.UUID]domain.Blocker

func (f fakeGuards) Blockers(context.Context, []uuid.UUID) (map[uuid.UUID]domain.Blocker, error) {
	return f, nil
}

// recorder remembers what the observer was told.
type recorder struct {
	statuses []domain.StatusChange
	content  [][]uuid.UUID
	deleted  [][]uuid.UUID
}

func (r *recorder) Deleted(_ context.Context, ids []uuid.UUID) error {
	r.deleted = append(r.deleted, ids)
	return nil
}

func (r *recorder) StatusChanged(_ context.Context, changes []domain.StatusChange) error {
	r.statuses = append(r.statuses, changes...)
	return nil
}

func (r *recorder) ContentChanged(_ context.Context, ids []uuid.UUID) error {
	r.content = append(r.content, ids)
	return nil
}

// The use cases hold their ports for good, so these forward to whatever a test installs later.

type swapPlans struct {
	mu    sync.Mutex
	inner usecase.PlanLimits
}

func (s *swapPlans) set(p usecase.PlanLimits) { s.mu.Lock(); s.inner = p; s.mu.Unlock() }
func (s *swapPlans) get() usecase.PlanLimits  { s.mu.Lock(); defer s.mu.Unlock(); return s.inner }

func (s *swapPlans) MaxBuildings(ctx context.Context, id uuid.UUID) (*int, error) {
	return s.get().MaxBuildings(ctx, id)
}

func (s *swapPlans) MaxRooms(ctx context.Context, id uuid.UUID) (*int, error) {
	return s.get().MaxRooms(ctx, id)
}

func (s *swapPlans) MaxMediaPerOwner(ctx context.Context, id uuid.UUID) (*int, error) {
	return s.get().MaxMediaPerOwner(ctx, id)
}

type swapGuards struct {
	mu    sync.Mutex
	inner usecase.RoomGuards
}

func (s *swapGuards) set(g usecase.RoomGuards) { s.mu.Lock(); s.inner = g; s.mu.Unlock() }

func (s *swapGuards) Blockers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.Blocker, error) {
	s.mu.Lock()
	inner := s.inner
	s.mu.Unlock()
	return inner.Blockers(ctx, ids)
}

type swapObserver struct {
	mu    sync.Mutex
	inner usecase.RoomObserver
}

func (s *swapObserver) set(o usecase.RoomObserver) { s.mu.Lock(); s.inner = o; s.mu.Unlock() }
func (s *swapObserver) get() usecase.RoomObserver  { s.mu.Lock(); defer s.mu.Unlock(); return s.inner }

func (s *swapObserver) StatusChanged(ctx context.Context, c []domain.StatusChange) error {
	return s.get().StatusChanged(ctx, c)
}

func (s *swapObserver) ContentChanged(ctx context.Context, ids []uuid.UUID) error {
	return s.get().ContentChanged(ctx, ids)
}

func (s *swapObserver) Deleted(ctx context.Context, ids []uuid.UUID) error {
	return s.get().Deleted(ctx, ids)
}

// propertyEnv is one test's stack: a fresh database, two landlords, a tenant and an admin, an in-memory file store,
// the real repositories and use cases, and the HTTP router with the property routes mounted.
type propertyEnv struct {
	t         *testing.T
	pool      *pgxpool.Pool
	buildings *usecase.Buildings
	seeder    *usecase.Seeder
	files     *storage.Memory
	engine    *gin.Engine
	plans     *swapPlans
	guards    *swapGuards
	observer  *swapObserver
	ward      int32
	landlA    uuid.UUID
	landlB    uuid.UUID
}

// withMaxMedia sets the number of pictures per owner when no plan limits it.
func withMaxMedia(n int) func(*usecase.Options) {
	return func(o *usecase.Options) { o.MaxMediaPerOwner = n }
}

func newPropertyEnv(t *testing.T, plans usecase.PlanLimits, options ...func(*usecase.Options)) *propertyEnv {
	t.Helper()
	ctx := context.Background()
	e := &propertyEnv{
		t: t, pool: pg.NewDB(t), files: storage.NewMemory("http://files.test/v1/media"),
		plans: &swapPlans{}, guards: &swapGuards{inner: usecase.NoGuards{}}, observer: &swapObserver{inner: usecase.NoObserver{}},
	}
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
		plans = usecase.UnlimitedPlans{}
	}
	e.plans.set(plans)
	opts := usecase.Options{MaxMediaPerOwner: 10, MaxUploadBytes: 1 << 20, MaxDimension: 2048}
	for _, apply := range options {
		apply(&opts)
	}
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Buildings: propertypg.NewBuildingRepository(e.pool), Rooms: propertypg.NewRoomRepository(e.pool),
		Charges: propertypg.NewChargeRepository(e.pool), Media: propertypg.NewMediaRepository(e.pool), Files: filestore.New(e.files),
		Images: imageproc.New(imaging.NewProcessor(imaging.Limits{MaxBytes: 1 << 20, MaxDimension: 2048, ThumbMaxSize: 480}, 2)),
		Plans:  e.plans, Guards: e.guards, Observer: e.observer, Clock: usecase.ClockFunc(time.Now),
	}
	buildings, rooms := usecase.NewBuildings(deps, opts), usecase.NewRooms(deps, opts)
	e.buildings, e.seeder = buildings, usecase.NewSeeder(buildings, rooms)

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	binder := httpx.NewBinder()
	v1 := e.engine.Group("/v1")
	media := propertyhttp.NewMediaHandler(usecase.NewMedia(deps, opts), e.files, binder, opts.MaxUploadBytes)
	propertyhttp.MountProperty(v1,
		propertyhttp.NewBuildingHandler(buildings, e.files, binder),
		propertyhttp.NewRoomHandler(rooms, e.files, binder), media, auth, allowAll{})
	// The public picture route is mounted by cmd/rental-management (shared with the other contexts).
	v1.GET("/media/*key", media.Serve)
	v1.HEAD("/media/*key", media.Serve)
	return e
}

// call sends a JSON request (body nil for none) with an optional bearer token.
func (e *propertyEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
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

type listBody struct {
	Data []BuildingSummary `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

// building creates a building through the API and returns it.
func (e *propertyEnv) building(bearer, name string, extra map[string]any) BuildingDetail {
	e.t.Helper()
	body := map[string]any{"name": name, "address_detail": "12 Le Loi"}
	for k, v := range extra {
		body[k] = v
	}
	w := e.call(http.MethodPost, "/v1/buildings", body, bearer)
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[BuildingDetail](e.t, w)
}

// room inserts a room straight into the table; followsPrice makes it follow the rent of its building.
func (e *propertyEnv) room(landlord, building uuid.UUID, name string, followsPrice bool) uuid.UUID {
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
