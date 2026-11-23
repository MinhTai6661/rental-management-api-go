//go:build integration

package planhttp_test

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
	planhttp "rental-management/internal/adapter/http/plan"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/persistence/postgres"
	planpg "rental-management/internal/adapter/persistence/postgres/plan"
	"rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/plan"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

// The response bodies under the names the plan tests use.
type (
	PlanView         = planhttp.PlanView
	SubscriptionView = planhttp.SubscriptionView
	MineView         = planhttp.MineView
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

// allowAll is a rate limiter that never limits.
type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

type notices struct{ sent []usecase.Notification }

func (n *notices) Notify(_ context.Context, m usecase.Notification) error {
	n.sent = append(n.sent, m)
	return nil
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

// planEnv is one test's stack: a fresh database with the base plan catalog, a landlord who is also a tenant, a tenant,
// an admin and a super admin, the real repositories and use cases, and the HTTP router with the plan routes.
type planEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	engine   *gin.Engine
	notices  *notices
	subs     *usecase.Subscriptions
	landlord uuid.UUID
	tenant   uuid.UUID
	admin    uuid.UUID
	super    uuid.UUID
}

func newPlanEnv(t *testing.T) *planEnv {
	t.Helper()
	ctx := context.Background()
	e := &planEnv{t: t, pool: pg.NewDB(t), notices: &notices{}}
	for _, q := range []string{
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true), ('landlord', 3, false), ('tenant', 5, false)`,
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string, roles ...string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, r := range roles {
			if _, err := e.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	e.landlord = user("l@example.vn", "Landlord", "landlord", "tenant")
	e.tenant = user("t@example.vn", "Tenant", "tenant")
	e.admin = user("a@example.vn", "Admin", "admin")
	e.super = user("s@example.vn", "Super", "super_admin")

	auth := fakeAuth{
		"landlord":           principal(e.landlord, authz.RoleLandlord),
		"landlord-as-tenant": principal(e.landlord, authz.RoleTenant),
		"tenant":             principal(e.tenant, authz.RoleTenant),
		"admin":              principal(e.admin, authz.RoleAdmin),
		"super":              principal(e.super, authz.RoleSuperAdmin),
	}
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Plans: planpg.NewPlanRepository(e.pool), Subs: planpg.NewSubscriptionRepository(e.pool),
		Accounts: planpg.NewAccounts(e.pool), Usage: planpg.NewUsage(e.pool), Notifier: e.notices, Clock: usecase.ClockFunc(time.Now),
	}
	if err := usecase.NewSeeder(deps).SeedPlans(ctx); err != nil {
		t.Fatal(err)
	}
	e.subs = usecase.NewSubscriptions(deps)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	planhttp.MountPlans(e.engine.Group("/v1"), planhttp.NewPlanHandler(usecase.NewPlans(deps), e.subs, httpx.NewBinder()), auth, allowAll{})
	return e
}

// call sends a JSON request (body nil for none) with an optional bearer token.
func (e *planEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
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
	req.Header.Set("Accept-Language", "en")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// planID looks a plan of the catalog up by code.
func (e *planEnv) planID(code string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM plans WHERE code = $1`, code).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *planEnv) mine(bearer string) MineView {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/my/subscription", nil, bearer)
	expectStatus(e.t, w, http.StatusOK)
	return decodeData[MineView](e.t, w)
}

type list[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

func listOf[T any](t *testing.T, w *httptest.ResponseRecorder) list[T] {
	t.Helper()
	expectStatus(t, w, http.StatusOK)
	var out list[T]
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *planEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
