//go:build integration

package postgres

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	"rental-management/internal/model"
	"rental-management/pkg/authz"
)

// snapshot reads the three catalog tables back as the catalog would describe them.
func snapshot(t *testing.T, q *sqlcgen.Queries) (roles map[string]sqlcgen.Role, perms []string, granted map[string][]string) {
	t.Helper()
	ctx := context.Background()

	rows, err := q.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roles = map[string]sqlcgen.Role{}
	for _, r := range rows {
		roles[r.Code] = r
	}

	ps, err := q.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		perms = append(perms, p.Code)
	}

	rps, err := q.ListRolePermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	granted = map[string][]string{}
	for _, rp := range rps {
		granted[rp.RoleCode] = append(granted[rp.RoleCode], rp.PermissionCode)
	}
	return roles, perms, granted
}

// seedCatalog seeds the catalog the way the seed command does: in one transaction, from authz and the model.
func seedCatalog(ctx context.Context, pool *pgxpool.Pool) error {
	var roles []model.Role
	for _, code := range authz.Roles() {
		roles = append(roles, model.Role{Code: code, Seniority: authz.Seniority(code), IsStaff: authz.IsStaff(code)})
	}
	return NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
		return NewPermissionRepository(pool).SeedCatalog(ctx, roles, model.Permissions(), model.RolePermissions())
	})
}

func TestSeedCatalogMatchesTheCatalogAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	q := sqlcgen.New(pool)

	if err := seedCatalog(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rolesFirst, permsFirst, grantedFirst := snapshot(t, q)

	// Running it again changes nothing.
	if err := seedCatalog(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rolesSecond, permsSecond, grantedSecond := snapshot(t, q)
	if !reflect.DeepEqual(rolesFirst, rolesSecond) || !reflect.DeepEqual(permsFirst, permsSecond) ||
		!reflect.DeepEqual(grantedFirst, grantedSecond) {
		t.Fatal("a second seed changed the data")
	}

	// Roles carry their seniority and staff flag from authz.
	if len(rolesFirst) != len(authz.Roles()) {
		t.Fatalf("%d roles stored, %d expected", len(rolesFirst), len(authz.Roles()))
	}
	for _, role := range authz.Roles() {
		got := rolesFirst[role]
		if int(got.Seniority) != authz.Seniority(role) || got.IsStaff != authz.IsStaff(role) {
			t.Errorf("role %s stored as %+v", role, got)
		}
	}

	// Permissions and the role matrix equal the catalog.
	var wantPerms []string
	for _, p := range model.Permissions() {
		wantPerms = append(wantPerms, p.Code)
	}
	if !slices.Equal(permsFirst, wantPerms) {
		t.Errorf("permissions stored differ from the catalog:\n got %v\nwant %v", permsFirst, wantPerms)
	}
	for role, want := range model.RolePermissions() {
		if !slices.Equal(grantedFirst[role], want) {
			t.Errorf("permissions of %s:\n got %v\nwant %v", role, grantedFirst[role], want)
		}
	}
}

func TestSeedCatalogRemovesStalePermissionsAndRepairsGrants(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	if err := seedCatalog(ctx, pool); err != nil {
		t.Fatal(err)
	}

	// Someone added a permission the catalog does not know and gave the tenant an extra one.
	for _, stmt := range []string{
		`INSERT INTO permissions (code, description) VALUES ('legacy.thing', 'old')`,
		`INSERT INTO role_permissions (role_code, permission_code) VALUES ('tenant', 'legacy.thing')`,
		`INSERT INTO role_permissions (role_code, permission_code) VALUES ('tenant', 'rooms.manage')`,
		`DELETE FROM role_permissions WHERE role_code = 'tenant' AND permission_code = 'rooms.view'`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	if err := seedCatalog(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, perms, granted := snapshot(t, sqlcgen.New(pool))
	if slices.Contains(perms, "legacy.thing") {
		t.Error("a permission outside the catalog must be removed")
	}
	if !slices.Equal(granted[authz.RoleTenant], model.RolePermissions()[authz.RoleTenant]) {
		t.Errorf("tenant grants were not repaired: %v", granted[authz.RoleTenant])
	}
}
