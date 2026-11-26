//go:build integration

package reportpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/report"
	"rental-management/internal/domain/shared"
)

type fixture struct {
	pool                                          *pgxpool.Pool
	landlord, otherLandlord, tenant, admin, super uuid.UUID
	room, otherRoom                               uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true), ('landlord', 3, false), ('tenant', 5, false)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string, roles ...string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, role := range roles {
			if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, role); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	f := &fixture{pool: pool}
	f.landlord, f.otherLandlord = user("l@example.vn", "Landlord", "landlord", "tenant"), user("o@example.vn", "Other", "landlord")
	f.tenant, f.admin, f.super = user("t@example.vn", "Tenant", "tenant"), user("a@example.vn", "Admin", "admin"), user("s@example.vn", "Super", "super_admin")
	room := func(owner uuid.UUID, name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, minimum_rental_period, rental_period_unit)
			VALUES ($1, $2, 4, '12 Le Loi', 3000000, 1, 1, 'month') RETURNING id`, owner, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.room, f.otherRoom = room(f.landlord, "P1"), room(f.otherLandlord, "X1")
	return f
}

func (f *fixture) rent(t *testing.T, room uuid.UUID, owner uuid.UUID, status string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, display_order)
		VALUES ($1, $2, $3, $4, 1000000, current_date, 0)`, room, owner, f.tenant, status); err != nil {
		t.Fatal(err)
	}
}

func at() time.Time { return time.Now().Truncate(time.Microsecond) }

func TestReportRoundTripAndDeletedVisibility(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewReportRepository(f.pool)
	room := f.room
	id, err := repo.Insert(ctx, domain.NewReport{ReporterID: f.tenant, Kind: domain.KindRoom, RoomID: &room, Subject: "Leak", Body: "Tap", At: at()})
	if err != nil {
		t.Fatal(err)
	}
	feature, err := repo.Insert(ctx, domain.NewReport{ReporterID: f.tenant, Kind: domain.KindFeature, Subject: "Dark mode", Body: "100% please", At: at()})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusOpen || got.RoomName != "P1" || got.RoomLandlordID == nil || *got.RoomLandlordID != f.landlord ||
		got.Reporter.Name != "Tenant" || got.Reporter.IsStaff || got.Reporter.IsLandlord || got.RepliesCount != 0 {
		t.Errorf("view = %+v", got)
	}
	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}

	reply, err := repo.InsertReply(ctx, id, f.landlord, "On it", at())
	if err != nil || reply.Author.Name != "Landlord" || !reply.Author.IsLandlord {
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	if _, err := repo.InsertReply(ctx, id, f.tenant, "Thanks", at().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	replies, total, err := repo.ListReplies(ctx, id, 1, 0)
	if err != nil || total != 2 || len(replies) != 1 || replies[0].Body != "Thanks" {
		t.Errorf("replies = %+v %d %v", replies, total, err)
	}
	if got, _ = repo.Get(ctx, id); got.RepliesCount != 2 {
		t.Errorf("replies_count = %d", got.RepliesCount)
	}

	if err := repo.SetStatus(ctx, id, domain.StatusResolved, at()); err != nil {
		t.Fatal(err)
	}
	if locked, err := repo.Lock(ctx, id); err != nil || locked.Status != domain.StatusResolved || locked.Deleted() {
		t.Errorf("locked = %+v, %v", locked, err)
	}

	mine, total, err := repo.ListMine(ctx, f.tenant, domain.Filter{}, 10, 0)
	if err != nil || total != 2 || len(mine) != 2 || mine[0].ID != feature {
		t.Errorf("mine = %+v %d %v", mine, total, err)
	}
	// "%" is a character to find, not a wildcard; the search ignores the case.
	if _, total, _ = repo.ListMine(ctx, f.tenant, domain.Filter{Search: "%"}, 10, 0); total != 1 {
		t.Errorf("percent search = %d", total)
	}
	if _, total, _ = repo.ListMine(ctx, f.tenant, domain.Filter{Search: "dark"}, 10, 0); total != 1 {
		t.Errorf("case search = %d", total)
	}
	resolved := domain.StatusResolved
	if _, total, _ = repo.ListMine(ctx, f.tenant, domain.Filter{Status: &resolved}, 10, 0); total != 1 {
		t.Errorf("status filter = %d", total)
	}

	if err := repo.SoftDelete(ctx, id, at()); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = repo.ListMine(ctx, f.tenant, domain.Filter{}, 10, 0); total != 1 {
		t.Errorf("the author's list keeps a deleted report: %d", total)
	}
	landlord := f.landlord
	inbox, total, err := repo.ListInbox(ctx, domain.InboxScope{LandlordID: &landlord}, domain.Filter{}, 10, 0)
	if err != nil || total != 1 || !inbox[0].Deleted() {
		t.Errorf("the recipient's inbox loses a deleted report: %+v %d %v", inbox, total, err)
	}
}

func TestInboxScopes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewReportRepository(f.pool)
	own, other := f.room, f.otherRoom
	for _, n := range []domain.NewReport{
		{ReporterID: f.tenant, Kind: domain.KindRoom, RoomID: &own, Subject: "a", Body: "a"},
		{ReporterID: f.tenant, Kind: domain.KindRoom, RoomID: &other, Subject: "b", Body: "b"},
		{ReporterID: f.tenant, Kind: domain.KindFeature, Subject: "c", Body: "c"},
	} {
		n.At = at()
		if _, err := repo.Insert(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	landlord, nobody := f.landlord, uuid.New()
	cases := []struct {
		name  string
		scope domain.InboxScope
		want  int
	}{
		{"everything", domain.InboxScope{Everything: true}, 3},
		{"features", domain.InboxScope{Features: true}, 1},
		{"one landlord", domain.InboxScope{LandlordID: &landlord}, 1},
		{"a landlord without rooms", domain.InboxScope{LandlordID: &nobody}, 0},
		{"nothing", domain.InboxScope{}, 0},
	}
	for _, c := range cases {
		got, total, err := repo.ListInbox(ctx, c.scope, domain.Filter{}, 10, 0)
		if err != nil || total != c.want || len(got) != c.want {
			t.Errorf("%s: %d reports, total %d, %v", c.name, len(got), total, err)
		}
	}
}

func TestSchemaRefusesARoomReportWithoutRoomAndAFeatureReportWithOne(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewReportRepository(f.pool)
	room := f.room
	if _, err := repo.Insert(ctx, domain.NewReport{ReporterID: f.tenant, Kind: domain.KindRoom, Subject: "a", Body: "b", At: at()}); err == nil {
		t.Error("a room report needs a room")
	}
	if _, err := repo.Insert(ctx, domain.NewReport{ReporterID: f.tenant, Kind: domain.KindFeature, RoomID: &room, Subject: "a", Body: "b", At: at()}); err == nil {
		t.Error("a feature report has no room")
	}
}

func TestRoomReader(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	rooms := NewRoomReader(f.pool)
	if _, err := rooms.RentedBy(ctx, f.tenant, f.room); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("never rented = %v", err)
	}
	f.rent(t, f.room, f.landlord, "ended")
	got, err := rooms.RentedBy(ctx, f.tenant, f.room)
	if err != nil || got.Name != "P1" || got.LandlordID != f.landlord {
		t.Errorf("rented = %+v, %v", got, err)
	}
	if _, err := rooms.RentedBy(ctx, f.tenant, f.otherRoom); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another room = %v", err)
	}
	f.rent(t, f.otherRoom, f.otherLandlord, "pending")
	if _, err := rooms.RentedBy(ctx, f.tenant, f.otherRoom); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a pending registration is not a rental: %v", err)
	}
	if first, err := rooms.FirstRentedFrom(ctx, f.tenant, f.landlord); err != nil || first.ID != f.room {
		t.Errorf("first = %+v, %v", first, err)
	}
	if _, err := rooms.FirstRentedFrom(ctx, f.tenant, f.otherLandlord); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("first of a landlord with no rental = %v", err)
	}
}

func TestLockAndTransactionJoin(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewReportRepository(f.pool)
	id, _ := repo.Insert(ctx, domain.NewReport{ReporterID: f.tenant, Kind: domain.KindFeature, Subject: "s", Body: "b", At: at()})
	boom := errors.New("boom")
	err := postgres.NewTxManager(f.pool).WithinTx(ctx, func(ctx context.Context) error {
		if _, err := repo.Lock(ctx, id); err != nil {
			return err
		}
		if _, err := repo.InsertReply(ctx, id, f.tenant, "x", at()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, total, _ := repo.ListReplies(ctx, id, 10, 0); total != 0 {
		t.Errorf("a rolled back reply stays: %d", total)
	}
}
