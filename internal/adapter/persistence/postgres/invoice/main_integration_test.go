//go:build integration

package invoicepg

import (
	"context"
	"fmt"
	"os"
	"testing"

	"rental-management/pkg/testdb"
)

var pg *testdb.Postgres

func TestMain(m *testing.M) {
	var err error
	if pg, err = testdb.StartPostgres(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	pg.Terminate()
	os.Exit(code)
}
