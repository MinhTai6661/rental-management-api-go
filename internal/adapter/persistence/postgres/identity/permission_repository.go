package identitypg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/identity"
)

// PermissionRepository reads the permissions of roles and writes the role and permission catalog.
type PermissionRepository struct{ pool *pgxpool.Pool }

// NewPermissionRepository returns a repository on the pool.
func NewPermissionRepository(pool *pgxpool.Pool) *PermissionRepository {
	return &PermissionRepository{pool: pool}
}

// CodesOfRole returns the permission codes granted to the role.
func (r *PermissionRepository) CodesOfRole(ctx context.Context, role string) ([]string, error) {
	codes, err := postgres.Queries(ctx, r.pool).ListPermissionCodesOfRole(ctx, role)
	return codes, postgres.Translate(err)
}

// SeedCatalog makes the roles, permissions and role_permissions tables match the arguments: it adds what is
// missing, updates what changed and removes permissions that are not listed. Call it inside a transaction.
func (r *PermissionRepository) SeedCatalog(ctx context.Context, roles []domain.Role, perms []domain.Permission, grants map[string][]string) error {
	q := postgres.Queries(ctx, r.pool)
	for _, role := range roles {
		err := q.UpsertRole(ctx, sqlcgen.UpsertRoleParams{
			Code: role.Code, Seniority: int16(role.Seniority), //nolint:gosec // seniority is 1 to 5
			IsStaff: role.IsStaff,
		})
		if err != nil {
			return fmt.Errorf("seed role %s: %w", role.Code, err)
		}
	}

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

	for role, granted := range grants {
		if err := q.DeleteRolePermissions(ctx, role); err != nil {
			return fmt.Errorf("clear permissions of %s: %w", role, err)
		}
		if err := q.InsertRolePermissions(ctx, sqlcgen.InsertRolePermissionsParams{RoleCode: role, PermissionCodes: granted}); err != nil {
			return fmt.Errorf("grant permissions to %s: %w", role, err)
		}
	}
	return nil
}
