package stats

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/stats"
)

// Limits of a ranking.
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// expiringDays is how soon a contract that ends counts as "ending soon".
const expiringDays = 30

// Property serves a landlord's figures about their rooms. Money has three readings: billed (invoices sent or paid, by
// the month their period starts; a draft is not billed), collected (confirmed payments, by the day they were paid) and
// still to collect (what the sent invoices lack of their confirmed payments). An answer is kept in the cache for the
// time to live, so a figure can lag by that much.
type Property struct {
	repo  PropertyRepository
	cache Cache
	clock Clock
	log   *slog.Logger
	ttl   time.Duration
}

// NewProperty returns the use cases; a ttl of zero switches the cache off.
func NewProperty(repo PropertyRepository, cache Cache, clock Clock, log *slog.Logger, ttl time.Duration) *Property {
	return &Property{repo: repo, cache: cache, clock: clock, log: log, ttl: ttl}
}

// run is one answer being worked out: the clock is read once and the rooms are loaded at most once, so the dashboard
// gives the very figures of the endpoints it gathers.
type run struct {
	p     *Property
	q     Query
	scope domain.Scope
	now   time.Time
	today time.Time
	rooms []domain.RoomFact
	lives map[uuid.UUID]domain.RoomLife
}

func (p *Property) start(q Query) *run {
	now := p.clock.Now()
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	return &run{p: p, q: q, scope: domain.Scope{LandlordID: q.LandlordID, BuildingID: q.BuildingID}, now: now, today: domain.Day(now)}
}

// load reads the rooms and the days they were rented.
func (r *run) load(ctx context.Context) error {
	if r.lives != nil {
		return nil
	}
	rooms, err := r.p.repo.Rooms(ctx, r.scope, r.q.Range.First())
	if err != nil {
		return err
	}
	contracts, err := r.p.repo.RentedContracts(ctx, r.scope, r.q.Range.First(), r.q.Range.Last())
	if err != nil {
		return err
	}
	lives := make(map[uuid.UUID]domain.RoomLife, len(rooms))
	for _, room := range rooms {
		lives[room.ID] = domain.RoomLife{Existed: room.Life()}
	}
	for _, c := range contracts {
		if life, ok := lives[c.RoomID]; ok {
			life.Rented = append(life.Rented, c.Span(r.today))
			lives[c.RoomID] = life
		}
	}
	r.rooms, r.lives = rooms, lives
	return nil
}

// money reads the three readings for every month of rng.
func (r *run) money(ctx context.Context, rng domain.MonthRange) ([]MonthMoney, error) {
	billed, err := r.p.repo.BilledByMonth(ctx, r.scope, rng.First(), rng.Last())
	if err != nil {
		return nil, err
	}
	collected, err := r.p.repo.CollectedByMonth(ctx, r.scope, rng.First(), rng.Last())
	if err != nil {
		return nil, err
	}
	outstanding, err := r.p.repo.OutstandingByMonth(ctx, r.scope, rng.First(), rng.Last())
	if err != nil {
		return nil, err
	}
	out := make([]MonthMoney, len(rng.Months()))
	for i, m := range rng.Months() {
		key := m.String()
		out[i] = MonthMoney{Billed: billed[key], Collected: collected[key], Outstanding: outstanding[key]}
	}
	return out, nil
}

func (r *run) overview(ctx context.Context) (Overview, error) {
	if err := r.load(ctx); err != nil {
		return Overview{}, err
	}
	var counts RoomCounts
	for _, room := range r.rooms {
		if room.Deleted() {
			continue
		}
		counts.Total++
		switch room.Status {
		case domain.RoomRented:
			counts.Rented++
		case domain.RoomAvailable:
			counts.Available++
		case domain.RoomMaintenance:
			counts.Maintenance++
		}
	}
	contracts, err := r.p.repo.ContractCounts(ctx, r.scope, r.today, r.today.AddDate(0, 0, expiringDays))
	if err != nil {
		return Overview{}, err
	}
	owed, err := r.p.repo.Owed(ctx, r.scope, r.now)
	if err != nil {
		return Overview{}, err
	}
	this := domain.MonthOf(r.today)
	pair, err := domain.NewMonthRange(this.Previous().First(), this.First())
	if err != nil {
		return Overview{}, err
	}
	months, err := r.money(ctx, pair)
	if err != nil {
		return Overview{}, err
	}
	previous, current := months[0], months[1]
	return Overview{
		Rooms:                counts,
		OccupancyRate:        domain.Percent(int64(counts.Rented), int64(counts.Total)),
		ActiveContracts:      contracts.Active,
		PendingRegistrations: contracts.Pending,
		ExpiringSoon:         contracts.ExpiringSoon,
		Money: OverviewMoney{
			OutstandingVND: owed.OutstandingVND, OverdueVND: owed.OverdueVND, OverdueCount: owed.OverdueCount,
			ThisMonth:       figuresOf(current),
			PreviousMonth:   figuresOf(previous),
			BilledChange:    domain.Change(current.Billed, previous.Billed),
			CollectedChange: domain.Change(current.Collected, previous.Collected),
		},
	}, nil
}

// figuresOf is a month's billed and collected money; the collection rate is the part of the month's invoices that is
// paid, so money collected for an older invoice does not lift it.
func figuresOf(m MonthMoney) MonthFigures {
	paid := m.Billed - m.Outstanding
	if paid < 0 {
		paid = 0
	}
	return MonthFigures{BilledVND: m.Billed, CollectedVND: m.Collected, CollectionRate: domain.Percent(paid, m.Billed)}
}

func (r *run) revenue(ctx context.Context) (Revenue, error) {
	months, err := r.money(ctx, r.q.Range)
	if err != nil {
		return Revenue{}, err
	}
	out := Revenue{Months: make([]RevenueMonth, len(months))}
	for i, m := range months {
		out.Months[i] = RevenueMonth{Month: r.q.Range.Months()[i].String(), BilledVND: m.Billed, CollectedVND: m.Collected, OutstandingVND: m.Outstanding}
		out.Totals.Billed += m.Billed
		out.Totals.Collected += m.Collected
		out.Totals.Outstanding += m.Outstanding
	}
	return out, nil
}

func (r *run) occupancy(ctx context.Context) ([]OccupancyMonth, error) {
	if err := r.load(ctx); err != nil {
		return nil, err
	}
	lives := make([]domain.RoomLife, 0, len(r.lives))
	for _, life := range r.lives {
		lives = append(lives, life)
	}
	months := r.q.Range.Months()
	byMonth := domain.OfMonths(lives, months, r.today)
	out := make([]OccupancyMonth, len(months))
	for i, o := range byMonth {
		out[i] = OccupancyMonth{Month: months[i].String(), OccupiedDays: o.Occupied, AvailableDays: o.Available, Rate: o.Rate()}
	}
	return out, nil
}

// roomFigures works out every room of the scope over the whole range.
func (r *run) roomFigures(ctx context.Context) ([]RoomFigures, error) {
	if err := r.load(ctx); err != nil {
		return nil, err
	}
	billed, err := r.p.repo.BilledByRoom(ctx, r.scope, r.q.Range.First(), r.q.Range.Last())
	if err != nil {
		return nil, err
	}
	collected, err := r.p.repo.CollectedByRoom(ctx, r.scope, r.q.Range.First(), r.q.Range.Last())
	if err != nil {
		return nil, err
	}
	out := make([]RoomFigures, 0, len(r.rooms))
	for _, room := range r.rooms {
		o := domain.OfRoom(r.lives[room.ID], r.q.Range.First(), r.q.Range.Last(), r.today)
		out = append(out, RoomFigures{
			RoomID: room.ID, BuildingID: room.BuildingID, Name: room.Name,
			BilledVND: billed[room.ID], CollectedVND: collected[room.ID],
			OccupiedDays: o.Occupied, AvailableDays: o.Available, OccupancyRate: o.Rate(),
		})
	}
	return out, nil
}

func (r *run) rankedRooms(ctx context.Context) ([]RoomFigures, error) {
	all, err := r.roomFigures(ctx)
	if err != nil {
		return nil, err
	}
	key := func(f RoomFigures) float64 {
		switch r.q.Sort {
		case domain.SortCollected:
			return float64(f.CollectedVND)
		case domain.SortOccupancy:
			if f.OccupancyRate == nil {
				return -1
			}
			return *f.OccupancyRate
		}
		return float64(f.BilledVND)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if ki, kj := key(all[i]), key(all[j]); ki != kj {
			return ki > kj
		}
		return all[i].Name < all[j].Name
	})
	if len(all) > r.q.Limit {
		all = all[:r.q.Limit]
	}
	return all, nil
}

func (r *run) buildings(ctx context.Context) ([]BuildingFigures, error) {
	all, err := r.roomFigures(ctx)
	if err != nil {
		return nil, err
	}
	byRoom := make(map[uuid.UUID]domain.RoomFact, len(r.rooms))
	for _, room := range r.rooms {
		byRoom[room.ID] = room
	}
	type tally struct {
		figures BuildingFigures
		o       domain.Occupancy
	}
	var order []string
	groups := map[string]*tally{}
	for _, f := range all {
		room := byRoom[f.RoomID]
		key := ""
		if room.BuildingID != nil {
			key = room.BuildingID.String()
		}
		g, ok := groups[key]
		if !ok {
			g = &tally{figures: BuildingFigures{BuildingID: room.BuildingID, Name: room.BuildingName}}
			groups[key] = g
			order = append(order, key)
		}
		if !room.Deleted() {
			g.figures.Rooms++
			if room.Status == domain.RoomRented {
				g.figures.Rented++
			}
		}
		g.figures.BilledVND += f.BilledVND
		g.figures.CollectedVND += f.CollectedVND
		g.o = g.o.Add(domain.Occupancy{Occupied: f.OccupiedDays, Available: f.AvailableDays})
	}
	out := make([]BuildingFigures, 0, len(order))
	for _, key := range order {
		g := groups[key]
		g.figures.OccupiedDays, g.figures.AvailableDays, g.figures.OccupancyRate = g.o.Occupied, g.o.Available, g.o.Rate()
		out = append(out, g.figures)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].BilledVND != out[j].BilledVND {
			return out[i].BilledVND > out[j].BilledVND
		}
		return deref(out[i].Name) < deref(out[j].Name)
	})
	if len(out) > r.q.Limit {
		out = out[:r.q.Limit]
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *run) utilities(ctx context.Context) ([]UtilityMonth, error) {
	uses, err := r.p.repo.Utilities(ctx, r.scope, r.q.Range.First(), r.q.Range.Last())
	if err != nil {
		return nil, err
	}
	months := r.q.Range.Months()
	out := make([]UtilityMonth, len(months))
	for i, m := range months {
		out[i].Month = m.String()
	}
	for _, u := range uses {
		i := r.q.Range.Index(u.Month)
		if i < 0 {
			continue
		}
		switch u.Kind {
		case domain.Electricity:
			out[i].Electricity = Utility{Consumption: u.Consumption, CostVND: u.CostVND}
		case domain.Water:
			out[i].Water = Utility{Consumption: u.Consumption, CostVND: u.CostVND}
		}
	}
	return out, nil
}

func (r *run) dashboard(ctx context.Context) (Dashboard, error) {
	var (
		d   Dashboard
		err error
	)
	if d.Overview, err = r.overview(ctx); err != nil {
		return d, err
	}
	if d.Revenue, err = r.revenue(ctx); err != nil {
		return d, err
	}
	if d.Occupancy, err = r.occupancy(ctx); err != nil {
		return d, err
	}
	if d.Rooms, err = r.rankedRooms(ctx); err != nil {
		return d, err
	}
	d.Utilities, err = r.utilities(ctx)
	return d, err
}

// cached answers from the cache when it can and keeps what it works out otherwise. The key holds who asks, in which
// role, what they ask and the day (what is overdue and which month is "this" change with the day).
func cached[T any](ctx context.Context, p *Property, name string, r *run, compute func() (T, error)) (T, error) {
	var out T
	if p.ttl <= 0 || p.cache == nil {
		return compute()
	}
	building := "all"
	if r.q.BuildingID != nil {
		building = r.q.BuildingID.String()
	}
	key := fmt.Sprintf("stats:%s:%s:%s:%s:%s-%s:%s:%d:%s", name, r.q.LandlordID, r.q.Role, building,
		r.q.Range.First().Format(time.DateOnly), r.q.Range.Last().Format(time.DateOnly), r.q.Sort, r.q.Limit, r.today.Format(time.DateOnly))
	if hit, err := p.cache.Get(ctx, key, &out); err != nil {
		p.log.WarnContext(ctx, "stats cache read failed", "err", err)
	} else if hit {
		return out, nil
	}
	out, err := compute()
	if err != nil {
		return out, err
	}
	if err := p.cache.Set(ctx, key, out, p.ttl); err != nil {
		p.log.WarnContext(ctx, "stats cache write failed", "err", err)
	}
	return out, nil
}

// Overview is the top of the page: rooms, contracts and the money owed and billed this month.
func (p *Property) Overview(ctx context.Context, q Query) (Overview, error) {
	r := p.start(q)
	return cached(ctx, p, "overview", r, func() (Overview, error) { return r.overview(ctx) })
}

// Revenue is the money of every month of the range.
func (p *Property) Revenue(ctx context.Context, q Query) (Revenue, error) {
	r := p.start(q)
	return cached(ctx, p, "revenue", r, func() (Revenue, error) { return r.revenue(ctx) })
}

// Occupancy is the share of days rooms were rented, month by month.
func (p *Property) Occupancy(ctx context.Context, q Query) ([]OccupancyMonth, error) {
	r := p.start(q)
	return cached(ctx, p, "occupancy", r, func() ([]OccupancyMonth, error) { return r.occupancy(ctx) })
}

// Rooms ranks the rooms by the sort key of the query and cuts the list at its limit.
func (p *Property) Rooms(ctx context.Context, q Query) ([]RoomFigures, error) {
	r := p.start(q)
	return cached(ctx, p, "rooms", r, func() ([]RoomFigures, error) { return r.rankedRooms(ctx) })
}

// Buildings sums the rooms by building, richest first, cut at the limit of the query.
func (p *Property) Buildings(ctx context.Context, q Query) ([]BuildingFigures, error) {
	r := p.start(q)
	r.q.BuildingID, r.scope.BuildingID = nil, nil
	return cached(ctx, p, "buildings", r, func() ([]BuildingFigures, error) { return r.buildings(ctx) })
}

// Utilities is the electricity and water read each month, and what they cost.
func (p *Property) Utilities(ctx context.Context, q Query) ([]UtilityMonth, error) {
	r := p.start(q)
	return cached(ctx, p, "utilities", r, func() ([]UtilityMonth, error) { return r.utilities(ctx) })
}

// Dashboard gives the five figures of the front page in one answer.
func (p *Property) Dashboard(ctx context.Context, q Query) (Dashboard, error) {
	r := p.start(q)
	return cached(ctx, p, "dashboard", r, func() (Dashboard, error) { return r.dashboard(ctx) })
}
