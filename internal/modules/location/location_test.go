package location

import (
	"bytes"
	"compress/gzip"
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

	"rental-management/pkg/httpx"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

func TestEmbeddedSnapshotIsUsable(t *testing.T) {
	snap, err := LoadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Provinces) != 34 || len(snap.Wards) < 3000 {
		t.Errorf("unexpected snapshot size: %d provinces, %d wards", len(snap.Provinces), len(snap.Wards))
	}
	codes := map[int]bool{}
	for _, w := range snap.Wards {
		if codes[w.Code] {
			t.Fatalf("ward code %d appears twice", w.Code)
		}
		codes[w.Code] = true
	}
}

func gzipped(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestDecodeSnapshotRejectsBrokenData(t *testing.T) {
	good := Snapshot{
		Provinces: []SnapshotProvince{{Code: 1, Name: "Hà Nội", Slug: "ha_noi"}},
		Wards:     []SnapshotWard{{Code: 10, ProvinceCode: 1, Name: "Phường A", Slug: "phuong_a"}},
	}
	if _, err := decodeSnapshot(gzipped(t, good)); err != nil {
		t.Fatalf("good snapshot refused: %v", err)
	}

	orphan := good
	orphan.Wards = []SnapshotWard{{Code: 10, ProvinceCode: 99, Name: "Phường A", Slug: "phuong_a"}}
	nameless := good
	nameless.Provinces = []SnapshotProvince{{Code: 1, Slug: "ha_noi"}}
	for name, snap := range map[string]Snapshot{"orphan ward": orphan, "province without name": nameless, "empty": {}} {
		if _, err := decodeSnapshot(gzipped(t, snap)); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := decodeSnapshot(strings.NewReader("not gzip")); err == nil {
		t.Error("data that is not gzip must be refused")
	}
}

func TestWardBatches(t *testing.T) {
	wards := make([]SnapshotWard, 2500)
	got := wardBatches(wards, 1000)
	if len(got) != 3 || len(got[0]) != 1000 || len(got[1]) != 1000 || len(got[2]) != 500 {
		t.Errorf("batch sizes wrong: %d batches", len(got))
	}
	if len(wardBatches(nil, 1000)) != 0 {
		t.Error("no wards must give no batches")
	}
	if got := wardBatches(wards[:1000], 1000); len(got) != 1 {
		t.Errorf("exactly one batch expected, got %d", len(got))
	}
}

type fakeReader struct {
	provinces []Item
	wards     map[int32][]Item
	err       error
}

func (f fakeReader) Provinces(context.Context) ([]Item, error) { return f.provinces, f.err }
func (f fakeReader) Wards(_ context.Context, code int32) ([]Item, bool, error) {
	w, ok := f.wards[code]
	return w, ok, f.err
}

// countingLimiter is a minimal fixed-count limiter for the unit tests (the real one needs Redis): the first limit
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

func newTestRouter(t *testing.T, r reader, limit int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(httpx.Locale(catalog))
	limiter := &countingLimiter{counts: map[string]int{}}
	mount(r, Options{Limiter: limiter, Limit: limit, Window: time.Minute})(engine.Group("/v1"))
	return engine
}

func get(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func fixture() fakeReader {
	return fakeReader{
		provinces: []Item{{Code: 1, Name: "Thành phố Hà Nội"}, {Code: 79, Name: "Thành phố Hồ Chí Minh"}},
		wards:     map[int32][]Item{1: {{Code: 4, Name: "Phường Ba Đình"}}, 79: {}},
	}
}

func TestProvincesAreListedAndCacheable(t *testing.T) {
	w := get(newTestRouter(t, fixture(), 100), "/v1/provinces")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Fatalf("status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	var body struct {
		Data []Item         `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0].Code != 1 || body.Meta != nil {
		t.Errorf("unexpected body: %s", w.Body)
	}
}

func TestWardsOfAProvince(t *testing.T) {
	engine := newTestRouter(t, fixture(), 100)

	w := get(engine, "/v1/provinces/1/wards")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Phường Ba Đình"`) {
		t.Errorf("status %d: %s", w.Code, w.Body)
	}
	// A province without wards answers an empty list, not null.
	if w := get(engine, "/v1/provinces/79/wards"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("empty list expected, got %d: %s", w.Code, w.Body)
	}
}

func TestUnknownProvinceIs404AndNotCacheable(t *testing.T) {
	engine := newTestRouter(t, fixture(), 100)
	for _, path := range []string{"/v1/provinces/999/wards", "/v1/provinces/abc/wards", "/v1/provinces/99999999999/wards"} {
		w := get(engine, path)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"location.province_not_found"`) {
			t.Errorf("%s: status %d: %s", path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "" {
			t.Errorf("%s: a 404 must not be cacheable", path)
		}
	}
}

func TestStoreFailureIs500WithoutDetails(t *testing.T) {
	r := fixture()
	r.err = errors.New("connection refused: secret-host")
	w := get(newTestRouter(t, r, 100), "/v1/provinces")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret-host") {
		t.Errorf("status %d: %s", w.Code, w.Body)
	}
}

func TestLocationRoutesAreRateLimited(t *testing.T) {
	engine := newTestRouter(t, fixture(), 2)
	for i := 0; i < 2; i++ {
		if w := get(engine, "/v1/provinces"); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	w := get(engine, "/v1/provinces")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" ||
		!strings.Contains(w.Body.String(), `"code":"request.rate_limited"`) {
		t.Errorf("status %d, Retry-After %q: %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
}
