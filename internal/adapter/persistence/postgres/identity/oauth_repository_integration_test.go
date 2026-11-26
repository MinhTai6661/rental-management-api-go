//go:build integration

package identitypg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
)

func newSocialUser(t *testing.T, pool *pgxpool.Pool, repo *UserRepository, email *string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ('tenant', 5, false) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := repo.CreateSocial(ctx, domain.NewSocialUser{ID: id, Email: email, FullName: "Social", LastActiveRole: "tenant", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOAuthAccountsLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	users, repo := NewUserRepository(pool), NewOAuthAccountRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	email := "ann@example.vn"
	ann := newSocialUser(t, pool, users, &email)
	bob := newSocialUser(t, pool, users, nil)

	if _, err := repo.FindByProvider(ctx, "google", "g-1"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("unknown provider account: %v", err)
	}
	if err := repo.Create(ctx, domain.OAuthAccount{UserID: ann, Provider: "google", ProviderUserID: "g-1"}, now); err != nil {
		t.Fatal(err)
	}
	row, err := repo.FindByProvider(ctx, "google", "g-1")
	if err != nil || row.UserID != ann || !row.Linked() || !row.LinkedAt.Equal(now) {
		t.Fatalf("found %+v, %v", row, err)
	}

	// One provider account opens one account; an account has one account of each provider.
	for name, a := range map[string]domain.OAuthAccount{
		"the same provider account for another user": {UserID: bob, Provider: "google", ProviderUserID: "g-1"},
		"another google account for the same user":   {UserID: ann, Provider: "google", ProviderUserID: "g-2"},
	} {
		if err := repo.Create(ctx, a, now); !shared.IsUnique(err) {
			t.Errorf("%s: want a unique violation, got %v", name, err)
		}
	}
	if err := repo.Create(ctx, domain.OAuthAccount{UserID: ann, Provider: "facebook", ProviderUserID: "x"}, now); err == nil {
		t.Error("an unknown provider must be refused by the database")
	}

	// Unlink keeps the row; relink and replace switch it back on.
	later := now.Add(time.Hour)
	if err := repo.Unlink(ctx, ann, "google", later); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListOfUser(ctx, ann)
	if err != nil || len(rows) != 1 || rows[0].Linked() || rows[0].UnlinkedAt == nil || !rows[0].UnlinkedAt.Equal(later) {
		t.Fatalf("after unlink: %+v, %v", rows, err)
	}
	if known, _ := repo.FindByProvider(ctx, "google", "g-1"); known.Linked() {
		t.Error("the unlinked row must still be found, and unlinked")
	}
	if err := repo.Relink(ctx, ann, "google", later); err != nil {
		t.Fatal(err)
	}
	if rows, _ = repo.ListOfUser(ctx, ann); !rows[0].Linked() || rows[0].ProviderUserID != "g-1" {
		t.Fatalf("after relink: %+v", rows)
	}
	if err := repo.Unlink(ctx, ann, "google", later); err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, ann, "google", "g-2", later); err != nil {
		t.Fatal(err)
	}
	if rows, _ = repo.ListOfUser(ctx, ann); len(rows) != 1 || !rows[0].Linked() || rows[0].ProviderUserID != "g-2" {
		t.Fatalf("after replace: %+v", rows)
	}
	if _, err := repo.FindByProvider(ctx, "google", "g-1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("the replaced provider account is gone: %v", err)
	}

	// The rows go with the account, and the repository joins the transaction in ctx (the locking read needs one).
	locked, err := repo.ListOfUserForUpdate(ctx, ann)
	if err != nil || len(locked) != 1 {
		t.Fatalf("locking read: %+v, %v", locked, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ann); err != nil {
		t.Fatal(err)
	}
	if rows, _ = repo.ListOfUser(ctx, ann); len(rows) != 0 {
		t.Errorf("rows must cascade with the user: %+v", rows)
	}
}

func TestCreateSocialMakesAnAccountWithoutPasswordAndTerms(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	users := NewUserRepository(pool)
	email := "zed@example.vn"
	withEmail := newSocialUser(t, pool, users, &email)
	without := newSocialUser(t, pool, users, nil)

	var password, terms *string
	var verified bool
	_ = pool.QueryRow(ctx, `SELECT password_hash, terms_version, email_verified_at IS NOT NULL FROM users WHERE id = $1`, withEmail).Scan(&password, &terms, &verified)
	if password != nil || terms != nil || !verified {
		t.Errorf("a vouched email makes a verified account without password or terms: %v %v %v", password, terms, verified)
	}
	_ = pool.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE id = $1`, without).Scan(&verified)
	if verified {
		t.Error("no email, nothing verified")
	}
	if err := users.CreateSocial(ctx, domain.NewSocialUser{ID: uuid.New(), Email: &email, FullName: "Dup", LastActiveRole: "tenant", CreatedAt: time.Now()}); !shared.IsUnique(err) {
		t.Errorf("a taken email is a unique violation: %v", err)
	}

	// Confirming from a provider verifies the email and drops the password.
	hash := "h"
	if _, err := pool.Exec(ctx, `UPDATE users SET password_hash = $1, email_verified_at = NULL WHERE id = $2`, hash, withEmail); err != nil {
		t.Fatal(err)
	}
	if err := users.ConfirmEmailFromProvider(ctx, withEmail, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT password_hash, email_verified_at IS NOT NULL FROM users WHERE id = $1`, withEmail).Scan(&password, &verified)
	if password != nil || !verified {
		t.Errorf("after confirming: %v %v", password, verified)
	}
}
