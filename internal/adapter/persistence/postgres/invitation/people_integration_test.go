//go:build integration

package invitationpg

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/domain/shared"
)

func newUser(t *testing.T, pool *pgxpool.Pool, email, status string, accepts, verified bool, roles ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name, status, accepts_invitations, email_verified_at)
		VALUES ($1, 'Some Renter', $2, $3, CASE WHEN $4 THEN now() END) RETURNING id`, email, status, accepts, verified).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, role); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestPeopleReadsAccountsByIDAndEmail(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	if _, err := pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ('landlord', 3, false), ('tenant', 5, false)`); err != nil {
		t.Fatal(err)
	}
	id := newUser(t, pool, "renter@example.com", "active", true, true, "tenant", "landlord")
	off := newUser(t, pool, "quiet@example.com", "inactive", false, false)
	people := NewPeopleRepository(pool)

	for name, get := range map[string]func() (uuid.UUID, error){
		"by id": func() (uuid.UUID, error) { p, err := people.ByID(ctx, id); return p.ID, err },
		"by email": func() (uuid.UUID, error) {
			p, err := people.ByEmail(ctx, "renter@example.com")
			return p.ID, err
		},
	} {
		if got, err := get(); err != nil || got != id {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
	p, err := people.ByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Roles, []string{"landlord", "tenant"}) || !p.Active || !p.AcceptsInvitations || !p.EmailVerified ||
		p.Email == nil || *p.Email != "renter@example.com" || p.FullName != "Some Renter" {
		t.Errorf("person = %+v", p)
	}
	q, err := people.ByID(ctx, off)
	if err != nil {
		t.Fatal(err)
	}
	if q.Active || q.AcceptsInvitations || q.EmailVerified || len(q.Roles) != 0 || q.Roles == nil {
		t.Errorf("an inactive opted-out account without roles: %+v", q)
	}

	if _, err := people.ByID(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := people.ByEmail(ctx, "nobody@example.com"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown email: %v", err)
	}
	// A partial address finds nothing: lookups are exact.
	if _, err := people.ByEmail(ctx, "renter@"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("partial email: %v", err)
	}

	for email, want := range map[string]bool{"quiet@example.com": true, "renter@example.com": false, "nobody@example.com": false} {
		if got, err := people.OptedOut(ctx, email); err != nil || got != want {
			t.Errorf("OptedOut(%q) = %v, %v; want %v", email, got, err, want)
		}
	}
}

func TestEmailBlocksAreIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	blocks := NewEmailBlockRepository(pool)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if ok, err := blocks.Exists(ctx, hash); err != nil || ok {
		t.Fatalf("before: %v, %v", ok, err)
	}
	for range 2 {
		if err := blocks.Add(ctx, hash); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := blocks.Exists(ctx, hash); err != nil || !ok {
		t.Errorf("after: %v, %v", ok, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invitation_email_blocks`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows %d, %v", n, err)
	}
}
