//go:build integration

package propertypg

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// propertyRepos is the four property repositories on one fresh database with a landlord and a ward.
type propertyRepos struct {
	pool      *pgxpool.Pool
	buildings *BuildingRepository
	rooms     *RoomRepository
	charges   *ChargeRepository
	media     *MediaRepository
	tx        *postgres.TxManager
	landlord  uuid.UUID
	other     uuid.UUID
	ward      int32
}

func newPropertyRepos(t *testing.T) *propertyRepos {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	if _, err := pool.Exec(ctx, `INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`); err != nil {
		t.Fatal(err)
	}
	user := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return &propertyRepos{
		pool: pool, buildings: NewBuildingRepository(pool), rooms: NewRoomRepository(pool), charges: NewChargeRepository(pool),
		media: NewMediaRepository(pool), tx: postgres.NewTxManager(pool), landlord: user("a@example.vn"), other: user("b@example.vn"), ward: 4,
	}
}

func (p *propertyRepos) building(t *testing.T, name string) uuid.UUID {
	t.Helper()
	price, area, period, unit := int64(2_000_000), 20.0, int32(3), "month"
	id, err := p.buildings.Insert(context.Background(), p.landlord, domain.BuildingData{Name: name, BuildingDefaults: domain.BuildingDefaults{
		WardCode: &p.ward, AddressDetail: "12 Le Loi", PriceVND: &price, Area: &area, MinPeriod: &period, PeriodUnit: &unit,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (p *propertyRepos) room(t *testing.T, building *uuid.UUID, name string, inherits ...domain.Group) uuid.UUID {
	t.Helper()
	id, err := p.rooms.Insert(context.Background(), p.landlord, domain.RoomData{
		BuildingID: building, Name: name, Status: domain.StatusAvailable, Inherits: inherits,
		RoomValues: domain.RoomValues{AddressDetail: "12 Le Loi", PriceVND: 1_000_000, MinPeriod: 1, PeriodUnit: "month"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBuildingRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	a, b := p.building(t, "Nha A"), p.building(t, "Nha B")

	got, err := p.buildings.ForUpdate(ctx, p.landlord, a)
	if err != nil || got.Name != "Nha A" || *got.PriceVND != 2_000_000 || *got.MinPeriod != 3 || *got.PeriodUnit != "month" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := p.buildings.ForUpdate(ctx, p.other, a); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord's building is not found: %v", err)
	}
	summary, err := p.buildings.Get(ctx, p.landlord, a)
	if err != nil || summary.Ward == nil || summary.Ward.Name != "Ba Dinh" || summary.Ward.Province.Name != "Ha Noi" || *summary.MinPeriod != 3 {
		t.Fatalf("%+v %v", summary, err)
	}

	like := "nha%"
	rows, total, err := p.buildings.Search(ctx, p.landlord, domain.BuildingQuery{NameLike: &like, Sort: "-name", Limit: 10})
	if err != nil || total != 2 || rows[0].ID != b {
		t.Fatalf("%+v %d %v", rows, total, err)
	}
	if rows, total, _ = p.buildings.Search(ctx, p.landlord, domain.BuildingQuery{Limit: 1, Offset: 1}); len(rows) != 1 || total != 2 {
		t.Errorf("paging keeps the total: %d %d", len(rows), total)
	}

	if taken, err := p.buildings.NameTaken(ctx, p.landlord, "NHA a", nil); err != nil || !taken {
		t.Errorf("a name is taken in any case: %v %v", taken, err)
	}
	if taken, _ := p.buildings.NameTaken(ctx, p.landlord, "NHA a", &a); taken {
		t.Error("a building does not clash with itself")
	}
	_, err = p.buildings.Insert(ctx, p.landlord, domain.BuildingData{Name: "nha a", BuildingDefaults: domain.BuildingDefaults{AddressDetail: "x"}})
	if !shared.IsUnique(err) {
		t.Errorf("the unique index refuses a duplicate name: %v", err)
	}
}

func TestBuildingRepositoryOrderAndDelete(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	a, b, c := p.building(t, "A"), p.building(t, "B"), p.building(t, "C")
	order, err := p.buildings.Order(ctx, p.landlord)
	if err != nil || len(order) != 3 || order[0].ID != a || order[0].Order >= order[1].Order {
		t.Fatalf("%+v %v", order, err)
	}
	if err := p.buildings.SetOrder(ctx, p.landlord, []domain.Position{{ID: c, Order: 1}, {ID: a, Order: 3}}); err != nil {
		t.Fatal(err)
	}
	order, _ = p.buildings.Order(ctx, p.landlord)
	if order[0].ID != c || order[2].ID != a {
		t.Errorf("%+v", order)
	}

	p.room(t, &b, "P1")
	rows, err := p.buildings.ToDelete(ctx, p.landlord, []uuid.UUID{a, b, uuid.Must(uuid.NewV7())})
	if err != nil || len(rows) != 2 {
		t.Fatalf("an id that is not theirs is missing: %+v %v", rows, err)
	}
	counts := map[uuid.UUID]int{}
	for _, r := range rows {
		counts[r.ID] = r.RoomsCount
	}
	if counts[a] != 0 || counts[b] != 1 {
		t.Errorf("%v", counts)
	}
	if err := p.buildings.SoftDelete(ctx, p.landlord, []uuid.UUID{a}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.buildings.ForUpdate(ctx, p.landlord, a); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a deleted building is gone: %v", err)
	}
	if ever, err := p.buildings.AnyEver(ctx, p.landlord); err != nil || !ever {
		t.Errorf("deleted ones count for AnyEver: %v %v", ever, err)
	}
	if ever, _ := p.buildings.AnyEver(ctx, p.other); ever {
		t.Error("another landlord never had one")
	}
	if n, err := p.buildings.CountLive(ctx, p.landlord); err != nil || n != 2 {
		t.Errorf("%d %v", n, err)
	}
	if ok, _ := p.buildings.WardExists(ctx, p.ward); !ok {
		t.Error("ward exists")
	}
	if ok, _ := p.buildings.WardExists(ctx, 999); ok {
		t.Error("no such ward")
	}
	if code, err := p.buildings.FirstWardCode(ctx); err != nil || code != p.ward {
		t.Errorf("%d %v", code, err)
	}
}

func TestRoomRepositoryKeepsTheInheritanceInTheFixedOrder(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	b := p.building(t, "A")
	r := p.room(t, &b, "P1", domain.GroupMedia, domain.GroupPrice)

	got, err := p.rooms.ForUpdate(ctx, p.landlord, r)
	if err != nil || !slices.Equal(got.Inherits, []domain.Group{domain.GroupPrice, domain.GroupMedia}) || got.Status != domain.StatusAvailable {
		t.Fatalf("%+v %v", got, err)
	}
	summary, err := p.rooms.Get(ctx, p.landlord, r)
	if err != nil || summary.Building == nil || summary.Building.Name != "A" || !summary.Follows(domain.GroupMedia) {
		t.Fatalf("%+v %v", summary, err)
	}
	if _, err := p.rooms.Get(ctx, p.other, r); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("%v", err)
	}

	if taken, _ := p.rooms.NameTaken(ctx, p.landlord, &b, "p1", nil); !taken {
		t.Error("room names are unique per building, in any case")
	}
	if taken, _ := p.rooms.NameTaken(ctx, p.landlord, nil, "p1", nil); taken {
		t.Error("another building (or none) is another scope")
	}
	_, err = p.rooms.Insert(ctx, p.landlord, domain.RoomData{BuildingID: &b, Name: "P1", RoomValues: domain.RoomValues{AddressDetail: "x", MinPeriod: 1, PeriodUnit: "month"}, Status: domain.StatusAvailable})
	if !shared.IsUnique(err) {
		t.Errorf("%v", err)
	}
}

func TestRoomRepositoryRunStatusOrderAndCopy(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	b, other := p.building(t, "A"), p.building(t, "B")
	ids, err := p.rooms.InsertRun(ctx, p.landlord, domain.RoomRun{
		BuildingID: b, Names: []string{"P1", "P2", "P3"}, Status: domain.StatusAvailable, Inherits: []domain.Group{domain.GroupPrice},
		RoomValues: domain.RoomValues{WardCode: &p.ward, AddressDetail: "12 Le Loi", PriceVND: 2_000_000, MinPeriod: 3, PeriodUnit: "month"},
	})
	if err != nil || len(ids) != 3 {
		t.Fatalf("%v %v", ids, err)
	}
	rows, total, err := p.rooms.Search(ctx, p.landlord, domain.RoomQuery{BuildingID: &b, Limit: 10})
	if err != nil || total != 3 || rows[0].ID != ids[0] || rows[2].ID != ids[2] {
		t.Fatalf("the ids come back in the order of the names: %v %v", rows, err)
	}
	taken, err := p.rooms.NamesTakenInBuilding(ctx, b, []string{"p2", "p9"})
	if err != nil || !slices.Equal(taken, []string{"P2"}) {
		t.Errorf("%v %v", taken, err)
	}

	refs, err := p.rooms.ForUpdateMany(ctx, p.landlord, []uuid.UUID{ids[0], ids[1], uuid.Must(uuid.NewV7())})
	if err != nil || len(refs) != 2 {
		t.Fatalf("%v %v", refs, err)
	}
	if err := p.rooms.SetStatus(ctx, p.landlord, []uuid.UUID{ids[0]}, domain.StatusMaintenance); err != nil {
		t.Fatal(err)
	}
	if r, _ := p.rooms.ForUpdate(ctx, p.landlord, ids[0]); r.Status != domain.StatusMaintenance {
		t.Errorf("%v", r.Status)
	}

	order, _ := p.rooms.Order(ctx, p.landlord)
	if len(order) != 3 {
		t.Fatalf("%v", order)
	}
	if err := p.rooms.SetOrder(ctx, p.landlord, []domain.Position{{ID: ids[2], Order: 1}, {ID: ids[0], Order: 3}}); err != nil {
		t.Fatal(err)
	}
	if order, _ = p.rooms.Order(ctx, p.landlord); order[0].ID != ids[2] {
		t.Errorf("%v", order)
	}

	sources, err := p.rooms.CopySources(ctx, p.landlord, b)
	if err != nil || len(sources) != 3 {
		t.Fatalf("%v %v", sources, err)
	}
	copies := []domain.RoomCopyPair{{Source: ids[0], Dest: uuid.Must(uuid.NewV7())}, {Source: ids[1], Dest: uuid.Must(uuid.NewV7())}}
	if err := p.rooms.Copy(ctx, p.landlord, other, copies); err != nil {
		t.Fatal(err)
	}
	copied, total, _ := p.rooms.Search(ctx, p.landlord, domain.RoomQuery{BuildingID: &other, Limit: 10})
	if total != 2 || copied[0].ID != copies[0].Dest || copied[0].Name != "P1" || copied[0].Status != domain.StatusMaintenance || copied[1].Status != domain.StatusAvailable {
		t.Errorf("copies keep the name and the status (maintenance stays), in order: %+v", copied)
	}
	if n, _ := p.rooms.CountLive(ctx, p.landlord); n != 5 {
		t.Errorf("%d", n)
	}
	if err := p.rooms.SoftDelete(ctx, p.landlord, []uuid.UUID{ids[1]}); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.rooms.CountLive(ctx, p.landlord); n != 4 {
		t.Errorf("%d", n)
	}
}

func TestChargeRepositoryFollowDetachAndSync(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	b := p.building(t, "A")
	r := p.room(t, &b, "P1", domain.GroupCharges)
	note := "per month"
	err := p.charges.AddToBuilding(ctx, b, []domain.ChargeCreate{
		{Input: domain.ChargeInput{Name: "Wifi", Kind: "internet", AmountVND: 100, Unit: "per_period", Note: &note}, Order: 1},
		{Input: domain.ChargeInput{Name: "Water", Kind: "water", AmountVND: 20, Unit: "per_cubic_meter"}, Order: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	templates, err := p.charges.OfBuilding(ctx, b)
	if err != nil || len(templates) != 2 || templates[0].Name != "Wifi" || *templates[0].Note != "per month" || templates[1].Order != 2 {
		t.Fatalf("%+v %v", templates, err)
	}

	if err := p.charges.Follow(ctx, []uuid.UUID{r}, b); err != nil {
		t.Fatal(err)
	}
	copies, _ := p.charges.OfRoom(ctx, r)
	if len(copies) != 2 || !copies[0].FollowsBuilding() || *copies[0].TemplateID != templates[0].ID {
		t.Fatalf("a followed charge is a copy of its template: %+v", copies)
	}

	if err := p.charges.Update(ctx, templates[0].ID, domain.ChargeInput{Name: "Wifi+", Kind: "internet", AmountVND: 150, Unit: "per_period"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := p.charges.Delete(ctx, []uuid.UUID{templates[1].ID}); err != nil {
		t.Fatal(err)
	}
	if err := p.charges.Follow(ctx, []uuid.UUID{r}, b); err != nil {
		t.Fatal(err)
	}
	copies, _ = p.charges.OfRoom(ctx, r)
	if len(copies) != 1 || copies[0].Name != "Wifi+" || copies[0].AmountVND != 150 {
		t.Errorf("following again updates in place and drops what is gone: %+v", copies)
	}

	if err := p.charges.Detach(ctx, []uuid.UUID{r}); err != nil {
		t.Fatal(err)
	}
	if own, _ := p.charges.OfRoom(ctx, r); len(own) != 1 || own[0].FollowsBuilding() {
		t.Errorf("a detached copy is the room's own: %+v", own)
	}

	r2 := p.room(t, &b, "P2")
	if err := p.charges.AddToRooms(ctx, []uuid.UUID{r2}, []domain.ChargeInput{{Name: "Own", Kind: "other", AmountVND: 1, Unit: "one_time"}}); err != nil {
		t.Fatal(err)
	}
	if own, _ := p.charges.OfRoom(ctx, r2); len(own) != 1 || own[0].Name != "Own" || own[0].Note != nil {
		t.Errorf("%+v", own)
	}
	if err := p.charges.DeleteOfBuildings(ctx, []uuid.UUID{b}); err != nil {
		t.Fatal(err)
	}
	if left, _ := p.charges.OfBuilding(ctx, b); len(left) != 0 {
		t.Errorf("%+v", left)
	}
}

func TestMediaRepositoryCoversAndOwnership(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	b := p.building(t, "A")
	follower := p.room(t, &b, "P1", domain.GroupMedia)
	owner := domain.BuildingMedia(b)

	if ok, err := p.media.Owns(ctx, p.landlord, owner); err != nil || !ok {
		t.Errorf("%v %v", ok, err)
	}
	if ok, _ := p.media.Owns(ctx, p.other, owner); ok {
		t.Error("another landlord does not own it")
	}
	if ok, _ := p.media.Owns(ctx, p.landlord, domain.RoomMedia(follower)); !ok {
		t.Error("a room is owned too")
	}

	first, err := p.media.Insert(ctx, owner, domain.NewMedia{Kind: "photo", FileKey: "property/a.webp", ThumbKey: "property/a_thumb.jpg", ContentType: "image/webp", SizeBytes: 5, Width: 10, Height: 8, DisplayOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.media.Insert(ctx, owner, domain.NewMedia{Kind: "cover", FileKey: "property/b.webp", ThumbKey: "property/b_thumb.jpg", ContentType: "image/webp", SizeBytes: 5, Width: 10, Height: 8, DisplayOrder: 2})
	if err != nil {
		t.Fatal(err)
	}
	items, err := p.media.OfOwner(ctx, owner)
	if err != nil || len(items) != 2 || items[0].ID != first.ID || items[1].Kind != "cover" || items[1].RoomID != nil {
		t.Fatalf("%+v %v", items, err)
	}

	followers, err := p.media.SetCover(ctx, owner, &domain.Cover{Key: "property/b.webp", ThumbKey: "property/b_thumb.jpg"})
	if err != nil || !slices.Equal(followers, []uuid.UUID{follower}) {
		t.Fatalf("a building's cover is pushed to the rooms that follow its pictures: %v %v", followers, err)
	}
	if room, _ := p.rooms.Get(ctx, p.landlord, follower); room.Cover == nil || room.Cover.Key != "property/b.webp" {
		t.Errorf("%+v", room.Cover)
	}

	if err := p.media.DemoteCovers(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := p.media.SetOrder(ctx, owner, []domain.MediaPlace{{ID: second.ID, Kind: "cover", Position: 1}, {ID: first.ID, Kind: "photo", Position: 2}}); err != nil {
		t.Fatal(err)
	}
	if items, _ = p.media.OfOwner(ctx, owner); items[0].ID != second.ID || items[0].Kind != "cover" {
		t.Errorf("%+v", items)
	}

	keys, err := p.media.Delete(ctx, owner, second.ID)
	if err != nil || !slices.Equal(keys, []string{"property/b.webp", "property/b_thumb.jpg"}) {
		t.Fatalf("%v %v", keys, err)
	}
	if _, err := p.media.Delete(ctx, owner, second.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if err := p.media.Renumber(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if items, _ = p.media.OfOwner(ctx, owner); len(items) != 1 || items[0].DisplayOrder != 1 {
		t.Errorf("%+v", items)
	}

	replaced, err := p.media.Replace(ctx, owner, first.ID, domain.MediaFile{FileKey: "property/c.webp", ThumbKey: "property/c_thumb.jpg", ContentType: "image/webp", SizeBytes: 9, Width: 3, Height: 3})
	if err != nil || replaced.FileKey != "property/c.webp" || replaced.Kind != "photo" || replaced.DisplayOrder != 1 {
		t.Errorf("the place and kind stay: %+v %v", replaced, err)
	}
	if _, err := p.media.Replace(ctx, domain.RoomMedia(follower), first.ID, domain.MediaFile{FileKey: "property/d.webp", ThumbKey: "property/d_thumb.jpg", ContentType: "image/webp"}); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a picture of another owner is not found: %v", err)
	}

	if err := p.media.StopFollowing(ctx, follower); err != nil {
		t.Fatal(err)
	}
	if r, _ := p.rooms.ForUpdate(ctx, p.landlord, follower); r.Follows(domain.GroupMedia) {
		t.Error("the room owns its pictures now")
	}
	keys, err = p.media.DeleteOfBuildings(ctx, []uuid.UUID{b})
	if err != nil || len(keys) != 2 {
		t.Errorf("%v %v", keys, err)
	}
}

func TestMediaRepositoryCopies(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	src, dst := p.building(t, "A"), p.building(t, "B")
	srcMedia, err := p.media.Insert(ctx, domain.BuildingMedia(src), domain.NewMedia{Kind: "photo", FileKey: "property/a.webp", ThumbKey: "property/a_thumb.jpg", ContentType: "image/webp", SizeBytes: 5, Width: 10, Height: 8, DisplayOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	room := p.room(t, &dst, "P1")
	if err := p.media.CopyForBuilding(ctx, dst, []domain.MediaCopy{{SourceID: srcMedia.ID, File: "property/x.webp", Thumb: "property/x_thumb.jpg"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.media.CopyForRooms(ctx, []domain.RoomMediaCopy{{RoomID: room, MediaCopy: domain.MediaCopy{SourceID: srcMedia.ID, File: "property/y.webp", Thumb: "property/y_thumb.jpg"}}}); err != nil {
		t.Fatal(err)
	}
	if items, _ := p.media.OfOwner(ctx, domain.BuildingMedia(dst)); len(items) != 1 || items[0].FileKey != "property/x.webp" || items[0].Width != 10 {
		t.Errorf("a copy keeps the data of its source: %+v", items)
	}
	items, _ := p.media.OfRooms(ctx, []uuid.UUID{room})
	if len(items) != 1 || items[0].RoomID == nil || *items[0].RoomID != room {
		t.Errorf("%+v", items)
	}
	if err := p.media.CoverRoomsFromBuilding(ctx, []uuid.UUID{room}); err != nil {
		t.Fatal(err)
	}
	if keys, err := p.media.DeleteOfRooms(ctx, []uuid.UUID{room}); err != nil || len(keys) != 2 {
		t.Errorf("%v %v", keys, err)
	}
}

func TestPropertyRepositoriesJoinTheTransactionOfTheContext(t *testing.T) {
	ctx := context.Background()
	p := newPropertyRepos(t)
	boom := context.Canceled
	var id uuid.UUID
	err := p.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := p.buildings.LockLandlord(ctx, p.landlord); err != nil {
			return err
		}
		var err error
		id, err = p.buildings.Insert(ctx, p.landlord, domain.BuildingData{Name: "Rolled back", BuildingDefaults: domain.BuildingDefaults{AddressDetail: "x"}})
		if err != nil {
			return err
		}
		if _, err := p.buildings.ForUpdate(ctx, p.landlord, id); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	if _, err := p.buildings.ForUpdate(ctx, p.landlord, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a rolled back building is gone: %v", err)
	}
}
