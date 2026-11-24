// Package adminpg is the database side of the admin use cases: the accounts the admin manages, landlord applications
// and profiles, the setting overrides, the legal documents and the data subject's rights. It returns domain types and
// translates driver errors (see postgres.Translate). The users table belongs to the identity context; this adapter
// works on it directly, with queries of its own.
package adminpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/admin"
)

// UserRepository reads and changes the accounts the admin manages.
type UserRepository struct{ pool *pgxpool.Pool }

// NewUserRepository returns a repository on the pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

// List returns a page of accounts and how many accounts match.
func (r *UserRepository) List(ctx context.Context, f domain.UserFilter, limit, offset int) ([]domain.Account, int, error) {
	q := postgres.Queries(ctx, r.pool)
	total, err := q.AdminCountUsers(ctx, sqlcgen.AdminCountUsersParams{
		ViewerID: f.ViewerID, IncludeStaff: f.IncludeStaff, Status: f.Status, RoleName: f.RoleName, Search: f.Search,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	rows, err := q.AdminListUsers(ctx, sqlcgen.AdminListUsersParams{
		ViewerID: f.ViewerID, IncludeStaff: f.IncludeStaff, Status: f.Status, RoleName: f.RoleName, Search: f.Search,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, Sort: string(f.Sort),
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the use case
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Account, len(rows))
	for i, row := range rows {
		out[i] = domain.Account{
			ID: row.ID, Email: row.Email, FullName: row.FullName, PhoneNumber: row.PhoneNumber, Status: row.Status,
			WardCode: row.WardCode, CreatedAt: row.CreatedAt, Roles: row.Roles,
		}
	}
	return out, int(total), nil
}

// Lock returns the account with its roles and locks it until the transaction ends.
func (r *UserRepository) Lock(ctx context.Context, id uuid.UUID) (domain.Account, error) {
	row, err := postgres.Queries(ctx, r.pool).AdminLockUser(ctx, id)
	return accountOf(sqlcgen.AdminGetUserRow(row)), postgres.Translate(err)
}

// Get returns the account with its roles.
func (r *UserRepository) Get(ctx context.Context, id uuid.UUID) (domain.Account, error) {
	row, err := postgres.Queries(ctx, r.pool).AdminGetUser(ctx, id)
	return accountOf(row), postgres.Translate(err)
}

func accountOf(row sqlcgen.AdminGetUserRow) domain.Account {
	return domain.Account{
		ID: row.ID, Email: row.Email, FullName: row.FullName, PhoneNumber: row.PhoneNumber, Status: row.Status,
		LastActiveRole: row.LastActiveRole, WardCode: row.WardCode, AddressDetail: row.AddressDetail,
		CreatedAt: row.CreatedAt, Roles: row.Roles,
	}
}

// SoftDelete marks the account deleted: it can no longer sign in.
func (r *UserRepository) SoftDelete(ctx context.Context, id uuid.UUID, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminSoftDeleteUser(ctx, sqlcgen.AdminSoftDeleteUserParams{ID: id, Now: now}))
}

// ReplaceRoles makes the account hold exactly the roles.
func (r *UserRepository) ReplaceRoles(ctx context.Context, id uuid.UUID, roles []string, now time.Time) error {
	q := postgres.Queries(ctx, r.pool)
	if err := q.AdminDeleteUserRoles(ctx, id); err != nil {
		return postgres.Translate(err)
	}
	return postgres.Translate(q.AdminInsertUserRoles(ctx, sqlcgen.AdminInsertUserRolesParams{UserID: id, RoleCodes: roles, Now: now}))
}

// AddRole gives the account a role; it does nothing when it already holds it.
func (r *UserRepository) AddRole(ctx context.Context, id uuid.UUID, role string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AddUserRole(ctx, sqlcgen.AddUserRoleParams{UserID: id, RoleCode: role, Now: now}))
}

// SetLastActiveRole stores the role the account acted in last (nil clears it).
func (r *UserRepository) SetLastActiveRole(ctx context.Context, id uuid.UUID, role *string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminSetLastActiveRole(ctx, sqlcgen.AdminSetLastActiveRoleParams{ID: id, Role: role, Now: now}))
}

// RevokeSessions closes every open session of the account and returns their ids.
func (r *UserRepository) RevokeSessions(ctx context.Context, id uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).AdminRevokeUserSessions(ctx, sqlcgen.AdminRevokeUserSessionsParams{UserID: id, Reason: &reason, Now: now})
	return ids, postgres.Translate(err)
}

// PasswordHash returns the stored hash; nil for an account without a password.
func (r *UserRepository) PasswordHash(ctx context.Context, id uuid.UUID) (*string, error) {
	hash, err := postgres.Queries(ctx, r.pool).GetPasswordHash(ctx, id)
	return hash, postgres.Translate(err)
}
