//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/testdb"
)

var (
	pg  *testdb.Postgres
	rds *testdb.Redis
)

func TestMain(m *testing.M) {
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

func permissionExists(t *testing.T, ctx context.Context, q *sqlcgen.Queries, code string) bool {
	t.Helper()
	all, err := q.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if p.Code == code {
			return true
		}
	}
	return false
}

func TestWithinTxCommitsOnNil(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	err := NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
		return Queries(ctx, pool).UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: "tx.commit", Description: "d"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !permissionExists(t, ctx, sqlcgen.New(pool), "tx.commit") {
		t.Error("the row of a committed transaction must be visible")
	}
}

func TestWithinTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	boom := errors.New("boom")
	err := NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
		if err := Queries(ctx, pool).UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: "tx.rollback", Description: "d"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("the error of fn must come back, got %v", err)
	}
	if permissionExists(t, ctx, sqlcgen.New(pool), "tx.rollback") {
		t.Error("the row of a rolled back transaction must not exist")
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	func() {
		defer func() { _ = recover() }()
		_ = NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
			_ = Queries(ctx, pool).UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: "tx.panic", Description: "d"})
			panic("oops")
		})
	}()
	if permissionExists(t, ctx, sqlcgen.New(pool), "tx.panic") {
		t.Error("a panic must roll the transaction back")
	}
}

// A repository call made with the transaction's context sees the transaction's own writes, which a call on the
// pool (another connection) does not see until the commit.
func TestQueriesJoinTheTransactionOfTheContext(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	err := NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
		if err := Queries(ctx, pool).UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: "tx.visible", Description: "d"}); err != nil {
			return err
		}
		if !permissionExists(t, ctx, Queries(ctx, pool), "tx.visible") {
			t.Error("a call inside the transaction must see its own write")
		}
		if permissionExists(t, context.Background(), Queries(context.Background(), pool), "tx.visible") {
			t.Error("a call outside the transaction must not see an uncommitted row")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithinTxDoesNotNest(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	tx := NewTxManager(pool)
	boom := errors.New("boom")
	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		inner := tx.WithinTx(ctx, func(ctx context.Context) error {
			return Queries(ctx, pool).UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: "tx.nested", Description: "d"})
		})
		if inner != nil {
			return inner
		}
		return boom // the outer transaction decides: the inner write goes with it
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if permissionExists(t, ctx, sqlcgen.New(pool), "tx.nested") {
		t.Error("the inner call must have joined the outer transaction and rolled back with it")
	}
}

func TestTranslateRealConstraintViolations(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	insert := `INSERT INTO permissions (code, description) VALUES ('dup.code', 'd')`
	if _, err := pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, insert)
	if err = Translate(err); !shared.IsUnique(err) {
		t.Errorf("a duplicate row must be a unique violation, got %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO role_permissions (role_code, permission_code) VALUES ('no_such_role', 'dup.code')`)
	if err = Translate(err); !shared.IsForeignKey(err) {
		t.Errorf("a missing parent must be a foreign key violation, got %v", err)
	}
}
