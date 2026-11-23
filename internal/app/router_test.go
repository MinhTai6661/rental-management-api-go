package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/platform/apperr"
	"rental-management/internal/platform/httpx"
	"rental-management/internal/platform/i18n"
	"rental-management/internal/platform/logger"
)

type createItem struct {
	Name  string `json:"name" validate:"required,min=3,max=10"`
	Qty   int    `json:"qty" validate:"required,min=1,max=5"`
	Email string `json:"email" validate:"omitempty,email"`
	Terms bool   `json:"terms" validate:"accepted"`
}

// newTestRouter is the real router plus a few /v1/t routes that exercise every response shape.
func newTestRouter(t *testing.T, timeout time.Duration, ready ...func(context.Context) error) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log, err := logger.New(&strings.Builder{}, "error", "text")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	binder := httpx.NewBinder()
	mount := func(v1 *gin.RouterGroup) {
		v1.GET("/t/ok", func(c *gin.Context) { httpx.OK(c, []string{"a"}) })
		v1.POST("/t/created", func(c *gin.Context) { httpx.Created(c, gin.H{"id": 1}) })
		v1.DELETE("/t/gone", func(c *gin.Context) { httpx.NoContent(c) })
		v1.GET("/t/list", func(c *gin.Context) {
			req, err := httpx.ParsePage(c, 0)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.List(c, []string{}, httpx.TotalMeta(req, 45))
		})
		v1.GET("/t/feed", func(c *gin.Context) {
			req, _ := httpx.ParsePage(c, 0)
			httpx.List(c, []string{}, httpx.FeedMeta(req, true))
		})
		v1.POST("/t/items", func(c *gin.Context) {
			var in createItem
			if err := binder.JSON(c, &in); err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, in)
		})
		v1.GET("/t/err/:code", func(c *gin.Context) {
			errs := map[string]error{
				"unauthenticated":     apperr.Unauthenticated(),
				"invalid_credentials": apperr.InvalidCredentials(),
				"refresh_invalid":     apperr.RefreshInvalid(),
				"forbidden":           apperr.Forbidden(),
				"inactive":            apperr.AccountInactive(),
				"notfound":            apperr.NotFound(),
				"province":            apperr.E(apperr.KindNotFound, apperr.CodeProvinceNotFound),
				"throttle":            apperr.RateLimited(30),
				"malformed":           apperr.Malformed(),
				"unexpected":          errors.New("secret database detail"),
				"deadline":            context.DeadlineExceeded,
			}
			httpx.Fail(c, errs[c.Param("code")])
		})
		v1.GET("/t/panic", func(*gin.Context) { panic("boom") })
		v1.GET("/t/slow", func(c *gin.Context) { <-c.Request.Context().Done() })
	}
	return NewRouter(Deps{
		Log: log, Catalog: catalog, RequestTimeout: timeout, CORSOrigins: []string{"http://localhost:2001"},
		Modules: []func(*gin.RouterGroup){mount}, Ready: ready,
	})
}

type reply struct {
	Code    int
	Body    string
	Headers http.Header
}

func do(r http.Handler, method, path, body string, headers map[string]string) reply {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return reply{rec.Code, strings.TrimSpace(rec.Body.String()), rec.Header()}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("body is not JSON: %q (%v)", body, err)
	}
	return out
}

// errorOf returns the "error" object of a response body.
func errorOf(t *testing.T, body string) map[string]any {
	t.Helper()
	e, ok := decode(t, body)["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object in %s", body)
	}
	return e
}

var en = map[string]string{"Accept-Language": "en"}

func TestSuccessEnvelopes(t *testing.T) {
	r := newTestRouter(t, time.Second)
	if got := do(r, "GET", "/v1/t/ok", "", nil); got.Code != 200 || got.Body != `{"data":["a"]}` {
		t.Errorf("OK: %d %s", got.Code, got.Body)
	}
	if got := do(r, "POST", "/v1/t/created", "", nil); got.Code != 201 || got.Body != `{"data":{"id":1}}` {
		t.Errorf("Created: %d %s", got.Code, got.Body)
	}
	if got := do(r, "DELETE", "/v1/t/gone", "", nil); got.Code != 204 || got.Body != "" {
		t.Errorf("NoContent: %d %q", got.Code, got.Body)
	}
}

func TestListMeta(t *testing.T) {
	r := newTestRouter(t, time.Second)
	got := do(r, "GET", "/v1/t/list?page=2&per_page=10", "", nil)
	if got.Code != 200 || got.Body != `{"data":[],"meta":{"page":2,"per_page":10,"total":45}}` {
		t.Errorf("table: %d %s", got.Code, got.Body)
	}
	if def := do(r, "GET", "/v1/t/list", "", nil); !strings.Contains(def.Body, `"page":1,"per_page":20,"total":45`) {
		t.Errorf("defaults not applied: %s", def.Body)
	}
	feed := do(r, "GET", "/v1/t/feed", "", nil)
	if feed.Body != `{"data":[],"meta":{"page":1,"per_page":20,"has_more":true}}` {
		t.Errorf("feed: %s", feed.Body)
	}
}

func TestPaginationValidation(t *testing.T) {
	r := newTestRouter(t, time.Second)
	cases := []struct {
		name, query, lang, message, field string
	}{
		{"per_page too big, en", "per_page=101", "en", "The per page field must not be greater than 100.", "per_page"},
		{"per_page too big, vi", "per_page=101", "vi", "Trường số dòng mỗi trang không được lớn hơn 100.", "per_page"},
		{"page zero", "page=0", "en", "The page field must be at least 1.", "page"},
		{"page not a number", "page=abc", "en", "The page field must be an integer.", "page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := do(r, "GET", "/v1/t/list?"+tc.query, "", map[string]string{"Accept-Language": tc.lang})
			if got.Code != 422 {
				t.Fatalf("status %d: %s", got.Code, got.Body)
			}
			e := errorOf(t, got.Body)
			if e["code"] != "validation.failed" {
				t.Errorf("code = %v", e["code"])
			}
			msgs := e["fields"].(map[string]any)[tc.field].([]any)
			if len(msgs) != 1 || msgs[0] != tc.message {
				t.Errorf("fields.%s = %v, want %q", tc.field, msgs, tc.message)
			}
		})
	}
}

func TestBodyValidation(t *testing.T) {
	r := newTestRouter(t, time.Second)
	valid := `{"name":"abc","qty":2,"terms":true}`
	cases := []struct {
		name, body, field, message string
	}{
		{"empty body is an empty object", ``, "name", "The name field is required."},
		{"missing fields", `{}`, "qty", "The qty field is required."},
		{"string too short", `{"name":"ab","qty":2,"terms":true}`, "name", "The name field must be at least 3 characters."},
		{"string too long", `{"name":"abcdefghijk","qty":2,"terms":true}`, "name", "The name field must not be greater than 10 characters."},
		{"number too big", `{"name":"abc","qty":9,"terms":true}`, "qty", "The qty field must not be greater than 5."},
		{"bad email", `{"name":"abc","qty":2,"terms":true,"email":"nope"}`, "email", "The email field must be a valid email address."},
		{"terms not accepted", `{"name":"abc","qty":2,"terms":false}`, "terms", "The terms field must be accepted."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := do(r, "POST", "/v1/t/items", tc.body, en)
			if got.Code != 422 {
				t.Fatalf("status %d: %s", got.Code, got.Body)
			}
			e := errorOf(t, got.Body)
			msgs, ok := e["fields"].(map[string]any)[tc.field].([]any)
			if !ok || msgs[0] != tc.message {
				t.Errorf("fields.%s = %v, want %q", tc.field, msgs, tc.message)
			}
		})
	}
	if ok := do(r, "POST", "/v1/t/items", valid, en); ok.Code != 200 || !strings.Contains(ok.Body, `"data":{"name":"abc","qty":2`) {
		t.Errorf("valid body rejected: %d %s", ok.Code, ok.Body)
	}
}

func TestMalformedAndTooLargeBodies(t *testing.T) {
	r := newTestRouter(t, time.Second)
	for name, body := range map[string]string{"not json": `not json`, "wrong type": `{"name":"abc","qty":"x","terms":true}`} {
		got := do(r, "POST", "/v1/t/items", body, en)
		if got.Code != 400 || errorOf(t, got.Body)["code"] != "request.malformed" {
			t.Errorf("%s: %d %s", name, got.Code, got.Body)
		}
	}
	big := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
	if got := do(r, "POST", "/v1/t/items", big, en); got.Code != 413 || errorOf(t, got.Body)["code"] != "request.too_large" {
		t.Errorf("too large: %d %.120s", got.Code, got.Body)
	}
}

func TestErrorCatalog(t *testing.T) {
	r := newTestRouter(t, time.Second)
	cases := []struct {
		route, code string
		status      int
		message     string // English
	}{
		{"unauthenticated", "auth.unauthenticated", 401, "Please log in."},
		{"invalid_credentials", "auth.invalid_credentials", 401, "The email or password is incorrect."},
		{"refresh_invalid", "auth.refresh_invalid", 401, "Your session has expired, please log in again."},
		{"forbidden", "auth.forbidden", 403, "You are not allowed to perform this action."},
		{"inactive", "auth.account_inactive", 403, "Your account has been disabled. Please contact support if you think this is a mistake."},
		{"notfound", "resource.not_found", 404, "Not found."},
		{"province", "location.province_not_found", 404, "Province not found."},
		{"throttle", "request.rate_limited", 429, "Too many requests, please try again later."},
		{"malformed", "request.malformed", 400, "The request is malformed."},
		{"unexpected", "server.internal", 500, "Server error, please try again later."},
		{"deadline", "server.busy", 503, "The system is busy, please try again in a moment."},
	}
	for _, tc := range cases {
		t.Run(tc.route, func(t *testing.T) {
			got := do(r, "GET", "/v1/t/err/"+tc.route, "", en)
			if got.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", got.Code, tc.status, got.Body)
			}
			e := errorOf(t, got.Body)
			if e["code"] != tc.code || e["message"] != tc.message {
				t.Errorf("error = %v", e)
			}
			if strings.Contains(got.Body, "secret") {
				t.Errorf("an internal detail leaked: %s", got.Body)
			}
		})
	}
	if got := do(r, "GET", "/v1/t/err/throttle", "", en); got.Headers.Get("Retry-After") != "30" {
		t.Errorf("Retry-After = %q", got.Headers.Get("Retry-After"))
	}
	vi := do(r, "GET", "/v1/t/err/notfound", "", nil) // no header: Vietnamese
	if errorOf(t, vi.Body)["message"] != "Không tìm thấy dữ liệu." {
		t.Errorf("not translated to Vietnamese: %s", vi.Body)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	r := newTestRouter(t, time.Second)

	nf := do(r, "GET", "/v1/unknown", "", en)
	if nf.Code != 404 || errorOf(t, nf.Body)["code"] != "http.route_not_found" {
		t.Errorf("404: %d %s", nf.Code, nf.Body)
	}

	na := do(r, "POST", "/v1/t/ok", "", en)
	if na.Code != 405 || errorOf(t, na.Body)["code"] != "http.method_not_allowed" {
		t.Errorf("405: %d %s", na.Code, na.Body)
	}
	if na.Headers.Get("Allow") != "GET" {
		t.Errorf("Allow = %q", na.Headers.Get("Allow"))
	}
}

func TestCORSThroughTheRouter(t *testing.T) {
	r := newTestRouter(t, time.Second)
	origin := "http://localhost:2001"

	pre := do(r, "OPTIONS", "/v1/unknown", "", map[string]string{"Origin": origin, "Access-Control-Request-Method": "GET"})
	if pre.Code != 204 || pre.Headers.Get("Access-Control-Max-Age") != "86400" || pre.Headers.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("preflight: %d %v", pre.Code, pre.Headers)
	}
	// An error response carries CORS too, or the browser would hide it from the web app.
	if e := do(r, "GET", "/v1/t/err/forbidden", "", map[string]string{"Origin": origin}); e.Headers.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("error response without CORS header: %v", e.Headers)
	}
}

func TestRecoverTurnsPanicIntoJSON500(t *testing.T) {
	r := newTestRouter(t, time.Second)
	got := do(r, "GET", "/v1/t/panic", "", en)
	if got.Code != 500 || errorOf(t, got.Body)["code"] != "server.internal" || strings.Contains(got.Body, "boom") {
		t.Errorf("got %d %s", got.Code, got.Body)
	}
	// The server keeps working afterwards.
	if again := do(r, "GET", "/v1/t/ok", "", en); again.Code != 200 {
		t.Errorf("router broken after a panic: %d", again.Code)
	}
}

func TestTimeoutAnswers503(t *testing.T) {
	r := newTestRouter(t, 30*time.Millisecond)
	got := do(r, "GET", "/v1/t/slow", "", en)
	if got.Code != 503 || errorOf(t, got.Body)["code"] != "server.busy" {
		t.Errorf("got %d %s", got.Code, got.Body)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	if h := do(newTestRouter(t, time.Second), "GET", "/healthz", "", nil); h.Code != 200 || h.Body != `{"status":"ok"}` {
		t.Errorf("healthz: %d %s", h.Code, h.Body)
	}
	ok := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("db down") }

	if r := do(newTestRouter(t, time.Second), "GET", "/readyz", "", en); r.Code != 200 {
		t.Errorf("readyz with no dependencies: %d", r.Code)
	}
	if r := do(newTestRouter(t, time.Second, ok), "GET", "/readyz", "", en); r.Code != 200 {
		t.Errorf("readyz with healthy deps: %d", r.Code)
	}
	if r := do(newTestRouter(t, time.Second, ok, down), "GET", "/readyz", "", en); r.Code != 503 || strings.Contains(r.Body, "db down") {
		t.Errorf("readyz with a failing dep: %d %s", r.Code, r.Body)
	}
}

func TestRequestIDAndSecurityHeaders(t *testing.T) {
	r := newTestRouter(t, time.Second)

	got := do(r, "GET", "/v1/t/ok", "", map[string]string{"X-Request-Id": "abc-123"})
	if got.Headers.Get("X-Request-Id") != "abc-123" {
		t.Errorf("incoming request id not echoed: %q", got.Headers.Get("X-Request-Id"))
	}
	generated := do(r, "GET", "/v1/t/ok", "", map[string]string{"X-Request-Id": "bad id with spaces"})
	if id := generated.Headers.Get("X-Request-Id"); id == "" || id == "bad id with spaces" {
		t.Errorf("an unusable request id was kept: %q", id)
	}
	if got.Headers.Get("X-Content-Type-Options") != "nosniff" || got.Headers.Get("X-Frame-Options") != "DENY" {
		t.Errorf("security headers missing: %v", got.Headers)
	}
}
