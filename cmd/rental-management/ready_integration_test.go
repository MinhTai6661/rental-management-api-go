//go:build integration

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/i18n"
	"rental-management/pkg/testdb"
)

var (
	pg  *testdb.Postgres
	rds *testdb.Redis
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	var err error
	if pg, err = testdb.StartPostgres(ctx); err == nil {
		rds, err = testdb.StartRedis(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	rds.Terminate()
	pg.Terminate()
	os.Exit(code)
}

func TestReadyzPingsDatabaseAndRedis(t *testing.T) {
	pool := pg.NewDB(t)
	client := rds.Client(t)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(routerDeps{
		Log:            slog.New(slog.DiscardHandler),
		Catalog:        catalog,
		RequestTimeout: 5 * time.Second,
		Ready: []func(context.Context) error{
			pool.Ping,
			func(ctx context.Context) error { return client.Ping(ctx).Err() },
		},
	})
	ready := func() int {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return w.Code
	}

	if got := ready(); got != http.StatusOK {
		t.Fatalf("/readyz with everything up = %d", got)
	}
	_ = client.Close()
	if got := ready(); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz with Redis down = %d, want 503", got)
	}
}
