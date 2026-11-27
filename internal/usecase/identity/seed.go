package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Seeder writes the role and permission catalog and the demo accounts.
type Seeder struct {
	tx      TxManager
	catalog CatalogRepository
	users   DemoUserRepository
	hasher  PasswordHasher
}

// NewSeeder returns a seeder.
func NewSeeder(tx TxManager, catalog CatalogRepository, users DemoUserRepository, hasher PasswordHasher) *Seeder {
	return &Seeder{tx: tx, catalog: catalog, users: users, hasher: hasher}
}

// SeedCatalog makes the roles, permissions and role_permissions tables match the catalog in the domain: it adds what
// is missing, updates what changed and removes permissions the catalog no longer has. It runs in one transaction
// and is safe to repeat. Users are not touched.
func (s *Seeder) SeedCatalog(ctx context.Context) error {
	roles := make([]domain.Role, 0, len(authz.Roles()))
	for _, code := range authz.Roles() {
		roles = append(roles, domain.Role{Code: code, Seniority: authz.Seniority(code), IsStaff: authz.IsStaff(code)})
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		return s.catalog.SeedCatalog(ctx, roles, domain.Permissions(), domain.RolePermissions())
	})
}

// SeedDemo creates (or refreshes) the demo accounts with the password and returns their user ids by email. Their
// emails count as verified, so they can log in at once. It is safe to repeat: a second run keeps the accounts and
// sets the password again. The roles and provinces must be seeded first (roles are referenced, the ward is taken
// from the seeded divisions).
func (s *Seeder) SeedDemo(ctx context.Context, password, termsVersion string, now time.Time) (map[string]uuid.UUID, error) {
	return s.seedAccounts(ctx, domain.DemoAccounts(), password, termsVersion, now)
}

// SeedSamples creates (or refreshes) the sample accounts the same way, with their own password.
func (s *Seeder) SeedSamples(ctx context.Context, password, termsVersion string, now time.Time) (map[string]uuid.UUID, error) {
	return s.seedAccounts(ctx, domain.SampleAccounts(), password, termsVersion, now)
}

func (s *Seeder) seedAccounts(ctx context.Context, accounts []domain.DemoAccount, password, termsVersion string, now time.Time) (map[string]uuid.UUID, error) {
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]uuid.UUID, len(accounts))
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var ward *int32
		if code, err := s.users.FirstWardCode(ctx); err == nil {
			ward = &code
		} else if !errors.Is(err, shared.ErrNotFound) {
			return err
		}

		for _, account := range accounts {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			demo := domain.DemoUser{
				ID: id, Email: account.Email, PasswordHash: hash, FullName: account.FullName,
				LastActiveRole: domain.InitialRole(account.Roles, ""), TermsVersion: termsVersion, Now: now,
			}
			if account.HasAddress {
				demo.WardCode = ward
			}
			userID, err := s.users.UpsertDemo(ctx, demo)
			if err != nil {
				return fmt.Errorf("seed demo account %s: %w", account.Email, err)
			}
			if err := assignRoles(ctx, s.users, userID, account.Roles, now); err != nil {
				return fmt.Errorf("seed demo roles of %s: %w", account.Email, err)
			}
			ids[account.Email] = userID
		}
		return nil
	})
	return ids, err
}
