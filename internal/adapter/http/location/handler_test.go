package locationhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	locationhttp "rental-management/internal/adapter/http/location"
	domain "rental-management/internal/domain/location"
	usecase "rental-management/internal/usecase/location"
	"rental-management/pkg/httpx"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

// fakeLocationRepo stands for the repository under the real use case.
type fakeLocationRepo struct {
	usecase.LocationRepository
	provinces []domain.Division
	wards     map[int32][]domain.Division
	err       error
}

func (f fakeLocationRepo) Provinces(context.Context) ([]domain.Division, error) {
	return f.provinces, f.err
}

func (f fakeLocationRepo) ProvinceExists(_ context.Context, code int32) (bool, error) {
	_, ok := f.wards[code]
	return ok, f.err
}

func (f fakeLocationRepo) WardsOfProvince(_ context.Context, code int32) ([]domain.Division, error) {
	return f.wards[code], f.err
}

// countingLimiter is a minimal fixed-count limiter for the tests (the real one needs Redis): the first limit
// requests of a key pass, the rest are refused with a one-minute wait.
type countingLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *countingLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[key]++
	if c.counts[key] > limit {
		return ratelimit.Result{Allowed: false, RetryAfter: window}, nil
	}
	return ratelimit.Result{Allowed: true}, nil
}

func newLocationRouter(t *testing.T, repo usecase.LocationRepository, limit int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(httpx.Locale(catalog))
	limiter := &countingLimiter{counts: map[string]int{}}
	h := locationhttp.NewLocationHandler(usecase.NewLocations(repo))
	locationhttp.MountLocations(engine.Group("/v1"), h, limiter, limit, time.Minute)
	return engine
}

func locationGet(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func locationFixture() fakeLocationRepo {
	return fakeLocationRepo{
		provinces: []domain.Division{{Code: 1, Name: "Thành phố Hà Nội"}, {Code: 79, Name: "Thành phố Hồ Chí Minh"}},
		wards:     map[int32][]domain.Division{1: {{Code: 4, Name: "Phường Ba Đình"}}, 79: {}},
	}
}

func TestProvincesAreListedAndCacheable(t *testing.T) {
	w := locationGet(newLocationRouter(t, locationFixture(), 100), "/v1/provinces")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Fatalf("status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	var body struct {
		Data []locationhttp.DivisionView `json:"data"`
		Meta map[string]any              `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0].Code != 1 || body.Meta != nil {
		t.Errorf("unexpected body: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `{"code":1,"name":"Thành phố Hà Nội"}`) {
		t.Errorf("unexpected JSON shape: %s", w.Body)
	}
}

func TestWardsOfAProvince(t *testing.T) {
	engine := newLocationRouter(t, locationFixture(), 100)

	w := locationGet(engine, "/v1/provinces/1/wards")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Phường Ba Đình"`) {
		t.Errorf("status %d: %s", w.Code, w.Body)
	}
	// A province without wards answers an empty list, not null.
	if w := locationGet(engine, "/v1/provinces/79/wards"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("empty list expected, got %d: %s", w.Code, w.Body)
	}
}

func TestUnknownProvinceIs404AndNotCacheable(t *testing.T) {
	engine := newLocationRouter(t, locationFixture(), 100)
	for _, path := range []string{"/v1/provinces/999/wards", "/v1/provinces/abc/wards", "/v1/provinces/99999999999/wards"} {
		w := locationGet(engine, path)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"location.province_not_found"`) {
			t.Errorf("%s: status %d: %s", path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "" {
			t.Errorf("%s: a 404 must not be cacheable", path)
		}
	}
}

func TestStoreFailureIs500WithoutDetails(t *testing.T) {
	r := locationFixture()
	r.err = errors.New("connection refused: secret-host")
	w := locationGet(newLocationRouter(t, r, 100), "/v1/provinces")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret-host") {
		t.Errorf("status %d: %s", w.Code, w.Body)
	}
}

func TestLocationRoutesAreRateLimited(t *testing.T) {
	engine := newLocationRouter(t, locationFixture(), 2)
	for i := 0; i < 2; i++ {
		if w := locationGet(engine, "/v1/provinces"); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	w := locationGet(engine, "/v1/provinces")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" ||
		!strings.Contains(w.Body.String(), `"code":"request.rate_limited"`) {
		t.Errorf("status %d, Retry-After %q: %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
}
