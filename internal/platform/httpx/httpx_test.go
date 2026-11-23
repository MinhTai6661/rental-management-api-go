package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rental-management/internal/platform/apperr"
	"rental-management/internal/platform/authz"
	"rental-management/internal/platform/i18n"
)

// testEngine is an engine with the locale middleware and the given routes.
func testEngine(t *testing.T, mount func(r *gin.Engine)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(Locale(catalog))
	mount(r)
	return r
}

func call(r http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err  error
		want apperr.Kind
	}{
		"statement timeout": {fmt.Errorf("query: %w", &pgconn.PgError{Code: "57014"}), apperr.KindBusy},
		"lock timeout":      {&pgconn.PgError{Code: "55P03"}, apperr.KindBusy},
		"deadline":          {context.DeadlineExceeded, apperr.KindBusy},
		"cancelled":         {fmt.Errorf("x: %w", context.Canceled), apperr.KindBusy},
		"no rows":           {fmt.Errorf("load: %w", pgx.ErrNoRows), apperr.KindNotFound},
		"body too large":    {&http.MaxBytesError{Limit: 1}, apperr.KindTooLarge},
		"other pg error":    {&pgconn.PgError{Code: "22P02"}, apperr.KindInternal},
		"unknown":           {errors.New("boom"), apperr.KindInternal},
		"already typed":     {fmt.Errorf("w: %w", apperr.Forbidden()), apperr.KindForbidden},
	}
	for name, tc := range cases {
		if got := classify(tc.err).Kind; got != tc.want {
			t.Errorf("%s: kind %d, want %d", name, got, tc.want)
		}
	}
}

func TestStatusOfCoversEveryKind(t *testing.T) {
	want := map[apperr.Kind]int{
		apperr.KindInternal: 500, apperr.KindUnauthenticated: 401, apperr.KindForbidden: 403, apperr.KindNotFound: 404,
		apperr.KindMethodNotAllowed: 405, apperr.KindBadRequest: 400, apperr.KindTooLarge: 413,
		apperr.KindValidation: 422, apperr.KindRateLimited: 429, apperr.KindBusy: 503,
	}
	for kind, status := range want {
		if got := statusOf(kind); got != status {
			t.Errorf("statusOf(%d) = %d, want %d", kind, got, status)
		}
	}
}

func TestFailBody(t *testing.T) {
	r := testEngine(t, func(r *gin.Engine) {
		r.GET("/x", func(c *gin.Context) {
			Fail(c, apperr.Invalid(
				apperr.Field{Name: "email", Rule: apperr.RuleRequired},
				apperr.Field{Name: "password", Rule: apperr.RuleMinString, Params: map[string]string{"min": "8"}},
			))
		})
	})
	rec := call(r, "GET", "/x", map[string]string{"Accept-Language": "en"})
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	var got struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Fields  map[string][]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != "validation.failed" || got.Error.Message != "The given data was invalid." {
		t.Errorf("unexpected error: %+v", got.Error)
	}
	if got.Error.Fields["email"][0] != "The email field is required." ||
		got.Error.Fields["password"][0] != "The password field must be at least 8 characters." {
		t.Errorf("unexpected fields: %v", got.Error.Fields)
	}
}

func TestParsePage(t *testing.T) {
	cases := []struct {
		name, query string
		want        PageRequest
		badField    string
	}{
		{"defaults", "", PageRequest{Page: 1, PerPage: 20}, ""},
		{"explicit", "page=3&per_page=50", PageRequest{Page: 3, PerPage: 50}, ""},
		{"largest page size", "per_page=100", PageRequest{Page: 1, PerPage: 100}, ""},
		{"per_page too big", "per_page=101", PageRequest{}, "per_page"},
		{"per_page zero", "per_page=0", PageRequest{}, "per_page"},
		{"page zero", "page=0", PageRequest{}, "page"},
		{"page not a number", "page=abc", PageRequest{}, "page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/?"+tc.query, nil)
			got, err := ParsePage(c, 0)
			if tc.badField == "" {
				if err != nil || got != tc.want {
					t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
				}
				return
			}
			ae, ok := apperr.As(err)
			if !ok || ae.Kind != apperr.KindValidation || len(ae.Fields) != 1 || ae.Fields[0].Name != tc.badField {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOffsetAndMeta(t *testing.T) {
	req := PageRequest{Page: 3, PerPage: 20}
	if req.Offset() != 40 {
		t.Errorf("Offset = %d", req.Offset())
	}
	table, _ := json.Marshal(TotalMeta(req, 135))
	if string(table) != `{"page":3,"per_page":20,"total":135}` {
		t.Errorf("total meta = %s", table)
	}
	feed, _ := json.Marshal(FeedMeta(req, false))
	if string(feed) != `{"page":3,"per_page":20,"has_more":false}` {
		t.Errorf("feed meta = %s", feed)
	}
}

type stubAuth struct {
	principal authz.Principal
	err       error
	seen      string
}

func (s *stubAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	s.seen = bearer
	return s.principal, s.err
}

func TestAuthMiddleware(t *testing.T) {
	admin := authz.Principal{UserID: "u1", Role: authz.RoleAdmin, Permissions: map[string]struct{}{"users.manage": {}}}
	auth := &stubAuth{principal: admin}
	r := testEngine(t, func(r *gin.Engine) {
		ok := func(c *gin.Context) {
			p, _ := PrincipalOf(c)
			c.JSON(http.StatusOK, gin.H{"user": p.UserID})
		}
		r.GET("/me", RequireAuth(auth), ok)
		r.GET("/users", RequireAuth(auth), RequirePermission("users.view"), ok)
		r.GET("/rooms", RequireAuth(auth), RequirePermission("rooms.view"), ok)
		r.GET("/staff", RequireAuth(auth), RequireRole(authz.RoleAdmin, authz.RoleSuperAdmin), ok)
		r.GET("/tenants", RequireAuth(auth), RequireRole(authz.RoleTenant), ok)
		r.GET("/unguarded", RequirePermission("users.view"), ok)
	})
	bearer := map[string]string{"Authorization": "Bearer tok-123"}

	if rec := call(r, "GET", "/me", nil); rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
		t.Errorf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, "GET", "/me", map[string]string{"Authorization": "Basic abc"}); rec.Code != 401 {
		t.Errorf("wrong scheme: %d", rec.Code)
	}
	if rec := call(r, "GET", "/me", bearer); rec.Code != 200 || auth.seen != "tok-123" {
		t.Errorf("valid token: %d, authenticator saw %q", rec.Code, auth.seen)
	}
	if rec := call(r, "GET", "/users", bearer); rec.Code != 200 {
		t.Errorf("users.manage covers users.view: %d", rec.Code)
	}
	if rec := call(r, "GET", "/rooms", bearer); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"auth.forbidden"`) {
		t.Errorf("missing permission: %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, "GET", "/staff", bearer); rec.Code != 200 {
		t.Errorf("role allowed: %d", rec.Code)
	}
	if rec := call(r, "GET", "/tenants", bearer); rec.Code != 403 {
		t.Errorf("role refused: %d", rec.Code)
	}
	if rec := call(r, "GET", "/unguarded", bearer); rec.Code != 401 {
		t.Errorf("guard without RequireAuth must not pass: %d", rec.Code)
	}

	auth.err = apperr.AccountInactive()
	if rec := call(r, "GET", "/me", bearer); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"auth.account_inactive"`) {
		t.Errorf("inactive account: %d %s", rec.Code, rec.Body)
	}
}

func TestCORS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS([]string{"http://localhost:2001"}))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	pre := call(r, "OPTIONS", "/anything", map[string]string{
		"Origin": "http://localhost:2001", "Access-Control-Request-Method": "POST",
	})
	if pre.Code != 204 {
		t.Fatalf("preflight status %d", pre.Code)
	}
	for k, v := range map[string]string{
		"Access-Control-Allow-Origin":  "http://localhost:2001",
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Authorization, Content-Type, Accept-Language, X-Request-Id",
		"Access-Control-Max-Age":       "86400",
	} {
		if got := pre.Header().Get(k); got != v {
			t.Errorf("preflight %s = %q, want %q", k, got, v)
		}
	}
	if pre.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must not be allowed")
	}

	act := call(r, "GET", "/x", map[string]string{"Origin": "http://localhost:2001"})
	if act.Header().Get("Access-Control-Allow-Origin") != "http://localhost:2001" ||
		act.Header().Get("Access-Control-Expose-Headers") != "Content-Disposition" {
		t.Errorf("actual request headers: %v", act.Header())
	}
	if !strings.Contains(strings.Join(act.Header().Values("Vary"), ","), "Origin") {
		t.Errorf("Vary: Origin missing: %v", act.Header())
	}

	other := call(r, "GET", "/x", map[string]string{"Origin": "https://evil.example"})
	if other.Code != 200 || other.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an unlisted origin must get no CORS headers: %d %v", other.Code, other.Header())
	}
	otherPre := call(r, "OPTIONS", "/x", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
	if otherPre.Code != 204 || otherPre.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an unlisted origin's preflight: %d %v", otherPre.Code, otherPre.Header())
	}
	if plain := call(r, "GET", "/x", nil); plain.Header().Get("Access-Control-Allow-Origin") != "" || plain.Header().Get("Vary") != "" {
		t.Errorf("CORS headers without an Origin: %v", plain.Header())
	}
}
