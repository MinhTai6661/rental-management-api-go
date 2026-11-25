//go:build integration

package managerpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/manager"
	"rental-management/internal/domain/shared"
)

type repos struct {
	pool                    *pgxpool.Pool
	landlord, other, mgr    uuid.UUID
	room1, room2, otherRoom uuid.UUID
}

func newRepos(t *testing.T) *repos {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('landlord', 3, false), ('manager', 4, false), ('tenant', 5, false)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	r := &repos{pool: pool, landlord: user("l@example.vn", "Landlord"), other: user("o@example.vn", "Other"), mgr: user("m@example.vn", "Manager")}
	room := func(owner uuid.UUID, name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, minimum_rental_period, rental_period_unit)
			VALUES ($1, $2, 4, '12 Le Loi', 3000000, 1, 1, 'month') RETURNING id`, owner, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	r.room1, r.room2, r.otherRoom = room(r.landlord, "P1"), room(r.landlord, "P2"), room(r.other, "X1")
	return r
}

func hash64(c string) string {
	out := ""
	for len(out) < 64 {
		out += c
	}
	return out[:64]
}

func (r *repos) invite(t *testing.T, hash string, rooms ...uuid.UUID) uuid.UUID {
	t.Helper()
	sealed := "sealed-" + hash
	id, err := NewInvitationRepository(r.pool).Insert(context.Background(), domain.Invitation{
		LandlordID: r.landlord, Status: domain.InvitationPending, TokenHash: hash, Sealed: &sealed, NotifyLandlord: true,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Truncate(time.Microsecond), RoomIDs: rooms,
		Capabilities: []domain.Capability{domain.ContractsPropose, domain.PaymentsReview},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInvitationStoresRoomsAndCapabilitiesAndIsScopedToItsLandlord(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	id := r.invite(t, hash64("a"), r.room1, r.room2)

	got, err := repo.Get(ctx, r.landlord, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.InvitationPending || len(got.Rooms) != 2 || len(got.Capabilities) != 2 || !got.NotifyLandlord || got.Sealed == nil {
		t.Errorf("invitation = %+v", got)
	}
	if _, err := repo.Get(ctx, r.other, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := repo.FindByTokenHash(ctx, hash64("a")); err != nil {
		t.Errorf("find by hash: %v", err)
	}
	if _, err := repo.FindByTokenHash(ctx, hash64("b")); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	if _, err := repo.FindForRecipient(ctx, id, r.mgr); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a public invitation has no recipient: %v", err)
	}
	items, total, err := repo.List(ctx, r.landlord, nil, 10, 0)
	if err != nil || total != 1 || len(items) != 1 {
		t.Errorf("list: %d %v %v", total, items, err)
	}
	if _, total, _ := repo.List(ctx, r.other, nil, 10, 0); total != 0 {
		t.Errorf("another landlord lists %d", total)
	}
}

func TestInvitationLocksAndTakesRoomsOutOfWaitingOnes(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	tx := postgres.NewTxManager(r.pool)
	id := r.invite(t, hash64("a"), r.room1, r.room2)

	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		open, err := repo.LockOpenOfRooms(ctx, []uuid.UUID{r.room1}, time.Now())
		if err != nil || len(open) != 1 || open[0].ID != id {
			t.Fatalf("open of the room: %v %v", open, err)
		}
		return repo.RemoveRooms(ctx, id, []uuid.UUID{r.room1})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, r.landlord, id)
	if len(got.Rooms) != 1 || got.Rooms[0].ID != r.room2 {
		t.Errorf("rooms left: %+v", got.Rooms)
	}
	// An expired invitation holds no room.
	old, _ := repo.ForUpdate(ctx, id)
	old.ExpiresAt = time.Now().Add(-time.Hour)
	if err := repo.Save(ctx, old); err != nil {
		t.Fatal(err)
	}
	open, err := repo.LockOpenOfRooms(ctx, []uuid.UUID{r.room2}, time.Now())
	if err != nil || len(open) != 0 {
		t.Errorf("expired holds a room: %v %v", open, err)
	}
}

func TestAssignmentsOneActiveManagerPerRoomAndRevokeFreesIt(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewAssignmentRepository(r.pool)
	now := time.Now().Truncate(time.Microsecond)

	made, err := repo.Insert(ctx, r.landlord, r.mgr, []uuid.UUID{r.room1, r.room2}, true, []domain.Capability{domain.PaymentsReview}, now)
	if err != nil || len(made) != 2 {
		t.Fatalf("insert: %v %v", made, err)
	}
	// The second manager of a room is a unique violation (the use case checks first; the index is the last defence).
	_, err = repo.Insert(ctx, r.landlord, r.other, []uuid.UUID{r.room1}, true, nil, now)
	var ce *shared.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("second manager: %v", err)
	}
	managed, err := repo.ManagedRooms(ctx, []uuid.UUID{r.room1, r.otherRoom})
	if err != nil || len(managed) != 1 || managed[0].Name != "P1" {
		t.Errorf("managed rooms: %v %v", managed, err)
	}
	a, err := repo.Of(ctx, r.mgr, r.room1)
	if err != nil || !a.Can(domain.PaymentsReview) || a.Can(domain.ContractsPropose) {
		t.Errorf("of: %+v %v", a, err)
	}
	if _, err := repo.Of(ctx, r.mgr, r.otherRoom); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a room not handed over: %v", err)
	}
	if n, _ := repo.CountActive(ctx, r.mgr); n != 2 {
		t.Errorf("active = %d", n)
	}
	if _, _, err := repo.ManagedList(ctx, r.mgr, nil, 10, 0); err != nil {
		t.Errorf("managed list: %v", err)
	}

	// Settings are saved with their capabilities; a hand-over of another landlord is not found.
	a.Capabilities, a.NotifyLandlord = []domain.Capability{domain.ContractsPropose, domain.ContractsCreate}, false
	if err := repo.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	d, err := repo.Get(ctx, r.landlord, a.ID)
	if err != nil || d.NotifyLandlord || len(d.Capabilities) != 2 || d.Manager.FullName != "Manager" {
		t.Errorf("get: %+v %v", d, err)
	}
	if _, err := repo.Get(ctx, r.other, a.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's hand-over: %v", err)
	}
	groups, total, err := repo.Groups(ctx, r.landlord, 10, 0)
	if err != nil || total != 1 || len(groups) != 1 || groups[0].RoomCount != 2 {
		t.Errorf("groups: %+v %d %v", groups, total, err)
	}

	if err := repo.Revoke(ctx, []uuid.UUID{a.ID}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Of(ctx, r.mgr, r.room1); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("revoked still holds the room: %v", err)
	}
	if _, err := repo.Insert(ctx, r.landlord, r.other, []uuid.UUID{r.room1}, true, nil, now); err != nil {
		t.Errorf("a revoked room can be handed to somebody else: %v", err)
	}
}

func TestRolesGrantAndRevokeClearTheLastRoleUsed(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	roles := NewRoles(r.pool)
	if err := roles.Grant(ctx, r.mgr); err != nil {
		t.Fatal(err)
	}
	if err := roles.Grant(ctx, r.mgr); err != nil {
		t.Fatalf("granting twice: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE users SET last_active_role = 'manager' WHERE id = $1`, r.mgr); err != nil {
		t.Fatal(err)
	}
	if err := roles.Revoke(ctx, r.mgr); err != nil {
		t.Fatal(err)
	}
	var n int
	var last *string
	_ = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM user_roles WHERE user_id = $1 AND role_code = 'manager'), last_active_role FROM users WHERE id = $1`, r.mgr).Scan(&n, &last)
	if n != 0 || last != nil {
		t.Errorf("role left: %d, last used: %v", n, last)
	}
}
