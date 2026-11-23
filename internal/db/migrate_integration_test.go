//go:build integration

package db_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"rental-management/internal/db"
	"rental-management/internal/testdb"
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

func TestMigrateUpDownUpRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewEmptyDB(t)

	count := func() int {
		t.Helper()
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	var out bytes.Buffer
	if err := db.Migrate(ctx, pool, "up", &out); err != nil {
		t.Fatalf("up: %v", err)
	}
	applied := count()
	if applied != 10 || !strings.Contains(out.String(), "00003_email_verification.sql") {
		t.Fatalf("up created %d tables, output:\n%s", applied, out.String())
	}

	// A second "up" has nothing to do.
	out.Reset()
	if err := db.Migrate(ctx, pool, "up", &out); err != nil || !strings.Contains(out.String(), "nothing to migrate") {
		t.Fatalf("second up: %v\n%s", err, out.String())
	}

	// Roll back every migration: the database must be empty again.
	for i := 0; i < 3; i++ {
		if err := db.Migrate(ctx, pool, "down", &out); err != nil {
			t.Fatalf("down %d: %v", i+1, err)
		}
	}
	if n := count(); n != 0 {
		t.Fatalf("%d tables left after rolling everything back", n)
	}

	if err := db.Migrate(ctx, pool, "up", &out); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if n := count(); n != applied {
		t.Fatalf("up again created %d tables, first run created %d", n, applied)
	}
}

func TestMigrateStatusAndUnknownCommand(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewEmptyDB(t)

	var out bytes.Buffer
	if err := db.Migrate(ctx, pool, "status", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "pending") != 3 {
		t.Errorf("all three migrations should be pending:\n%s", out.String())
	}

	if err := db.Migrate(ctx, pool, "sideways", &out); err == nil {
		t.Error("an unknown command must be refused")
	}
}

func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)

	// Emails are stored lower-case, roles and permission codes have a fixed shape.
	for name, stmt := range map[string]string{
		"upper-case email":    `INSERT INTO users (email, full_name) VALUES ('A@Example.vn', 'A')`,
		"empty name":          `INSERT INTO users (email, full_name) VALUES ('a@example.vn', '')`,
		"unknown status":      `INSERT INTO users (email, full_name, status) VALUES ('b@example.vn', 'B', 'deleted')`,
		"bad permission code": `INSERT INTO permissions (code, description) VALUES ('Rooms', 'x')`,
		"bad role code":       `INSERT INTO roles (code, seniority, is_staff) VALUES ('Boss', 9, false)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%s must be rejected by the database", name)
		}
	}

	// uuidv7() supplies the id; two rows get different, time-ordered ids.
	var a, b string
	for i, dst := range []*string{&a, &b} {
		err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id::text`,
			fmt.Sprintf("u%d@example.vn", i)).Scan(dst)
		if err != nil {
			t.Fatal(err)
		}
	}
	if a >= b || a[14] != '7' {
		t.Errorf("ids should be version-7 uuids in creation order: %s, %s", a, b)
	}
}
