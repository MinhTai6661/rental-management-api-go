//go:build integration

package contractpg

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

type repos struct {
	pool      *pgxpool.Pool
	contracts *ContractRepository
	rooms     *RoomReader
	tx        *postgres.TxManager
	landlord  uuid.UUID
	other     uuid.UUID // another landlord
	tenant    uuid.UUID
	tenant2   uuid.UUID
	room      uuid.UUID // a room of landlord
}

func newRepos(t *testing.T) *repos {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	if _, err := pool.Exec(ctx, `INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`); err != nil {
		t.Fatal(err)
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name, phone_number) VALUES ($1, $2, '0901') RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	r := &repos{
		pool: pool, contracts: NewContractRepository(pool), rooms: NewRoomReader(pool), tx: postgres.NewTxManager(pool),
		landlord: user("l@example.vn", "Landlord"), other: user("o@example.vn", "Other"),
		tenant: user("t@example.vn", "Tenant"), tenant2: user("t2@example.vn", "Tenant Two"),
	}
	r.room = r.newRoom(t, r.landlord, "P101")
	return r
}

func (r *repos) newRoom(t *testing.T, landlord uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, minimum_rental_period, rental_period_unit,
		                   cover_key, cover_thumb_key)
		VALUES ($1, $2, 4, '12 Le Loi', 3000000, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), 2, 'month', 'k/c.webp', 'k/c_thumb.webp')
		RETURNING id`, landlord, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var (
	day   = func(s string) time.Time { d, _ := domain.ParseDate(s); return d }
	confd = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
)

func (r *repos) insert(t *testing.T, room, landlord uuid.UUID, tenant *uuid.UUID, status domain.Status, order int) uuid.UUID {
	t.Helper()
	id, err := r.contracts.Insert(context.Background(), domain.Contract{
		RoomID: room, LandlordID: landlord, TenantID: tenant, Status: status, PriceVND: 3_000_000, OccupantCount: 2,
		StartsOn: day("2026-11-23"), ConfirmedAt: &confd, DisplayOrder: order,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInsertGetAndSaveRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	deposit, notes, ends := int64(1_000_000), "bring a pet", day("2027-11-23")
	id, err := r.contracts.Insert(ctx, domain.Contract{
		RoomID: r.room, LandlordID: r.landlord, TenantID: &r.tenant, Status: domain.StatusPending, PriceVND: 3_000_000,
		DepositVND: &deposit, OccupantCount: 3, StartsOn: day("2026-11-23"), EndsOn: &ends, Notes: &notes, ConfirmedAt: &confd, DisplayOrder: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	landlordView, tenantView := domain.Viewer{UserID: r.landlord, AsLandlord: true}, domain.Viewer{UserID: r.tenant}
	d, err := r.contracts.Get(ctx, landlordView, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.StatusPending || d.PriceVND != 3_000_000 || *d.DepositVND != deposit || d.OccupantCount != 3 ||
		d.StartsOn.Format(domain.DateLayout) != "2026-11-23" || d.EndsOn.Format(domain.DateLayout) != "2027-11-23" || *d.Notes != notes || d.DisplayOrder != -1 {
		t.Errorf("contract = %+v", d.Contract)
	}
	if d.Room.Name != "P101" || d.Room.PriceVND != 3_000_000 || d.Room.MinPeriod != 2 || d.Room.PeriodUnit != domain.UnitMonth ||
		d.Room.Ward == nil || d.Room.Ward.Name != "Ba Dinh" || d.Room.Ward.Province.Name != "Ha Noi" || d.Room.Cover == nil || d.Room.Cover.ThumbKey != "k/c_thumb.webp" {
		t.Errorf("room = %+v", d.Room)
	}
	if d.Tenant == nil || d.Tenant.FullName != "Tenant" || *d.Tenant.Email != "t@example.vn" || d.Landlord.FullName != "Landlord" {
		t.Errorf("people = %+v %+v", d.Tenant, d.Landlord)
	}
	if _, err := r.contracts.Get(ctx, tenantView, id); err != nil {
		t.Errorf("the tenant sees their contract: %v", err)
	}
	for _, v := range []domain.Viewer{
		{UserID: r.other, AsLandlord: true}, {UserID: r.other}, {UserID: r.landlord}, {UserID: r.tenant, AsLandlord: true},
	} {
		if _, err := r.contracts.Get(ctx, v, id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("viewer %+v must not see it: %v", v, err)
		}
	}

	// Save writes the changeable fields, the cancellation columns included.
	locked, err := r.contracts.ForUpdate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	locked.RequestCancellation(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC), ptr("moving"))
	locked.Status, locked.Notes, locked.EndsOn = domain.StatusActive, nil, nil
	if err := r.contracts.Save(ctx, locked); err != nil {
		t.Fatal(err)
	}
	again, _ := r.contracts.ForUpdate(ctx, id)
	if again.Status != domain.StatusActive || again.Notes != nil || again.EndsOn != nil || *again.Cancellation.Status != domain.CancellationRequested ||
		*again.Cancellation.Reason != "moving" || again.Cancellation.RequestedAt == nil {
		t.Errorf("saved = %+v", again)
	}
}

func ptr[T any](v T) *T { return &v }

func TestSearchScopingFiltersSortAndPaging(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	p2, p3 := r.newRoom(t, r.landlord, "P102"), r.newRoom(t, r.landlord, "P103")
	foreignRoom := r.newRoom(t, r.other, "Z1")
	a := r.insert(t, r.room, r.landlord, &r.tenant, domain.StatusPending, 3)
	b := r.insert(t, p2, r.landlord, &r.tenant2, domain.StatusActive, 1)
	c := r.insert(t, p3, r.landlord, nil, domain.StatusActive, 2) // blank
	foreign := r.insert(t, foreignRoom, r.other, &r.tenant, domain.StatusPending, 1)
	if _, err := r.pool.Exec(ctx, `UPDATE contracts SET tenant_name = 'Mr Blank' WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE contracts SET cancellation_status = 'requested' WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	landlord := domain.Viewer{UserID: r.landlord, AsLandlord: true}
	ids := func(items []domain.Detail) []uuid.UUID {
		out := make([]uuid.UUID, len(items))
		for i, d := range items {
			out[i] = d.ID
		}
		return out
	}
	search := func(v domain.Viewer, q domain.Query) ([]uuid.UUID, int) {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = 50
		}
		items, total, err := r.contracts.Search(ctx, v, q)
		if err != nil {
			t.Fatal(err)
		}
		return ids(items), total
	}

	// The landlord's own order, never somebody else's contracts.
	if got, total := search(landlord, domain.Query{}); !slices.Equal(got, []uuid.UUID{b, c, a}) || total != 3 {
		t.Errorf("landlord list = %v (%d)", got, total)
	}
	if got, _ := search(domain.Viewer{UserID: r.other, AsLandlord: true}, domain.Query{}); !slices.Equal(got, []uuid.UUID{foreign}) {
		t.Errorf("other landlord list = %v", got)
	}
	// The tenant sees their own contracts, across landlords, latest start first.
	if got, total := search(domain.Viewer{UserID: r.tenant}, domain.Query{Sort: "-starts_on"}); total != 2 || !slices.Contains(got, a) || !slices.Contains(got, foreign) {
		t.Errorf("tenant list = %v", got)
	}
	// Filters.
	if got, _ := search(landlord, domain.Query{Statuses: []domain.Status{domain.StatusActive}}); !slices.Equal(got, []uuid.UUID{b, c}) {
		t.Errorf("status filter = %v", got)
	}
	if got, _ := search(landlord, domain.Query{Statuses: []domain.Status{domain.StatusPending, domain.StatusEnded}}); !slices.Equal(got, []uuid.UUID{a}) {
		t.Errorf("statuses filter = %v", got)
	}
	cancel := domain.CancellationRequested
	if got, _ := search(landlord, domain.Query{CancellationStatus: &cancel}); !slices.Equal(got, []uuid.UUID{b}) {
		t.Errorf("cancellation filter = %v", got)
	}
	if got, _ := search(landlord, domain.Query{RoomID: &p3}); !slices.Equal(got, []uuid.UUID{c}) {
		t.Errorf("room filter = %v", got)
	}
	for _, term := range []string{"p102", "tenant two", "t2@example", "mr blank"} {
		like := domain.EscapeLike(term)
		if got, _ := search(landlord, domain.Query{SearchLike: &like}); len(got) != 1 {
			t.Errorf("search %q = %v", term, got)
		}
	}
	if like := "no-such-thing"; true {
		if got, total := search(landlord, domain.Query{SearchLike: &like}); len(got) != 0 || total != 0 {
			t.Errorf("search nothing = %v", got)
		}
	}
	var buildingID uuid.UUID
	if err := r.pool.QueryRow(ctx, `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'B', 'x', 1) RETURNING id`, r.landlord).Scan(&buildingID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE rooms SET building_id = $1 WHERE id = $2`, buildingID, p2); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(landlord, domain.Query{BuildingID: &buildingID}); !slices.Equal(got, []uuid.UUID{b}) {
		t.Errorf("building filter = %v", got)
	}
	if got, _ := search(landlord, domain.Query{NoBuilding: true}); !slices.Equal(got, []uuid.UUID{c, a}) {
		t.Errorf("no building filter = %v", got)
	}
	// Paging reports the total of the whole filter.
	if got, total := search(landlord, domain.Query{Limit: 2, Offset: 2}); len(got) != 1 || total != 3 {
		t.Errorf("page 2 = %v (%d)", got, total)
	}
	// Sorting by a column.
	if _, err := r.pool.Exec(ctx, `UPDATE contracts SET starts_on = '2026-12-01' WHERE id = $1`, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(landlord, domain.Query{Sort: "-starts_on"}); got[0] != a {
		t.Errorf("-starts_on = %v", got)
	}
	if got, _ := search(landlord, domain.Query{Sort: "starts_on"}); got[2] != a {
		t.Errorf("starts_on = %v", got)
	}
	// An unconfirmed contract and one of a deleted room are hidden from the landlord (the tenant still sees them).
	if _, err := r.pool.Exec(ctx, `UPDATE contracts SET confirmed_at = NULL WHERE id = $1`, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(landlord, domain.Query{}); slices.Contains(got, a) {
		t.Error("an unconfirmed contract is hidden")
	}
	if _, err := r.pool.Exec(ctx, `UPDATE rooms SET deleted_at = now() WHERE id = $1`, p2); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(landlord, domain.Query{}); slices.Contains(got, b) {
		t.Error("a deleted room hides its contracts")
	}
	if got, _ := search(domain.Viewer{UserID: r.tenant2}, domain.Query{}); !slices.Equal(got, []uuid.UUID{b}) {
		t.Errorf("the tenant keeps their history: %v", got)
	}
	// A deleted contract is gone for everybody.
	if err := r.contracts.SoftDelete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.contracts.Get(ctx, landlord, c); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("deleted contract: %v", err)
	}
	if _, err := r.contracts.ForUpdate(ctx, c); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("deleted contract lock: %v", err)
	}
}

func TestConstraintsKeepOneRunningContractPerRoom(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	insert := func(tenant *uuid.UUID, status domain.Status) error {
		_, err := r.contracts.Insert(ctx, domain.Contract{
			RoomID: r.room, LandlordID: r.landlord, TenantID: tenant, Status: status, PriceVND: 1, OccupantCount: 1, StartsOn: day("2026-11-23"),
		})
		return err
	}
	if err := insert(&r.tenant, domain.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := insert(&r.tenant2, domain.StatusActive); !shared.IsUnique(err) {
		t.Errorf("a second running tenant contract: %v", err)
	}
	// One open registration per tenant and room (pending or running), but history does not count.
	if err := insert(&r.tenant, domain.StatusPending); !shared.IsUnique(err) {
		t.Errorf("a second open registration: %v", err)
	}
	if err := insert(&r.tenant, domain.StatusEnded); err != nil {
		t.Errorf("an ended contract is history: %v", err)
	}
	if err := insert(&r.tenant2, domain.StatusPending); err != nil {
		t.Errorf("another tenant may wait: %v", err)
	}
	// A blank contract needs no tenant and runs beside nothing else of its kind.
	if err := insert(nil, domain.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := insert(nil, domain.StatusActive); !shared.IsUnique(err) {
		t.Errorf("a second blank contract: %v", err)
	}
	// A blank contract is never pending or rejected.
	for _, st := range []domain.Status{domain.StatusPending, domain.StatusRejected} {
		if err := insert(nil, st); err == nil {
			t.Errorf("a blank %s contract must be refused", st)
		}
	}
	// A deleted contract frees its place.
	occ, _ := r.contracts.Occupancy(ctx, r.room)
	if occ.Running != 2 || occ.WithTenant != 1 {
		t.Errorf("occupancy = %+v", occ)
	}
}

func TestOccupancyRegistrationAndRejection(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	if occ, err := r.contracts.Occupancy(ctx, r.room); err != nil || occ != (domain.Occupancy{}) {
		t.Fatalf("empty room = %+v, %v", occ, err)
	}
	winner := r.insert(t, r.room, r.landlord, &r.tenant, domain.StatusPending, 1)
	rival := r.insert(t, r.room, r.landlord, &r.tenant2, domain.StatusPending, 2)
	if open, _ := r.contracts.RegistrationOpen(ctx, r.room, r.tenant); !open {
		t.Error("a waiting registration is open")
	}
	if open, _ := r.contracts.RegistrationOpen(ctx, r.room, r.other); open {
		t.Error("nobody else has one")
	}
	if n, _ := r.contracts.CountPending(ctx, r.tenant); n != 1 {
		t.Errorf("pending = %d", n)
	}
	rejected, err := r.contracts.RejectOtherPending(ctx, r.room, winner)
	if err != nil || !slices.Equal(rejected, []uuid.UUID{rival}) {
		t.Fatalf("rejected = %v, %v", rejected, err)
	}
	if d, _ := r.contracts.ForUpdate(ctx, rival); d.Status != domain.StatusRejected {
		t.Errorf("rival = %s", d.Status)
	}
	if d, _ := r.contracts.ForUpdate(ctx, winner); d.Status != domain.StatusPending {
		t.Errorf("winner = %s", d.Status)
	}
	if rooms, _ := r.contracts.RunningTenantRooms(ctx, []uuid.UUID{r.room}); len(rooms) != 0 {
		t.Errorf("a waiting registration does not lock the room: %v", rooms)
	}
	d, _ := r.contracts.ForUpdate(ctx, winner)
	d.Status = domain.StatusActive
	if err := r.contracts.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	if rooms, _ := r.contracts.RunningTenantRooms(ctx, []uuid.UUID{r.room, uuid.New()}); !slices.Equal(rooms, []uuid.UUID{r.room}) {
		t.Errorf("running rooms = %v", rooms)
	}
	if n, _ := r.contracts.CountRunning(ctx, r.landlord); n != 1 {
		t.Errorf("running = %d", n)
	}
	if n, _ := r.contracts.CountOfTenant(ctx, r.landlord, r.tenant); n != 1 {
		t.Errorf("tenant contracts = %d", n)
	}
}

func TestBlankContractsClose(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	p2 := r.newRoom(t, r.landlord, "P102")
	blank := r.insert(t, r.room, r.landlord, nil, domain.StatusActive, 1)
	held := r.insert(t, p2, r.landlord, &r.tenant, domain.StatusActive, 2)
	// A blank contract with an end date in the past keeps it.
	if _, err := r.pool.Exec(ctx, `UPDATE contracts SET ends_on = '2026-11-01' WHERE id = $1`, blank); err != nil {
		t.Fatal(err)
	}
	closed, err := r.contracts.CloseBlank(ctx, []uuid.UUID{r.room, p2}, day("2026-11-23"))
	if err != nil || len(closed) != 1 || closed[0].ContractID != blank || closed[0].RoomID != r.room || closed[0].LandlordID != r.landlord {
		t.Fatalf("closed = %+v, %v", closed, err)
	}
	if d, _ := r.contracts.ForUpdate(ctx, blank); d.Status != domain.StatusEnded || d.EndsOn.Format(domain.DateLayout) != "2026-11-01" {
		t.Errorf("blank = %+v", d)
	}
	if d, _ := r.contracts.ForUpdate(ctx, held); d.Status != domain.StatusActive {
		t.Error("a contract a tenant holds is never closed here")
	}
	// Without an end date it closes today.
	again := r.insert(t, r.room, r.landlord, nil, domain.StatusActive, 3)
	if closed, _ = r.contracts.CloseBlank(ctx, []uuid.UUID{r.room}, day("2026-11-23")); len(closed) != 1 {
		t.Fatalf("closed = %v", closed)
	}
	if d, _ := r.contracts.ForUpdate(ctx, again); d.EndsOn.Format(domain.DateLayout) != "2026-11-23" {
		t.Errorf("ends = %v", d.EndsOn)
	}
}

func TestOrderTopOrderAndSetOrder(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	if top, err := r.contracts.TopOrder(ctx, r.landlord); err != nil || top != -1 {
		t.Fatalf("empty top = %d, %v", top, err)
	}
	var ids []uuid.UUID
	for i, name := range []string{"P101x", "P102", "P103"} {
		room := r.room
		if i > 0 {
			room = r.newRoom(t, r.landlord, name)
		}
		ids = append(ids, r.insert(t, room, r.landlord, &r.tenant, domain.StatusPending, i+1))
	}
	if top, _ := r.contracts.TopOrder(ctx, r.landlord); top != 0 {
		t.Errorf("top = %d", top)
	}
	var current []domain.OrderedID
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if current, err = r.contracts.Order(ctx, r.landlord); err != nil {
			return err
		}
		return r.contracts.SetOrder(ctx, r.landlord, domain.Reorder(current, []uuid.UUID{ids[2], ids[0]}))
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := r.contracts.Order(ctx, r.landlord)
	got := []uuid.UUID{after[0].ID, after[1].ID, after[2].ID}
	if !slices.Equal(got, []uuid.UUID{ids[2], ids[1], ids[0]}) {
		t.Errorf("order = %v, want %v", got, []uuid.UUID{ids[2], ids[1], ids[0]})
	}
	// SetOrder never touches another landlord's contract.
	foreign := r.insert(t, r.newRoom(t, r.other, "Z9"), r.other, &r.tenant, domain.StatusPending, 7)
	if err := r.contracts.SetOrder(ctx, r.landlord, []domain.Position{{ID: foreign, Order: 1}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.contracts.ForUpdate(ctx, foreign); d.DisplayOrder != 7 {
		t.Errorf("foreign order = %d", d.DisplayOrder)
	}
}

func TestRoomReaderAndStatusCounts(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	facts, err := r.rooms.Lock(ctx, r.room)
	if err != nil || facts.Name != "P101" || facts.LandlordID != r.landlord || facts.PriceVND != 3_000_000 || facts.MinPeriod != 2 ||
		facts.PeriodUnit != domain.UnitMonth || !facts.Live || facts.Status != domain.RoomAvailable {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	if _, err := r.rooms.Lock(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown room: %v", err)
	}
	found, err := r.rooms.FindByName(ctx, r.landlord, "p101")
	if err != nil || found.ID != r.room {
		t.Errorf("find = %+v, %v", found, err)
	}
	if _, err := r.rooms.FindByName(ctx, r.other, "P101"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's room: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE rooms SET deleted_at = now() WHERE id = $1`, r.room); err != nil {
		t.Fatal(err)
	}
	if facts, _ = r.rooms.Lock(ctx, r.room); facts.Live {
		t.Error("a deleted room is not live")
	}

	r.insert(t, r.newRoom(t, r.landlord, "A"), r.landlord, &r.tenant, domain.StatusActive, 1)
	r.insert(t, r.newRoom(t, r.landlord, "B"), r.landlord, &r.tenant, domain.StatusEnded, 2)
	r.insert(t, r.newRoom(t, r.landlord, "C"), r.landlord, &r.tenant, domain.StatusEnded, 3)
	counts, err := r.contracts.StatusCounts(ctx, r.tenant)
	if err != nil || counts[domain.StatusActive] != 1 || counts[domain.StatusEnded] != 2 || counts[domain.StatusPending] != 0 {
		t.Errorf("counts = %v, %v", counts, err)
	}
}

func TestRentalExtrasFollowTheInheritance(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	var buildingID uuid.UUID
	if err := r.pool.QueryRow(ctx, `INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, 'B', 'x', 1) RETURNING id`, r.landlord).Scan(&buildingID); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := r.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Dien', 'electricity', 3500, 'per_kwh', 1), ($1, 'Rac', 'service', 20000, 'per_period', 2)`, r.room)
	exec(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order, deleted_at) VALUES ($1, 'Old', 'other', 1, 'one_time', 3, now())`, r.room)
	exec(`INSERT INTO media (room_id, file_key, thumb_key, content_type, size_bytes, width, height, display_order) VALUES ($1, 'r/1.webp', 'r/1_t.webp', 'image/webp', 10, 100, 80, 1)`, r.room)
	exec(`INSERT INTO media (building_id, file_key, thumb_key, content_type, size_bytes, width, height, display_order) VALUES ($1, 'b/1.webp', 'b/1_t.webp', 'image/webp', 10, 100, 80, 1)`, buildingID)
	exec(`UPDATE rooms SET building_id = $1 WHERE id = $2`, buildingID, r.room)

	charges, pictures, err := r.contracts.RentalExtras(ctx, r.room)
	if err != nil || len(charges) != 2 || charges[0].Name != "Dien" || charges[0].AmountVND != 3500 || len(pictures) != 1 || pictures[0].FileKey != "r/1.webp" || pictures[0].Width != 100 {
		t.Fatalf("own pictures = %+v %+v, %v", charges, pictures, err)
	}
	exec(`UPDATE rooms SET inherits_media = true WHERE id = $1`, r.room)
	if _, pictures, _ = r.contracts.RentalExtras(ctx, r.room); len(pictures) != 1 || pictures[0].FileKey != "b/1.webp" {
		t.Errorf("a room that follows its building shows the building's pictures: %+v", pictures)
	}
}

func TestLimitsAndLandlordLock(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	r.insert(t, r.room, r.landlord, &r.tenant, domain.StatusActive, 1)
	r.insert(t, r.newRoom(t, r.landlord, "P2"), r.landlord, nil, domain.StatusActive, 2) // blank: not counted
	if n, _ := r.contracts.CountRunning(ctx, r.landlord); n != 1 {
		t.Errorf("a blank contract is not a tenant: %d", n)
	}
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error { return r.contracts.LockLandlord(ctx, r.landlord) })
	if err != nil {
		t.Errorf("lock: %v", err)
	}
}

func TestRoomFactsTellWhetherAnApprovedListingExists(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	listed := func(room uuid.UUID) bool {
		t.Helper()
		facts, err := r.rooms.Lock(ctx, room)
		if err != nil {
			t.Fatal(err)
		}
		return facts.Listed
	}
	if listed(r.room) {
		t.Fatal("a room with no listing is not listed")
	}
	// A room has at most one open listing (the database enforces it): the others are not open.
	for _, status := range []string{"draft", "rejected", "unpublished", "pending"} {
		if _, err := r.pool.Exec(ctx, `INSERT INTO listings (room_id, landlord_id, title, status) VALUES ($1, $2, 'T', $3)`, r.room, r.landlord, status); err != nil {
			t.Fatal(err)
		}
	}
	if listed(r.room) {
		t.Error("only an approved listing counts")
	}
	if _, err := r.pool.Exec(ctx, `UPDATE listings SET status = 'approved' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if !listed(r.room) {
		t.Error("an approved listing lists the room")
	}
	found, err := r.rooms.FindByName(ctx, r.landlord, "p101")
	if err != nil || !found.Listed {
		t.Errorf("FindByName reads it too: %+v %v", found, err)
	}
}
