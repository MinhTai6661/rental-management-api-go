package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/model"
	"rental-management/pkg/authz"
)

// CatalogSeeder writes the role and permission catalog.
type CatalogSeeder interface {
	SeedCatalog(ctx context.Context, roles []model.Role, perms []model.Permission, grants map[string][]string) error
}

// DemoUserRepository is what the demo accounts need from the user repository.
type DemoUserRepository interface {
	roleGranter
	UpsertDemo(ctx context.Context, u model.DemoUser) (uuid.UUID, error)
	// FirstWardCode returns the first seeded ward, or model.ErrNotFound when there is none.
	FirstWardCode(ctx context.Context) (int32, error)
}

// SeedCatalog makes the roles, permissions and role_permissions tables match the catalog in model: it adds what
// is missing, updates what changed and removes permissions the catalog no longer has. It runs in one transaction
// and is safe to repeat. Users are not touched.
func SeedCatalog(ctx context.Context, tx TxManager, seeder CatalogSeeder) error {
	roles := make([]model.Role, 0, len(authz.Roles()))
	for _, code := range authz.Roles() {
		roles = append(roles, model.Role{Code: code, Seniority: authz.Seniority(code), IsStaff: authz.IsStaff(code)})
	}
	return tx.WithinTx(ctx, func(ctx context.Context) error {
		return seeder.SeedCatalog(ctx, roles, model.Permissions(), model.RolePermissions())
	})
}

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

// SeedDemo creates (or refreshes) the demo accounts with the password and returns their user ids by email. Their
// emails count as verified, so they can log in at once. It is safe to repeat: a second run keeps the accounts and
// sets the password again. The roles and provinces must be seeded first (roles are referenced, the ward is taken
// from the seeded divisions).
func SeedDemo(ctx context.Context, tx TxManager, users DemoUserRepository, password, termsVersion string, now time.Time) (map[string]uuid.UUID, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]uuid.UUID, len(DemoAccounts()))
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		var ward *int32
		if code, err := users.FirstWardCode(ctx); err == nil {
			ward = &code
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}

		for _, account := range DemoAccounts() {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			demo := model.DemoUser{
				ID: id, Email: account.Email, PasswordHash: hash, FullName: account.FullName,
				LastActiveRole: initialRole(account.Roles, ""), TermsVersion: termsVersion, Now: now,
			}
			if account.HasAddress {
				demo.WardCode = ward
			}
			userID, err := users.UpsertDemo(ctx, demo)
			if err != nil {
				return fmt.Errorf("seed demo account %s: %w", account.Email, err)
			}
			if err := assignRoles(ctx, users, userID, account.Roles, now); err != nil {
				return fmt.Errorf("seed demo roles of %s: %w", account.Email, err)
			}
			ids[account.Email] = userID
		}
		return nil
	})
	return ids, err
}
