package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/identity"
)

// UserRepository stores accounts and the roles they hold.
type UserRepository struct{ pool *pgxpool.Pool }

// NewUserRepository returns a repository on the pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

// Create inserts an account. A taken email is a unique ConstraintError, an unknown ward a foreign key one.
func (r *UserRepository) Create(ctx context.Context, u domain.NewUser) error {
	_, err := queries(ctx, r.pool).CreateUser(ctx, sqlcgen.CreateUserParams{
		ID: u.ID, Email: &u.Email, PasswordHash: &u.PasswordHash, FullName: u.FullName, PhoneNumber: u.PhoneNumber,
		WardCode: u.WardCode, AddressDetail: u.AddressDetail, LastActiveRole: &u.LastActiveRole,
		TermsVersion: &u.TermsVersion, TermsAcceptedAt: &u.TermsAcceptedAt, Now: u.CreatedAt,
	})
	return translate(err)
}

// FindByEmail returns the account with the (already normalized) email, or shared.ErrNotFound.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	row, err := queries(ctx, r.pool).GetUserByEmail(ctx, email)
	if err != nil {
		return domain.User{}, translate(err)
	}
	return domain.User{
		ID: row.ID, Email: row.Email, PasswordHash: row.PasswordHash, Status: row.Status,
		LastActiveRole: row.LastActiveRole, EmailVerifiedAt: row.EmailVerifiedAt, FullName: row.FullName,
	}, nil
}

// Profile returns the account with its ward and province, or shared.ErrNotFound.
func (r *UserRepository) Profile(ctx context.Context, id uuid.UUID) (domain.UserProfile, error) {
	row, err := queries(ctx, r.pool).GetUserProfile(ctx, id)
	if err != nil {
		return domain.UserProfile{}, translate(err)
	}
	return domain.UserProfile{
		ID: row.ID, Email: row.Email, FullName: row.FullName, PhoneNumber: row.PhoneNumber, Status: row.Status,
		AddressDetail: row.AddressDetail, LastActiveRole: row.LastActiveRole, TermsVersion: row.TermsVersion,
		CreatedAt: row.CreatedAt, WardCode: row.WardCode, WardName: row.WardName,
		ProvinceCode: row.ProvinceCode, ProvinceName: row.ProvinceName,
	}, nil
}

// AuthUser returns the state and roles of the account, or shared.ErrNotFound.
func (r *UserRepository) AuthUser(ctx context.Context, id uuid.UUID) (domain.AuthUser, error) {
	row, err := queries(ctx, r.pool).GetAuthUser(ctx, id)
	if err != nil {
		return domain.AuthUser{}, translate(err)
	}
	return domain.AuthUser{Status: row.Status, LastActiveRole: row.LastActiveRole, Roles: row.Roles}, nil
}

// PasswordHash returns the stored hash; nil for an account without a password.
func (r *UserRepository) PasswordHash(ctx context.Context, id uuid.UUID) (*string, error) {
	hash, err := queries(ctx, r.pool).GetPasswordHash(ctx, id)
	return hash, translate(err)
}

// UpdatePassword stores a new password hash.
func (r *UserRepository) UpdatePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error {
	return translate(queries(ctx, r.pool).UpdatePassword(ctx, sqlcgen.UpdatePasswordParams{ID: id, PasswordHash: &hash, Now: now}))
}

// SetLastActiveRole remembers the role the account acted in last.
func (r *UserRepository) SetLastActiveRole(ctx context.Context, id uuid.UUID, role string, now time.Time) error {
	return translate(queries(ctx, r.pool).SetLastActiveRole(ctx, sqlcgen.SetLastActiveRoleParams{ID: id, Role: &role, Now: now}))
}

// MarkEmailVerified records that the account's email was verified (the first time only).
func (r *UserRepository) MarkEmailVerified(ctx context.Context, id uuid.UUID, now time.Time) error {
	return translate(queries(ctx, r.pool).MarkEmailVerified(ctx, sqlcgen.MarkEmailVerifiedParams{ID: id, Now: now}))
}

// AddRole gives the account a role; it does nothing when it already holds it.
func (r *UserRepository) AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error {
	return translate(queries(ctx, r.pool).AddUserRole(ctx, sqlcgen.AddUserRoleParams{UserID: userID, RoleCode: role, Now: now}))
}

// UpsertDemo creates a development account, or refreshes its password when the email exists, and returns its id.
func (r *UserRepository) UpsertDemo(ctx context.Context, u domain.DemoUser) (uuid.UUID, error) {
	id, err := queries(ctx, r.pool).UpsertDemoUser(ctx, sqlcgen.UpsertDemoUserParams{
		ID: u.ID, Email: &u.Email, PasswordHash: &u.PasswordHash, FullName: u.FullName, WardCode: u.WardCode,
		LastActiveRole: &u.LastActiveRole, TermsVersion: &u.TermsVersion, Now: u.Now,
	})
	return id, translate(err)
}

// FirstWardCode returns the first seeded ward (the address of the demo accounts), or shared.ErrNotFound.
func (r *UserRepository) FirstWardCode(ctx context.Context) (int32, error) {
	code, err := queries(ctx, r.pool).FirstWardCode(ctx)
	return code, translate(err)
}
