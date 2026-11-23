package identity

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/modules/identity/sqlcgen"
	"rental-management/internal/platform/authz"
	"rental-management/internal/platform/db"
)

// Seed makes the roles, permissions and role_permissions tables match the catalog: it adds what is missing,
// updates what changed and removes permissions the catalog no longer has. It runs in one transaction and is safe
// to repeat. Users are not touched.
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return seed(ctx, sqlcgen.New(tx))
	})
}

func seed(ctx context.Context, q *sqlcgen.Queries) error {
	for _, role := range authz.Roles() {
		err := q.UpsertRole(ctx, sqlcgen.UpsertRoleParams{
			Code:      role,
			Seniority: int16(authz.Seniority(role)), //nolint:gosec // seniority is 1 to 5
			IsStaff:   authz.IsStaff(role),
		})
		if err != nil {
			return fmt.Errorf("seed role %s: %w", role, err)
		}
	}

	perms := Permissions()
	codes := make([]string, len(perms))
	for i, p := range perms {
		codes[i] = p.Code
		if err := q.UpsertPermission(ctx, sqlcgen.UpsertPermissionParams{Code: p.Code, Description: p.Description}); err != nil {
			return fmt.Errorf("seed permission %s: %w", p.Code, err)
		}
	}
	if err := q.DeleteStalePermissions(ctx, codes); err != nil {
		return fmt.Errorf("remove stale permissions: %w", err)
	}

	for role, granted := range RolePermissions() {
		if err := q.DeleteRolePermissions(ctx, role); err != nil {
			return fmt.Errorf("clear permissions of %s: %w", role, err)
		}
		err := q.InsertRolePermissions(ctx, sqlcgen.InsertRolePermissionsParams{RoleCode: role, PermissionCodes: granted})
		if err != nil {
			return fmt.Errorf("grant permissions to %s: %w", role, err)
		}
	}
	return nil
}
