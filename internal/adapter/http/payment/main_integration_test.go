//go:build integration

package paymenthttp_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/testdb"
)

var pg *testdb.Postgres

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	var err error
	if pg, err = testdb.StartPostgres(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	pg.Terminate()
	os.Exit(code)
}
