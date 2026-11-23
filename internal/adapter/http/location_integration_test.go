//go:build integration

package httpadapter_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/persistence/postgres"
	"rental-management/internal/adapter/snapshot/locationdata"
	usecase "rental-management/internal/usecase/location"
)

// seededLocationRouter is the real repository over a freshly seeded database (through the seed use case).
func seededLocationRouter(t *testing.T, limit int) (router *gin.Engine, seed func() error) {
	t.Helper()
	pool := pg.NewDB(t)
	repo := postgres.NewLocationRepository(pool)
	seeder := usecase.NewSeeder(postgres.NewTxManager(pool), repo, locationdata.Source{})
	seed = func() error { return seeder.Seed(context.Background()) }
	if err := seed(); err != nil {
		t.Fatal(err)
	}
	return newLocationRouter(t, repo, limit), seed
}

func TestLocationEndpointsAgainstARealDatabase(t *testing.T) {
	engine, seed := seededLocationRouter(t, 100)
	if err := seed(); err != nil { // seeding again must change nothing
		t.Fatal(err)
	}

	w := locationGet(engine, "/v1/provinces")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=86400" ||
		!strings.Contains(w.Body.String(), `"name":"Thành phố Hà Nội"`) {
		t.Fatalf("provinces: status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}

	// Ho Chi Minh City is code 79 and has wards.
	w = locationGet(engine, "/v1/provinces/79/wards")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[{"code"`) {
		t.Fatalf("wards: status %d: %.200s", w.Code, w.Body.String())
	}
}

func TestLocationRateLimitAgainstARealDatabase(t *testing.T) {
	engine, _ := seededLocationRouter(t, 2)

	for i := 1; i <= 2; i++ {
		if w := locationGet(engine, "/v1/provinces"); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := locationGet(engine, "/v1/provinces/79/wards")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("the third request must be refused: status %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestUnknownProvinceAgainstARealDatabase(t *testing.T) {
	engine, _ := seededLocationRouter(t, 100)

	w := locationGet(engine, "/v1/provinces/999999/wards")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"location.province_not_found"`) {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}
