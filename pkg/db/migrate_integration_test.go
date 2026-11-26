//go:build integration

package db_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"rental-management/pkg/db"
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

func TestMigrateUpDownUpRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewEmptyDB(t)

	count := func() int {
		t.Helper()
		var n int
		// Partitions are not counted: the audit tables each stand for one, however many months they hold.
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace ns ON ns.oid = c.relnamespace
			WHERE ns.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND c.relname <> 'goose_db_version'`).Scan(&n)
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
	if applied != 44 || !strings.Contains(out.String(), "00017_oauth_accounts.sql") {
		t.Fatalf("up created %d tables, output:\n%s", applied, out.String())
	}

	// A second "up" has nothing to do.
	out.Reset()
	if err := db.Migrate(ctx, pool, "up", &out); err != nil || !strings.Contains(out.String(), "nothing to migrate") {
		t.Fatalf("second up: %v\n%s", err, out.String())
	}

	// Roll back every migration: the database must be empty again.
	for i := 0; i < 17; i++ {
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
	if strings.Count(out.String(), "pending") != 17 {
		t.Errorf("all seventeen migrations should be pending:\n%s", out.String())
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

func TestPropertyConstraints(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)

	var landlord, other, building string
	for i, dst := range []*string{&landlord, &other} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'L') RETURNING id::text`,
			fmt.Sprintf("l%d@example.vn", i)).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(stmt string, args ...any) error {
		_, err := pool.Exec(ctx, stmt, args...)
		return err
	}
	mustExec := func(stmt string, args ...any) {
		t.Helper()
		if err := exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	mustFail := func(name, stmt string, args ...any) {
		t.Helper()
		if err := exec(stmt, args...); err == nil {
			t.Errorf("%s must be rejected by the database", name)
		}
	}

	if err := pool.QueryRow(ctx, `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'Alpha', 'x', 1) RETURNING id::text`, landlord).Scan(&building); err != nil {
		t.Fatal(err)
	}

	// Building names: unique per landlord whatever the case, free again after a soft delete.
	mustFail("duplicate building name in another case", `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'ALPHA', 'x', 2)`, landlord)
	mustExec(`INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'ALPHA', 'x', 2)`, other)
	mustFail("padded building name", `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, ' Pad ', 'x', 3)`, landlord)
	mustFail("rental period without a unit", `INSERT INTO buildings (landlord_id, name, address_detail, minimum_rental_period, display_order) VALUES ($1, 'Pair', 'x', 2, 3)`, landlord)
	mustExec(`UPDATE buildings SET deleted_at = now() WHERE id = $1`, building)
	mustExec(`INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'alpha', 'x', 3)`, landlord)

	// Room names: unique per building, per group of rooms outside any building, free after a soft delete.
	var b1, b2 string
	for i, dst := range []*string{&b1, &b2} {
		if err := pool.QueryRow(ctx, `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, $2, 'x', $3) RETURNING id::text`,
			landlord, fmt.Sprintf("B%d", i), 10+i).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	const room = `INSERT INTO rooms (landlord_id, building_id, name, address_detail, price_vnd, display_order) VALUES ($1, $2, $3, 'x', 1, 1)`
	mustExec(room, landlord, b1, "P1")
	mustExec(room, landlord, b2, "P1") // the same name in another building is fine
	mustFail("the same room name twice in one building (any case)", room, landlord, b1, "p1")
	mustExec(room, landlord, nil, "A1")
	mustFail("the same room name twice outside any building", room, landlord, nil, "a1")
	mustExec(`UPDATE rooms SET deleted_at = now() WHERE name = 'A1'`)
	mustExec(room, landlord, nil, "A1")
	mustFail("a room that follows its building without a building", `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order, inherits_price) VALUES ($1, 'F', 'x', 1, 2, true)`, landlord)
	mustFail("a negative price", `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order) VALUES ($1, 'N', 'x', -1, 2)`, landlord)
	mustFail("an unknown status", `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, status, display_order) VALUES ($1, 'S', 'x', 1, 'repairing', 2)`, landlord)
	mustFail("a cover without its thumbnail", `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, cover_key, display_order) VALUES ($1, 'C', 'x', 1, 'k', 2)`, landlord)

	// Charges and media belong to exactly one owner.
	var roomID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM rooms WHERE name = 'P1' AND building_id = $1`, b1).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Power', 'electricity', 1, 'per_kwh', 1)`, roomID)
	mustFail("a charge with both owners", `INSERT INTO charges (room_id, building_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, $2, 'X', 'water', 1, 'per_kwh', 1)`, roomID, b1)
	mustFail("a charge with no owner", `INSERT INTO charges (name, kind, amount_vnd, unit, display_order) VALUES ('X', 'water', 1, 'per_kwh', 1)`)
	mustFail("a building charge that follows a template", `INSERT INTO charges (building_id, building_charge_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, gen_random_uuid(), 'X', 'water', 1, 'per_kwh', 1)`, b1)
	mustFail("an unknown charge unit", `INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'X', 'water', 1, 'per_week', 1)`, roomID)

	const media = `INSERT INTO media (room_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, display_order) VALUES ($1, $2, $3, $4, 'image/webp', 1, 1, 1, 1)`
	mustExec(media, roomID, "cover", "k1", "t1")
	mustFail("two covers for one room", media, roomID, "cover", "k2", "t2")
	mustExec(media, roomID, "photo", "k3", "t3")
	mustFail("a picture file key used twice", media, roomID, "photo", "k3", "t4")
	mustFail("a picture of an owner-less record", `INSERT INTO media (file_key, thumb_key, content_type, size_bytes, width, height, display_order) VALUES ('k9', 't9', 'image/webp', 1, 1, 1, 1)`)
}
