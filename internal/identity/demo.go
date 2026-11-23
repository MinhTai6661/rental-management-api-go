package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/authz"
	"rental-management/internal/identity/sqlcgen"
)

// DemoAccount is an account `seed -demo` creates for development.
type DemoAccount struct {
	Email    string
	FullName string
	Roles    []string
	// HasAddress gives the account a ward (the first one of the seeded divisions).
	HasAddress bool
}

// DemoAccounts are the accounts of the development data: one for each side of the platform.
func DemoAccounts() []DemoAccount {
	return []DemoAccount{
		{Email: "superadmin@rental.test", FullName: "Demo Super Admin", Roles: []string{authz.RoleSuperAdmin}},
		{Email: "admin@rental.test", FullName: "Demo Admin", Roles: []string{authz.RoleAdmin}},
		{Email: "landlord@rental.test", FullName: "Demo Landlord", Roles: []string{authz.RoleLandlord, authz.RoleTenant}, HasAddress: true},
		{Email: "tenant@rental.test", FullName: "Demo Tenant", Roles: []string{authz.RoleTenant}, HasAddress: true},
	}
}

// SeedDemo creates (or refreshes) the demo accounts with the password. Their emails count as verified, so they can
// log in at once. It is safe to repeat: a second run keeps the accounts and sets the password again. The roles and
// provinces must be seeded first (roles are referenced, the ward is taken from the seeded divisions).
func SeedDemo(ctx context.Context, pool *pgxpool.Pool, password, termsVersion string, now time.Time) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return NewStore(pool).InTx(ctx, func(q *sqlcgen.Queries) error {
		var ward *int32
		if code, err := q.FirstWardCode(ctx); err == nil {
			ward = &code
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		for _, account := range DemoAccounts() {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			last := initialRole(account.Roles, "")
			params := sqlcgen.UpsertDemoUserParams{
				ID: id, Email: &account.Email, PasswordHash: &hash, FullName: account.FullName,
				LastActiveRole: &last, TermsVersion: &termsVersion, Now: now,
			}
			if account.HasAddress {
				params.WardCode = ward
			}
			userID, err := q.UpsertDemoUser(ctx, params)
			if err != nil {
				return fmt.Errorf("seed demo account %s: %w", account.Email, err)
			}
			if err := assignRoles(ctx, q, userID, account.Roles, now); err != nil {
				return fmt.Errorf("seed demo roles of %s: %w", account.Email, err)
			}
		}
		return nil
	})
}
