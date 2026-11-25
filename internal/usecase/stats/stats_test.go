package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/stats"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// now is 25 Nov 2026, 10:00 in Vietnam.
var now = time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)

type fakeRepo struct {
	rooms       []domain.RoomFact
	contracts   []domain.RentedContract
	counts      domain.ContractCounts
	billed      map[string]int64
	collected   map[string]int64
	outstanding map[string]int64
	owed        domain.Owed
	roomBilled  map[uuid.UUID]int64
	roomPaid    map[uuid.UUID]int64
	uses        []domain.UtilityUse
	calls       int
}

func (f *fakeRepo) Rooms(context.Context, domain.Scope, time.Time) ([]domain.RoomFact, error) {
	f.calls++
	return f.rooms, nil
}
func (f *fakeRepo) RentedContracts(context.Context, domain.Scope, time.Time, time.Time) ([]domain.RentedContract, error) {
	return f.contracts, nil
}
func (f *fakeRepo) ContractCounts(context.Context, domain.Scope, time.Time, time.Time) (domain.ContractCounts, error) {
	return f.counts, nil
}
func (f *fakeRepo) BilledByMonth(context.Context, domain.Scope, time.Time, time.Time) (map[string]int64, error) {
	return f.billed, nil
}
func (f *fakeRepo) CollectedByMonth(context.Context, domain.Scope, time.Time, time.Time) (map[string]int64, error) {
	return f.collected, nil
}
func (f *fakeRepo) OutstandingByMonth(context.Context, domain.Scope, time.Time, time.Time) (map[string]int64, error) {
	return f.outstanding, nil
}
func (f *fakeRepo) Owed(context.Context, domain.Scope, time.Time) (domain.Owed, error) {
	return f.owed, nil
}
func (f *fakeRepo) BilledByRoom(context.Context, domain.Scope, time.Time, time.Time) (map[uuid.UUID]int64, error) {
	return f.roomBilled, nil
}
func (f *fakeRepo) CollectedByRoom(context.Context, domain.Scope, time.Time, time.Time) (map[uuid.UUID]int64, error) {
	return f.roomPaid, nil
}
func (f *fakeRepo) Utilities(context.Context, domain.Scope, time.Time, time.Time) ([]domain.UtilityUse, error) {
	return f.uses, nil
}

type memoryCache struct {
	data map[string][]byte
	gets int
}

func (c *memoryCache) Get(_ context.Context, key string, dst any) (bool, error) {
	c.gets++
	raw, ok := c.data[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dst)
}

func (c *memoryCache) Set(_ context.Context, key string, v any, _ time.Duration) error {
	raw, err := json.Marshal(v)
	c.data[key] = raw
	return err
}

var (
	roomA, roomB, roomC = uuid.New(), uuid.New(), uuid.New()
	tower               = uuid.New()
	towerName           = "Tower"
)

func world() *fakeRepo {
	return &fakeRepo{
		rooms: []domain.RoomFact{
			{ID: roomA, BuildingID: &tower, BuildingName: &towerName, Name: "A", Status: "rented", CreatedOn: day(2026, 1, 1)},
			{ID: roomB, BuildingID: &tower, BuildingName: &towerName, Name: "B", Status: "available", CreatedOn: day(2026, 1, 1)},
			{ID: roomC, Name: "C", Status: "maintenance", CreatedOn: day(2026, 1, 1), DeletedOn: ptrDay(day(2026, 6, 30))},
		},
		contracts: []domain.RentedContract{{RoomID: roomA, StartsOn: day(2026, 1, 1)}},
		counts:    domain.ContractCounts{Active: 1, Pending: 2, ExpiringSoon: 1},
		// October and November of 2026
		billed:      map[string]int64{"2026-10": 4_000_000, "2026-11": 6_000_000},
		collected:   map[string]int64{"2026-10": 4_000_000, "2026-11": 5_000_000},
		outstanding: map[string]int64{"2026-11": 1_500_000},
		owed:        domain.Owed{OutstandingVND: 1_500_000, OverdueVND: 500_000, OverdueCount: 1},
		roomBilled:  map[uuid.UUID]int64{roomA: 7_000_000, roomB: 2_000_000, roomC: 1_000_000},
		roomPaid:    map[uuid.UUID]int64{roomA: 7_000_000, roomB: 500_000},
		uses: []domain.UtilityUse{
			{Month: "2026-11", Kind: domain.Electricity, Consumption: 120.5, CostVND: 421_750},
			{Month: "2026-11", Kind: domain.Water, Consumption: 8, CostVND: 120_000},
			{Month: "2025-01", Kind: domain.Water, Consumption: 1, CostVND: 1}, // outside the range: ignored
		},
	}
}

func ptrDay(t time.Time) *time.Time { return &t }

func newProperty(repo *fakeRepo, cache Cache, ttl time.Duration) *Property {
	return NewProperty(repo, cache, ClockFunc(func() time.Time { return now }), slog.New(slog.DiscardHandler), ttl)
}

func query(t *testing.T) Query {
	t.Helper()
	rng, err := domain.NewMonthRange(day(2026, 9, 1), day(2026, 11, 25))
	if err != nil {
		t.Fatal(err)
	}
	return Query{LandlordID: uuid.New(), Role: "landlord", Range: rng, Sort: domain.SortBilled}
}

func TestOverview(t *testing.T) {
	o, err := newProperty(world(), nil, 0).Overview(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	// Two rooms stand (C is deleted): one rented of two = 50 %.
	if o.Rooms != (RoomCounts{Total: 2, Rented: 1, Available: 1}) || o.OccupancyRate == nil || *o.OccupancyRate != 50 {
		t.Errorf("rooms %+v rate %v", o.Rooms, o.OccupancyRate)
	}
	if o.ActiveContracts != 1 || o.PendingRegistrations != 2 || o.ExpiringSoon != 1 {
		t.Errorf("%+v", o)
	}
	m := o.Money
	if m.OutstandingVND != 1_500_000 || m.OverdueVND != 500_000 || m.OverdueCount != 1 {
		t.Errorf("owed %+v", m)
	}
	// November billed 6M, 1.5M of it still unpaid: 4.5M / 6M = 75 %. October is fully paid: 100 %.
	if m.ThisMonth.BilledVND != 6_000_000 || m.ThisMonth.CollectedVND != 5_000_000 || m.ThisMonth.CollectionRate == nil || *m.ThisMonth.CollectionRate != 75 {
		t.Errorf("this month %+v", m.ThisMonth)
	}
	if m.PreviousMonth.CollectionRate == nil || *m.PreviousMonth.CollectionRate != 100 {
		t.Errorf("previous month %+v", m.PreviousMonth)
	}
	// billed 6M vs 4M = +50 %; collected 5M vs 4M = +25 %.
	if m.BilledChange == nil || *m.BilledChange != 50 || m.CollectedChange == nil || *m.CollectedChange != 25 {
		t.Errorf("change %v %v", m.BilledChange, m.CollectedChange)
	}
}

func TestOverviewWithNothingBilledHasNoRates(t *testing.T) {
	repo := &fakeRepo{}
	o, err := newProperty(repo, nil, 0).Overview(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	if o.OccupancyRate != nil || o.Money.ThisMonth.CollectionRate != nil || o.Money.BilledChange != nil {
		t.Errorf("nothing to divide by is null, never 0: %+v", o)
	}
}

func TestRevenueFillsEveryMonthAndSums(t *testing.T) {
	r, err := newProperty(world(), nil, 0).Revenue(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Months) != 3 || r.Months[0] != (RevenueMonth{Month: "2026-09"}) || r.Months[1].BilledVND != 4_000_000 || r.Months[2].OutstandingVND != 1_500_000 {
		t.Errorf("%+v", r.Months)
	}
	if r.Totals != (MonthMoney{Billed: 10_000_000, Collected: 9_000_000, Outstanding: 1_500_000}) {
		t.Errorf("%+v", r.Totals)
	}
}

func TestOccupancyByMonth(t *testing.T) {
	months, err := newProperty(world(), nil, 0).Occupancy(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	// September: A and B exist all 30 days (C is gone), A is rented: 30 of 60 = 50 %.
	if months[0].AvailableDays != 60 || months[0].OccupiedDays != 30 || months[0].Rate == nil || *months[0].Rate != 50 {
		t.Errorf("%+v", months[0])
	}
	// November counts up to the 25th: 25 of 50.
	if months[2].AvailableDays != 50 || months[2].OccupiedDays != 25 {
		t.Errorf("%+v", months[2])
	}
}

func TestRoomsAreRankedAndCut(t *testing.T) {
	q := query(t)
	q.Limit = 2
	rooms, err := newProperty(world(), nil, 0).Rooms(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 || rooms[0].RoomID != roomA || rooms[1].RoomID != roomB {
		t.Fatalf("by billed: %+v", rooms)
	}
	if rooms[0].BilledVND != 7_000_000 || rooms[0].CollectedVND != 7_000_000 {
		t.Errorf("%+v", rooms[0])
	}
	q.Sort, q.Limit = domain.SortCollected, 0
	byPaid, _ := newProperty(world(), nil, 0).Rooms(context.Background(), q)
	if len(byPaid) != 3 || byPaid[2].RoomID != roomC {
		t.Errorf("the room that collected nothing is last: %+v", byPaid)
	}
	q.Sort = domain.SortOccupancy
	byOcc, _ := newProperty(world(), nil, 0).Rooms(context.Background(), q)
	if byOcc[0].RoomID != roomA || *byOcc[0].OccupancyRate != 100 {
		t.Errorf("A was rented the whole time: %+v", byOcc[0])
	}
}

func TestLimitIsCapped(t *testing.T) {
	q := query(t)
	q.Limit = 5000
	if got := newProperty(world(), nil, 0).start(q).q.Limit; got != MaxLimit {
		t.Errorf("limit %d", got)
	}
}

func TestBuildingsGroupTheRooms(t *testing.T) {
	list, err := newProperty(world(), nil, 0).Buildings(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].BuildingID == nil || *list[0].BuildingID != tower {
		t.Fatalf("%+v", list)
	}
	tw := list[0]
	if tw.Rooms != 2 || tw.Rented != 1 || tw.BilledVND != 9_000_000 || tw.CollectedVND != 7_500_000 {
		t.Errorf("%+v", tw)
	}
	// The room that belongs to no building (and is deleted: counted for money, not as a room standing).
	none := list[1]
	if none.BuildingID != nil || none.Name != nil || none.Rooms != 0 || none.BilledVND != 1_000_000 {
		t.Errorf("%+v", none)
	}
}

func TestUtilitiesFillTheMonths(t *testing.T) {
	months, err := newProperty(world(), nil, 0).Utilities(context.Background(), query(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 3 || months[0].Electricity != (Utility{}) {
		t.Fatalf("%+v", months)
	}
	if months[2].Electricity != (Utility{Consumption: 120.5, CostVND: 421_750}) || months[2].Water != (Utility{Consumption: 8, CostVND: 120_000}) {
		t.Errorf("%+v", months[2])
	}
}

func TestTheDashboardIsTheEndpointsInOne(t *testing.T) {
	ctx, q := context.Background(), query(t)
	p := newProperty(world(), nil, 0)
	d, err := p.Dashboard(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	overview, _ := p.Overview(ctx, q)
	revenue, _ := p.Revenue(ctx, q)
	occupancy, _ := p.Occupancy(ctx, q)
	rooms, _ := p.Rooms(ctx, q)
	utilities, _ := p.Utilities(ctx, q)
	if !reflect.DeepEqual(d, Dashboard{Overview: overview, Revenue: revenue, Occupancy: occupancy, Rooms: rooms, Utilities: utilities}) {
		t.Errorf("the dashboard differs from its parts:\n%+v", d)
	}
}

func TestADashboardLoadsTheRoomsOnce(t *testing.T) {
	repo := world()
	if _, err := newProperty(repo, nil, 0).Dashboard(context.Background(), query(t)); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 1 {
		t.Errorf("rooms read %d times", repo.calls)
	}
}

func TestAnswersAreCachedPerAskerAndDay(t *testing.T) {
	repo, cache := world(), &memoryCache{data: map[string][]byte{}}
	p := newProperty(repo, cache, time.Minute)
	q := query(t)
	first, err := p.Dashboard(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Dashboard(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 1 || !reflect.DeepEqual(first, second) {
		t.Errorf("the second answer comes from the cache: calls %d", repo.calls)
	}
	other := q
	other.LandlordID = uuid.New()
	if _, err := p.Dashboard(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 2 {
		t.Error("another landlord has an answer of their own")
	}
	inBuilding := q
	inBuilding.BuildingID = &tower
	if _, err := p.Dashboard(context.Background(), inBuilding); err != nil || repo.calls != 3 {
		t.Error("a building filter has an answer of its own")
	}
}

type fakePlatform struct{ facts domain.PlatformFacts }

func (f fakePlatform) Facts(context.Context, time.Time, time.Time, time.Time, time.Time) (domain.PlatformFacts, error) {
	return f.facts, nil
}
func (f fakePlatform) SignUps(context.Context, time.Time, time.Time) (map[string]int, map[string]int, error) {
	return map[string]int{"2026-10": 4}, map[string]int{"2026-11": 2}, nil
}
func (f fakePlatform) System(context.Context, time.Time) (domain.SystemFacts, error) {
	return domain.SystemFacts{Admins: 2, SuperAdmins: 1, Plans: []domain.PlanFacts{
		{Code: "pro", PriceVND: 100_000, BillingInterval: "month", Subscribers: 3},
		{Code: "free", BillingInterval: "month", Subscribers: 9},
	}}, nil
}

func TestPlatformOverview(t *testing.T) {
	p := NewPlatform(fakePlatform{facts: domain.PlatformFacts{
		Users:                domain.UserCounts{Total: 10, Active: 9, NewThisMonth: 3, NewPreviousMonth: 2},
		UsersByRole:          map[string]int{"tenant": 8, "landlord": 2},
		ApplicationsByStatus: map[string]int{"pending_review": 1},
		WaitingApplications:  1, OldestWaitingDays: 4,
		AnnouncementRead: 3, AnnouncementReached: 8,
	}}, ClockFunc(func() time.Time { return now }))
	o, err := p.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.Users.Change == nil || *o.Users.Change != 50 || o.Users.ByRole["manager"] != 0 || len(o.Users.ByRole) != 5 {
		t.Errorf("%+v", o.Users)
	}
	if o.Applications.AverageReviewHours != nil {
		t.Error("nothing was reviewed: no average")
	}
	if o.Applications.OldestWaitingDays == nil || *o.Applications.OldestWaitingDays != 4 || o.Applications.ByStatus["approved"] != 0 {
		t.Errorf("%+v", o.Applications)
	}
	if o.Announcements.RecentReadRate == nil || *o.Announcements.RecentReadRate != 37.5 {
		t.Errorf("%+v", o.Announcements)
	}
}

func TestPlatformGrowthFillsTheMonths(t *testing.T) {
	rng, _ := domain.NewMonthRange(day(2026, 9, 1), day(2026, 11, 1))
	out, err := NewPlatform(fakePlatform{}, ClockFunc(func() time.Time { return now })).Growth(context.Background(), rng)
	if err != nil {
		t.Fatal(err)
	}
	want := []GrowthMonth{{Month: "2026-09"}, {Month: "2026-10", Users: 4}, {Month: "2026-11", Applications: 2}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("%+v", out)
	}
}

func TestSystemFigures(t *testing.T) {
	s, err := NewPlatform(fakePlatform{}, ClockFunc(func() time.Time { return now })).System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.MonthlyRecurringVND != 300_000 || s.PaidSubscribers != 3 || len(s.Plans) != 2 || s.Admins != 2 {
		t.Errorf("%+v", s)
	}
}
