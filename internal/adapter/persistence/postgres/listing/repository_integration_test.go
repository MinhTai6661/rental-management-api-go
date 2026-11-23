//go:build integration

package listingpg

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

type repos struct {
	pool     *pgxpool.Pool
	listings *ListingRepository
	rooms    *RoomReader
	offers   *OfferRepository
	tx       *postgres.TxManager
	landlord uuid.UUID
	other    uuid.UUID // another landlord
	tenant   uuid.UUID
	admin    uuid.UUID
}

func newRepos(t *testing.T) *repos {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi'), (2, 'Da Nang', 'city', 'da-nang')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh'), (5, 1, 'Cau Giay', 'ward', 'cau-giay'), (6, 2, 'Hai Chau', 'ward', 'hai-chau')`,
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('landlord', 3, false), ('tenant', 5, false)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := &repos{
		pool: pool, listings: NewListingRepository(pool), rooms: NewRoomReader(pool), offers: NewOfferRepository(pool),
		tx: postgres.NewTxManager(pool),
	}
	r.landlord, r.other = r.user(t, "l@example.vn", "Chu Hoa", "landlord"), r.user(t, "o@example.vn", "Chu Lan", "landlord")
	r.tenant, r.admin = r.user(t, "t@example.vn", "Tenant", "tenant"), r.user(t, "a@example.vn", "Admin")
	return r
}

func (r *repos) user(t *testing.T, email, name string, roles ...string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.pool.QueryRow(context.Background(), `INSERT INTO users (email, full_name, phone_number) VALUES ($1, $2, '0901') RETURNING id`, email, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		r.exec(t, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, role)
	}
	return id
}

func (r *repos) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// room inserts an available room (ward 4, 20 m2) of the landlord; the options change it afterwards.
func (r *repos) room(t *testing.T, landlord uuid.UUID, name string, price int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, area_square_meters, display_order, cover_key, cover_thumb_key)
		VALUES ($1, $2, 4, '12 Le Loi', $3, 20, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), 'k/c.webp', 'k/c_t.webp')
		RETURNING id`, landlord, name, price).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *repos) building(t *testing.T, landlord uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), `
		INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, $2, '1 Pho', 1) RETURNING id`, landlord, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// list inserts a listing in the status for the room and returns its id.
func (r *repos) list(t *testing.T, room, landlord uuid.UUID, title string, status domain.Status) uuid.UUID {
	t.Helper()
	now := time.Now()
	l := domain.Listing{RoomID: room, LandlordID: landlord, Title: title, Status: status}
	if status != domain.StatusDraft {
		l.SubmittedAt = &now
	}
	id, err := r.listings.Insert(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *repos) offered(t *testing.T, q domain.OfferQuery) []string {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 50
	}
	items, total, err := r.offers.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(items))
	for i, o := range items {
		names[i] = o.Name
	}
	if total != len(names) && q.Offset == 0 && q.Limit >= total {
		t.Errorf("total %d for %v", total, names)
	}
	return names
}

func TestListingRoundTripAndScope(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	room := r.room(t, r.landlord, "P101", 3_000_000)
	desc := "Thoáng mát"
	id, err := r.listings.Insert(ctx, domain.Listing{RoomID: room, LandlordID: r.landlord, Title: "Gần chợ", Description: &desc, Status: domain.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	mine := &r.landlord
	d, err := r.listings.Get(ctx, mine, id)
	if err != nil || d.Title != "Gần chợ" || *d.Description != desc || d.Status != domain.StatusDraft || d.Room.Name != "P101" ||
		d.Room.PriceVND != 3_000_000 || d.Room.Ward == nil || d.Room.Ward.ProvinceName != "Ha Noi" || d.Room.Cover == nil ||
		d.Landlord.FullName != "Chu Hoa" || d.Landlord.Email == nil || d.Reviewer != nil || !d.Room.Live {
		t.Fatalf("get: %+v %v", d, err)
	}
	if _, err := r.listings.Get(ctx, &r.other, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's scope: %v", err)
	}
	if _, err := r.listings.Get(ctx, nil, id); err != nil {
		t.Errorf("the admins' scope: %v", err)
	}
	if _, err := r.listings.Get(ctx, nil, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}

	// Save writes every field that can change, and the reviewer shows up.
	l, err := r.listings.ForUpdate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	l.Title, l.Description = "Mới", nil
	if err := l.Submit(now); err != nil {
		t.Fatal(err)
	}
	if err := l.Approve(r.admin, now, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.listings.Save(ctx, l); err != nil {
		t.Fatal(err)
	}
	d, _ = r.listings.Get(ctx, nil, id)
	if d.Title != "Mới" || d.Description != nil || d.Status != domain.StatusApproved || d.PublishedAt == nil || d.SubmittedAt == nil ||
		d.Reviewer == nil || d.Reviewer.FullName != "Admin" || d.ReviewedAt == nil {
		t.Errorf("after save: %+v", d)
	}

	got, err := r.listings.ByIDs(ctx, []uuid.UUID{id, uuid.New()})
	if err != nil || len(got) != 1 || got[0].ID != id {
		t.Errorf("by ids: %+v %v", got, err)
	}
	if err := r.listings.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.listings.ForUpdate(ctx, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}

func TestOneOpenListingPerRoomIsEnforcedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	room := r.room(t, r.landlord, "P101", 1)
	first := r.list(t, room, r.landlord, "A", domain.StatusPending)

	_, err := r.listings.Insert(ctx, domain.Listing{RoomID: room, LandlordID: r.landlord, Title: "B", Status: domain.StatusApproved})
	if !shared.IsUnique(err) {
		t.Fatalf("a second open listing: %v", err)
	}
	// Drafts, refused and taken-down listings do not hold the room, however many there are.
	for _, s := range []domain.Status{domain.StatusDraft, domain.StatusDraft, domain.StatusRejected, domain.StatusUnpublished} {
		r.list(t, room, r.landlord, "x", s)
	}
	open, err := r.listings.OpenOfRoom(ctx, room, nil)
	if err != nil || !open {
		t.Errorf("open: %v %v", open, err)
	}
	if open, _ := r.listings.OpenOfRoom(ctx, room, &first); open {
		t.Error("the listing itself is not 'another'")
	}
	rooms, err := r.listings.OpenRooms(ctx, []uuid.UUID{room, uuid.New()})
	if err != nil || len(rooms) != 1 || rooms[0] != room {
		t.Errorf("open rooms: %v %v", rooms, err)
	}

	// Sending a draft for a room that has an open listing is refused by the index too.
	draft := r.list(t, room, r.landlord, "D", domain.StatusDraft)
	l, _ := r.listings.ForUpdate(ctx, draft)
	_ = l.Submit(time.Now())
	if err := r.listings.Save(ctx, l); !shared.IsUnique(err) {
		t.Errorf("saving a second pending: %v", err)
	}

	// The open ones can be locked and counted.
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		locked, err := r.listings.LockOpenOfRooms(ctx, []uuid.UUID{room})
		if err != nil || len(locked) != 1 || locked[0].ID != first {
			t.Errorf("locked: %+v %v", locked, err)
		}
		if n, err := r.listings.CountOpen(ctx, r.landlord); err != nil || n != 1 {
			t.Errorf("count open: %d %v", n, err)
		}
		return r.listings.LockLandlord(ctx, r.landlord)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSearchFiltersSortsAndCountsPerScope(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	a := r.room(t, r.landlord, "P101", 1_000_000)
	b := r.room(t, r.landlord, "P102", 5_000_000)
	c := r.room(t, r.landlord, "P103", 3_000_000)
	x := r.room(t, r.other, "X1", 2_000_000)
	r.exec(t, `UPDATE rooms SET ward_code = 5, area_square_meters = 40 WHERE id = $1`, b)
	r.exec(t, `UPDATE rooms SET ward_code = 6, area_square_meters = NULL WHERE id = $1`, c)
	la := r.list(t, a, r.landlord, "Gần chợ", domain.StatusApproved)
	lb := r.list(t, b, r.landlord, "Có ban công", domain.StatusPending)
	lc := r.list(t, c, r.landlord, "Phòng 50%", domain.StatusDraft)
	lx := r.list(t, x, r.other, "Of other", domain.StatusPending)

	ids := func(q domain.Query) []uuid.UUID {
		t.Helper()
		q.Limit = 50
		items, total, err := r.listings.Search(ctx, q)
		if err != nil || total != len(items) {
			t.Fatalf("search %+v: %d of %d, %v", q, len(items), total, err)
		}
		out := make([]uuid.UUID, len(items))
		for i, d := range items {
			out[i] = d.ID
		}
		return out
	}
	same := func(name string, got []uuid.UUID, want ...uuid.UUID) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	mine := &r.landlord
	same("a landlord sees only theirs, newest first", ids(domain.Query{LandlordID: mine}), lc, lb, la)
	same("the admins see everybody's", ids(domain.Query{}), lx, lc, lb, la)
	same("no drafts for the admins", ids(domain.Query{ExcludeDrafts: true}), lx, lb, la)
	pending := domain.StatusPending
	same("a status", ids(domain.Query{Status: &pending}), lx, lb)
	same("oldest first", ids(domain.Query{LandlordID: mine, Sort: "created_at"}), la, lb, lc)
	same("title", ids(domain.Query{LandlordID: mine, Sort: "title"}), lb, la, lc)
	same("rent high to low", ids(domain.Query{LandlordID: mine, Sort: "-price"}), lb, lc, la)
	same("rent low to high", ids(domain.Query{LandlordID: mine, Sort: "price"}), la, lc, lb)
	same("area, unknown last", ids(domain.Query{LandlordID: mine, Sort: "area"}), la, lb, lc)
	same("by title text", ids(domain.Query{LandlordID: mine, SearchLike: ptr(`ban c`)}), lb)
	same("by room name", ids(domain.Query{LandlordID: mine, SearchLike: ptr(`p103`)}), lc)
	same("an escaped percent matches itself only", ids(domain.Query{LandlordID: mine, SearchLike: ptr(`50\%`)}), lc)
	same("a ward", ids(domain.Query{LandlordID: mine, WardCode: ptr(int32(5))}), lb)
	same("a province", ids(domain.Query{LandlordID: mine, ProvinceCode: ptr(int32(2))}), lc)
	same("a ward wins over the province", ids(domain.Query{LandlordID: mine, WardCode: ptr(int32(5)), ProvinceCode: ptr(int32(2))}), lb)
	same("rent range", ids(domain.Query{LandlordID: mine, PriceFrom: ptr(int64(2_000_000)), PriceTo: ptr(int64(4_000_000))}), lc)
	same("area range", ids(domain.Query{LandlordID: mine, AreaFrom: ptr(30.0)}), lb)

	counts, err := r.listings.Counts(ctx, mine, false)
	if err != nil || counts[domain.StatusApproved] != 1 || counts[domain.StatusPending] != 1 || counts[domain.StatusDraft] != 1 {
		t.Errorf("own counts: %v %v", counts, err)
	}
	counts, _ = r.listings.Counts(ctx, nil, true)
	if _, has := counts[domain.StatusDraft]; has || counts[domain.StatusPending] != 2 {
		t.Errorf("the admins' counts leave drafts out: %v", counts)
	}

	// A page reports the total of the whole match.
	items, total, err := r.listings.Search(ctx, domain.Query{LandlordID: mine, Limit: 2, Offset: 2})
	if err != nil || total != 3 || len(items) != 1 {
		t.Errorf("page: %d of %d %v", len(items), total, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestOffersAreVacantRoomsWithAnApprovedListingOfALandlord(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	room := r.room(t, r.landlord, "P101", 1_000_000)
	r.list(t, room, r.landlord, "Offered", domain.StatusApproved)
	if got := r.offered(t, domain.OfferQuery{}); !slices.Equal(got, []string{"P101"}) {
		t.Fatalf("the base case is offered: %v", got)
	}

	cases := map[string]struct{ break_, mend string }{
		"rented":      {`UPDATE rooms SET status = 'rented'`, `UPDATE rooms SET status = 'available'`},
		"maintenance": {`UPDATE rooms SET status = 'maintenance'`, `UPDATE rooms SET status = 'available'`},
		"deleted":     {`UPDATE rooms SET deleted_at = now()`, `UPDATE rooms SET deleted_at = NULL`},
		"landlord role revoked": {
			`DELETE FROM user_roles WHERE role_code = 'landlord' AND user_id = (SELECT landlord_id FROM rooms)`,
			`INSERT INTO user_roles (user_id, role_code) SELECT landlord_id, 'landlord' FROM rooms`,
		},
		"listing pending":     {`UPDATE listings SET status = 'pending'`, `UPDATE listings SET status = 'approved'`},
		"listing draft":       {`UPDATE listings SET status = 'draft'`, `UPDATE listings SET status = 'approved'`},
		"listing rejected":    {`UPDATE listings SET status = 'rejected'`, `UPDATE listings SET status = 'approved'`},
		"listing unpublished": {`UPDATE listings SET status = 'unpublished'`, `UPDATE listings SET status = 'approved'`},
	}
	for name, c := range cases {
		r.exec(t, c.break_)
		if got := r.offered(t, domain.OfferQuery{}); len(got) != 0 {
			t.Errorf("%s: the room must not be offered, got %v", name, got)
		}
		if _, err := r.offers.Get(ctx, room); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: Get must not find it: %v", name, err)
		}
		r.exec(t, c.mend)
		if got := r.offered(t, domain.OfferQuery{}); len(got) != 1 {
			t.Fatalf("%s: mended, the room is offered again, got %v", name, got)
		}
	}

	// A contract a tenant holds takes the room off; a waiting registration and a blank contract do not.
	insert := func(tenant *uuid.UUID, status string) {
		t.Helper()
		r.exec(t, `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
			VALUES ($1, $2, $3, $4, 1, current_date, now(), 1)`, room, r.landlord, tenant, status)
	}
	insert(&r.tenant, "pending")
	insert(nil, "active")
	if got := r.offered(t, domain.OfferQuery{}); len(got) != 1 {
		t.Errorf("a waiting registration and a blank contract leave the room offered: %v", got)
	}
	r.exec(t, `UPDATE contracts SET status = 'active' WHERE tenant_id IS NOT NULL`)
	if got := r.offered(t, domain.OfferQuery{}); len(got) != 0 {
		t.Errorf("a running contract with a tenant takes it off: %v", got)
	}
	r.exec(t, `UPDATE contracts SET status = 'ended' WHERE tenant_id IS NOT NULL`)
	if got := r.offered(t, domain.OfferQuery{}); len(got) != 1 {
		t.Errorf("an ended contract does not: %v", got)
	}
	r.exec(t, `UPDATE contracts SET status = 'active', deleted_at = now() WHERE tenant_id IS NOT NULL`)
	if got := r.offered(t, domain.OfferQuery{}); len(got) != 1 {
		t.Errorf("a deleted contract does not: %v", got)
	}
}

func TestOfferSearchFiltersHouseNamePlaceRentAndArea(t *testing.T) {
	r := newRepos(t)
	saoMai := r.building(t, r.landlord, "Nhà trọ Sao Mai")
	a := r.room(t, r.landlord, "P101", 1_000_000)
	b := r.room(t, r.landlord, "P102", 4_000_000)
	loose := r.room(t, r.other, "A1", 2_000_000) // outside any building: goes by the landlord's name
	far := r.room(t, r.other, "A2", 6_000_000)
	r.exec(t, `UPDATE rooms SET building_id = $1 WHERE id IN ($2, $3)`, saoMai, a, b)
	r.exec(t, `UPDATE rooms SET ward_code = 6, area_square_meters = 55 WHERE id = $1`, far)
	r.exec(t, `UPDATE rooms SET area_square_meters = 33 WHERE id = $1`, b)
	for i, id := range []uuid.UUID{a, b, loose, far} {
		owner := r.landlord
		if i >= 2 {
			owner = r.other
		}
		r.list(t, id, owner, "L", domain.StatusApproved)
	}
	names := func(q domain.OfferQuery) []string {
		got := r.offered(t, q)
		slices.Sort(got)
		return got
	}
	check := func(name string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	check("the building's name", names(domain.OfferQuery{HouseLike: ptr("sao mai")}), "P101", "P102")
	check("a part of it", names(domain.OfferQuery{HouseLike: ptr("mai")}), "P101", "P102")
	check("a room outside a building goes by its landlord", names(domain.OfferQuery{HouseLike: ptr("chu lan")}), "A1", "A2")
	check("the landlord of a room in a building does not match", names(domain.OfferQuery{HouseLike: ptr("chu hoa")}))
	check("the room's own name is not searched", names(domain.OfferQuery{HouseLike: ptr("p101")}))
	check("a landlord", names(domain.OfferQuery{LandlordID: &r.other}), "A1", "A2")
	check("a ward", names(domain.OfferQuery{WardCode: ptr(int32(6))}), "A2")
	check("a province", names(domain.OfferQuery{ProvinceCode: ptr(int32(1))}), "A1", "P101", "P102")
	check("rent range", names(domain.OfferQuery{PriceFrom: ptr(int64(1_500_000)), PriceTo: ptr(int64(4_000_000))}), "A1", "P102")
	check("area range", names(domain.OfferQuery{AreaFrom: ptr(30.0), AreaTo: ptr(40.0)}), "P102")

	// Sorts and pages.
	check("price high to low", r.offered(t, domain.OfferQuery{Sort: "-price"}), "A2", "P102", "A1", "P101")
	check("area small to large (a tie goes to the newer room)", r.offered(t, domain.OfferQuery{Sort: "area"}), "A1", "P101", "P102", "A2")
	check("default is newest first", r.offered(t, domain.OfferQuery{}), "A2", "A1", "P102", "P101")
	items, total, err := r.offers.Search(context.Background(), domain.OfferQuery{Limit: 2, Offset: 1})
	if err != nil || total != 4 || len(items) != 2 || items[0].Name != "A1" {
		t.Errorf("page: %d of %d %v %v", len(items), total, err, items)
	}
	if items[1].HouseName != "Nhà trọ Sao Mai" || items[0].HouseName != "Chu Lan" {
		t.Errorf("house names: %q, %q", items[0].HouseName, items[1].HouseName)
	}
}

func TestOfferSearchOrdersByTheGivenPlanPriorities(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	old := r.room(t, r.landlord, "Old", 1)
	mid := r.room(t, r.other, "Mid", 1)
	young := r.room(t, r.landlord, "Young", 1)
	r.list(t, old, r.landlord, "L", domain.StatusApproved)
	r.list(t, mid, r.other, "L", domain.StatusApproved)
	r.list(t, young, r.landlord, "L", domain.StatusApproved)

	if got := r.offered(t, domain.OfferQuery{}); !slices.Equal(got, []string{"Young", "Mid", "Old"}) {
		t.Fatalf("no priorities: %v", got)
	}
	boost := []domain.Priority{{LandlordID: r.other, Value: 10}}
	if got := r.offered(t, domain.OfferQuery{Priorities: boost}); !slices.Equal(got, []string{"Mid", "Young", "Old"}) {
		t.Errorf("one boosted landlord leads, the rest newest first: %v", got)
	}
	two := []domain.Priority{{LandlordID: r.other, Value: 10}, {LandlordID: r.landlord, Value: 100}}
	if got := r.offered(t, domain.OfferQuery{Priorities: two}); !slices.Equal(got, []string{"Young", "Old", "Mid"}) {
		t.Errorf("greater priority first: %v", got)
	}
	// An explicit sort ignores them.
	if got := r.offered(t, domain.OfferQuery{Priorities: two, Sort: "created_at"}); !slices.Equal(got, []string{"Old", "Mid", "Young"}) {
		t.Errorf("sorted: %v", got)
	}
	landlords, err := r.offers.Landlords(ctx, domain.OfferQuery{})
	if err != nil || len(landlords) != 2 {
		t.Errorf("distinct landlords: %v %v", landlords, err)
	}
	only, _ := r.offers.Landlords(ctx, domain.OfferQuery{PriceFrom: ptr(int64(2))})
	if len(only) != 0 {
		t.Errorf("filters apply: %v", only)
	}
}

func TestOfferDetailAndLandlordCard(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	b := r.building(t, r.landlord, "Nhà trọ Hoa")
	room := r.room(t, r.landlord, "P101", 2_500_000)
	other := r.room(t, r.landlord, "P102", 2_500_000)
	r.exec(t, `UPDATE rooms SET building_id = $1, description = 'Quiet', inherits_media = true WHERE id = $2`, b, room)
	r.list(t, room, r.landlord, "Bright room", domain.StatusApproved)
	r.list(t, other, r.landlord, "Not yet", domain.StatusPending)
	r.exec(t, `INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Điện', 'electricity', 3500, 'per_kwh', 1)`, room)
	r.exec(t, `INSERT INTO media (building_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, display_order)
		VALUES ($1, 'cover', 'b/1.webp', 'b/1_t.webp', 'image/webp', 100, 800, 600, 1)`, b)

	o, err := r.offers.Get(ctx, room)
	if err != nil || o.Name != "P101" || o.HouseName != "Nhà trọ Hoa" || o.Listing.Title != "Bright room" || o.Landlord.FullName != "Chu Hoa" ||
		o.AddressDetail != "12 Le Loi" || o.Ward == nil || o.Ward.Name != "Ba Dinh" || *o.Description != "Quiet" {
		t.Fatalf("offer: %+v %v", o, err)
	}
	if _, err := r.offers.Get(ctx, other); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a room with a waiting listing is not offered: %v", err)
	}
	charges, pictures, err := r.rooms.Extras(ctx, room)
	if err != nil || len(charges) != 1 || charges[0].Name != "Điện" || charges[0].AmountVND != 3500 || len(pictures) != 1 || pictures[0].Key != "b/1.webp" {
		t.Errorf("extras (the building's pictures while the room follows them): %+v %+v %v", charges, pictures, err)
	}

	card, err := r.offers.Landlord(ctx, r.landlord)
	if err != nil || card.FullName != "Chu Hoa" || card.VacantRooms != 1 || !slices.Equal(card.Houses, []string{"Nhà trọ Hoa"}) {
		t.Errorf("card: %+v %v", card, err)
	}
	empty, err := r.offers.Landlord(ctx, r.other)
	if err != nil || empty.VacantRooms != 0 || empty.Houses == nil || len(empty.Houses) != 0 {
		t.Errorf("a landlord with nothing on offer: %+v %v", empty, err)
	}
	for name, id := range map[string]uuid.UUID{"a tenant": r.tenant, "an admin": r.admin, "nobody": uuid.New()} {
		if _, err := r.offers.Landlord(ctx, id); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s is not a landlord: %v", name, err)
		}
	}
	r.exec(t, `UPDATE users SET status = 'inactive' WHERE id = $1`, r.landlord)
	if _, err := r.offers.Landlord(ctx, r.landlord); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a disabled landlord: %v", err)
	}
}

func TestRoomReaderLocksAndListsSeedRooms(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	room := r.room(t, r.landlord, "P101", 1)
	listed := r.room(t, r.landlord, "P102", 1)
	rented := r.room(t, r.landlord, "P103", 1)
	r.exec(t, `UPDATE rooms SET status = 'rented' WHERE id = $1`, rented)
	r.list(t, listed, r.landlord, "L", domain.StatusRejected)

	facts, err := r.rooms.Lock(ctx, room)
	if err != nil || facts.Name != "P101" || facts.LandlordID != r.landlord || !facts.Live {
		t.Fatalf("lock: %+v %v", facts, err)
	}
	r.exec(t, `UPDATE rooms SET deleted_at = now() WHERE id = $1`, room)
	if facts, _ = r.rooms.Lock(ctx, room); facts.Live {
		t.Error("a deleted room is not live")
	}
	if _, err := r.rooms.Lock(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown room: %v", err)
	}
	got, err := r.rooms.WithoutListing(ctx, r.landlord)
	if err != nil || len(got) != 0 {
		t.Errorf("without listing (the others are deleted, rented or listed): %+v %v", got, err)
	}
	free := r.room(t, r.landlord, "P104", 1)
	if got, _ = r.rooms.WithoutListing(ctx, r.landlord); len(got) != 1 || got[0].ID != free {
		t.Errorf("without listing: %+v", got)
	}
}
