//go:build integration

package location

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

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

func TestSeedLoadsTheSnapshotAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	store := NewStore(pool)

	snap, err := LoadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Seed(ctx, pool); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
		var provinces, wards int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM provinces), (SELECT count(*) FROM wards)`).
			Scan(&provinces, &wards); err != nil {
			t.Fatal(err)
		}
		if provinces != len(snap.Provinces) || wards != len(snap.Wards) {
			t.Fatalf("after seed %d: %d provinces, %d wards; snapshot has %d and %d",
				i+1, provinces, wards, len(snap.Provinces), len(snap.Wards))
		}
	}

	items, err := store.Provinces(ctx)
	if err != nil || len(items) != len(snap.Provinces) {
		t.Fatalf("Provinces: %d items, %v", len(items), err)
	}
}

func TestEndpointsAgainstARealDatabase(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	if err := Seed(ctx, pool); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(t, NewStore(pool), 100)

	w := get(engine, "/v1/provinces")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=86400" ||
		!strings.Contains(w.Body.String(), `"name":"Thành phố Hà Nội"`) {
		t.Fatalf("provinces: status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}

	// Ho Chi Minh City is code 79 and has wards.
	w = get(engine, "/v1/provinces/79/wards")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[{"code"`) {
		t.Fatalf("wards: status %d: %.200s", w.Code, w.Body.String())
	}

}

func TestRateLimitAgainstARealDatabase(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	if err := Seed(ctx, pool); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(t, NewStore(pool), 2)

	for i := 1; i <= 2; i++ {
		if w := get(engine, "/v1/provinces"); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := get(engine, "/v1/provinces/79/wards")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("the third request must be refused: status %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestUnknownProvinceAgainstARealDatabase(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	if err := Seed(ctx, pool); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(t, NewStore(pool), 100)

	w := get(engine, "/v1/provinces/999999/wards")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"location.province_not_found"`) {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}
