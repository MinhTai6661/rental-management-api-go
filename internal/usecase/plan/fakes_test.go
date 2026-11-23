package plan

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// The plan use cases run here against hand-written fakes of the ports: no database. fakeStore is one in-memory
// world (accounts, plans, subscriptions) that implements every repository port, so a test reads like a story.

type fakeStore struct {
	now      time.Time
	accounts map[uuid.UUID]*domain.Account
	plans    map[uuid.UUID]*domain.Plan
	byOrder  []uuid.UUID
	subs     map[uuid.UUID]*domain.Subscription
	subOrder []uuid.UUID
	rooms    map[uuid.UUID]int
	failNext error
}

func newStore(now time.Time) *fakeStore {
	return &fakeStore{
		now: now, accounts: map[uuid.UUID]*domain.Account{}, plans: map[uuid.UUID]*domain.Plan{},
		subs: map[uuid.UUID]*domain.Subscription{}, rooms: map[uuid.UUID]int{},
	}
}

func (f *fakeStore) Now() time.Time { return f.now }

func (f *fakeStore) account(name string, roles ...string) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.accounts[id] = &domain.Account{ID: id, Name: name, Roles: roles}
	return id
}

func n(v int) *int { return &v }

func (f *fakeStore) plan(code string, audience domain.Audience, price int64) *domain.Plan {
	p := &domain.Plan{
		ID: uuid.Must(uuid.NewV7()), Code: code, Name: code, Audience: audience, PriceVND: price, Interval: domain.IntervalMonth,
		Features: []domain.Feature{}, IsActive: true,
	}
	f.plans[p.ID] = p
	f.byOrder = append(f.byOrder, p.ID)
	return p
}

// putSub stores a subscription directly.
func (f *fakeStore) putSub(user uuid.UUID, p *domain.Plan, status domain.Status, ends *time.Time) *domain.Subscription {
	s := &domain.Subscription{
		ID: uuid.Must(uuid.NewV7()), UserID: user, PlanID: p.ID, Audience: p.Audience, Status: status,
		StartsAt: f.now.Add(-time.Hour), EndsAt: ends, CreatedAt: f.now,
	}
	f.subs[s.ID] = s
	f.subOrder = append(f.subOrder, s.ID)
	return s
}

func (f *fakeStore) countStatus(user uuid.UUID, aud domain.Audience, st domain.Status) int {
	c := 0
	for _, s := range f.subs {
		if s.UserID == user && s.Audience == aud && s.Status == st {
			c++
		}
	}
	return c
}

func (f *fakeStore) detail(s *domain.Subscription) domain.Detail {
	d := domain.Detail{Subscription: *s, Plan: *f.plans[s.PlanID]}
	if a, ok := f.accounts[s.UserID]; ok {
		d.UserName = a.Name
	}
	return d
}

// TxManager

func (f *fakeStore) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// Accounts

func (f *fakeStore) Lock(_ context.Context, id uuid.UUID) (domain.Account, error) {
	if a, ok := f.accounts[id]; ok {
		return *a, nil
	}
	return domain.Account{}, shared.ErrNotFound
}

// UsageReader

func (f *fakeStore) Landlord(_ context.Context, id uuid.UUID) (domain.LandlordUsage, error) {
	return domain.LandlordUsage{Rooms: f.rooms[id], Buildings: 1, ActiveContracts: 2}, nil
}

func (f *fakeStore) PendingRegistrations(context.Context, uuid.UUID) (int, error) { return 3, nil }

// PlanRepository

func (f *fakeStore) Search(_ context.Context, q domain.PlanQuery) ([]domain.Plan, int, error) {
	var out []domain.Plan
	for _, id := range f.byOrder {
		p := f.plans[id]
		if q.Audience != nil && p.Audience != *q.Audience || q.ActiveOnly && !p.IsActive {
			continue
		}
		out = append(out, *p)
	}
	total := len(out)
	if q.Limit > 0 && q.Offset < len(out) {
		out = out[q.Offset:min(len(out), q.Offset+q.Limit)]
	}
	return out, total, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (domain.Plan, error) {
	if p, ok := f.plans[id]; ok {
		return *p, nil
	}
	return domain.Plan{}, shared.ErrNotFound
}

func (f *fakeStore) GetByCode(_ context.Context, code string) (domain.Plan, error) {
	for _, p := range f.plans {
		if p.Code == code {
			return *p, nil
		}
	}
	return domain.Plan{}, shared.ErrNotFound
}

func (f *fakeStore) Default(_ context.Context, a domain.Audience) (domain.Plan, error) {
	for _, p := range f.plans {
		if p.Audience == a && p.IsDefault && p.IsActive {
			return *p, nil
		}
	}
	return domain.Plan{}, shared.ErrNotFound
}

func (f *fakeStore) Insert(_ context.Context, p domain.Plan) (uuid.UUID, error) {
	for _, o := range f.plans {
		if o.Code == p.Code {
			return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: planCodeConstraint}
		}
	}
	p.ID = uuid.Must(uuid.NewV7())
	f.plans[p.ID] = &p
	f.byOrder = append(f.byOrder, p.ID)
	return p.ID, nil
}

func (f *fakeStore) Update(_ context.Context, p domain.Plan) error {
	for _, o := range f.plans {
		if o.Code == p.Code && o.ID != p.ID {
			return &shared.ConstraintError{Kind: shared.Unique, Constraint: planCodeConstraint}
		}
	}
	*f.plans[p.ID] = p
	return nil
}

func (f *fakeStore) ClearDefault(_ context.Context, a domain.Audience, except uuid.UUID) error {
	for _, p := range f.plans {
		if p.Audience == a && p.ID != except {
			p.IsDefault = false
		}
	}
	return nil
}

// SubscriptionRepository

func (f *fakeStore) InsertSub(_ context.Context, s domain.Subscription) (uuid.UUID, error) {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return uuid.Nil, err
	}
	// The database allows one active and one pending subscription per user and audience.
	for _, o := range f.subs {
		if o.UserID == s.UserID && o.Audience == s.Audience && o.Status == s.Status && (s.Status == domain.StatusActive || s.Status == domain.StatusPending) {
			return uuid.Nil, &shared.ConstraintError{Kind: shared.Unique, Constraint: "subscriptions_user_audience_" + string(s.Status) + "_key"}
		}
	}
	s.ID = uuid.Must(uuid.NewV7())
	f.subs[s.ID] = &s
	f.subOrder = append(f.subOrder, s.ID)
	return s.ID, nil
}

func (f *fakeStore) Lock2(_ context.Context, id uuid.UUID) (domain.Subscription, error) {
	if s, ok := f.subs[id]; ok {
		return *s, nil
	}
	return domain.Subscription{}, shared.ErrNotFound
}

func (f *fakeStore) GetSub(_ context.Context, id uuid.UUID) (domain.Detail, error) {
	if s, ok := f.subs[id]; ok {
		return f.detail(s), nil
	}
	return domain.Detail{}, shared.ErrNotFound
}

func (f *fakeStore) Running(_ context.Context, user uuid.UUID, a domain.Audience, now time.Time) (domain.Detail, error) {
	for _, s := range f.subs {
		if s.UserID == user && s.Audience == a && s.RunningAt(now) {
			return f.detail(s), nil
		}
	}
	return domain.Detail{}, shared.ErrNotFound
}

func (f *fakeStore) Pending(_ context.Context, user uuid.UUID, a domain.Audience) (domain.Detail, error) {
	for _, s := range f.subs {
		if s.UserID == user && s.Audience == a && s.Status == domain.StatusPending {
			return f.detail(s), nil
		}
	}
	return domain.Detail{}, shared.ErrNotFound
}

func (f *fakeStore) CancelPending(_ context.Context, user uuid.UUID, a domain.Audience, now time.Time) error {
	for _, s := range f.subs {
		if s.UserID == user && s.Audience == a && s.Status == domain.StatusPending {
			s.Status, s.CanceledAt = domain.StatusCanceled, &now
		}
	}
	return nil
}

func (f *fakeStore) EndOpen(_ context.Context, user uuid.UUID, a *domain.Audience, except uuid.UUID, now time.Time) (int, error) {
	ended := 0
	for _, s := range f.subs {
		open := s.Status == domain.StatusActive || s.Status == domain.StatusPending
		if s.UserID == user && open && s.ID != except && (a == nil || s.Audience == *a) {
			s.Status, s.CanceledAt, s.EndsAt = domain.StatusCanceled, &now, &now
			ended++
		}
	}
	return ended, nil
}

func (f *fakeStore) ActivateSub(_ context.Context, id uuid.UUID, starts time.Time, ends *time.Time, by uuid.UUID) error {
	s := f.subs[id]
	s.Status, s.StartsAt, s.EndsAt, s.GrantedByID = domain.StatusActive, starts, ends, &by
	return nil
}

func (f *fakeStore) SearchSubs(_ context.Context, q domain.SubscriptionQuery) ([]domain.Detail, int, error) {
	var out []domain.Detail
	for i := len(f.subOrder) - 1; i >= 0; i-- {
		s := f.subs[f.subOrder[i]]
		if q.Status != nil && s.Status != *q.Status || q.Audience != nil && s.Audience != *q.Audience || q.UserID != nil && s.UserID != *q.UserID {
			continue
		}
		out = append(out, f.detail(s))
	}
	total := len(out)
	if q.Limit > 0 && q.Offset < len(out) {
		out = out[q.Offset:min(len(out), q.Offset+q.Limit)]
	}
	return out, total, nil
}

func (f *fakeStore) ExpireDueSubs(_ context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, id := range f.subOrder {
		s := f.subs[id]
		if s.Status == domain.StatusActive && s.EndsAt != nil && !s.EndsAt.After(now) && len(ids) < limit {
			s.Status = domain.StatusExpired
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// The SubscriptionRepository the use cases see: a thin wrapper that renames the clashing methods of fakeStore.
type fakeSubs struct{ f *fakeStore }

func (s fakeSubs) Insert(ctx context.Context, x domain.Subscription) (uuid.UUID, error) {
	return s.f.InsertSub(ctx, x)
}
func (s fakeSubs) Get(ctx context.Context, id uuid.UUID) (domain.Detail, error) {
	return s.f.GetSub(ctx, id)
}
func (s fakeSubs) Lock(ctx context.Context, id uuid.UUID) (domain.Subscription, error) {
	return s.f.Lock2(ctx, id)
}
func (s fakeSubs) Running(ctx context.Context, u uuid.UUID, a domain.Audience, now time.Time) (domain.Detail, error) {
	return s.f.Running(ctx, u, a, now)
}
func (s fakeSubs) RunningPriorities(_ context.Context, ids []uuid.UUID, a domain.Audience, now time.Time) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	for _, id := range ids {
		if d, err := s.f.Running(context.Background(), id, a, now); err == nil {
			out[id] = d.Plan.SearchPriority
		}
	}
	return out, nil
}
func (s fakeSubs) Pending(ctx context.Context, u uuid.UUID, a domain.Audience) (domain.Detail, error) {
	return s.f.Pending(ctx, u, a)
}
func (s fakeSubs) CancelPending(ctx context.Context, u uuid.UUID, a domain.Audience, now time.Time) error {
	return s.f.CancelPending(ctx, u, a, now)
}
func (s fakeSubs) EndOpen(ctx context.Context, u uuid.UUID, a *domain.Audience, except uuid.UUID, now time.Time) (int, error) {
	return s.f.EndOpen(ctx, u, a, except, now)
}
func (s fakeSubs) Activate(ctx context.Context, id uuid.UUID, starts time.Time, ends *time.Time, by uuid.UUID) error {
	return s.f.ActivateSub(ctx, id, starts, ends, by)
}
func (s fakeSubs) Search(ctx context.Context, q domain.SubscriptionQuery) ([]domain.Detail, int, error) {
	return s.f.SearchSubs(ctx, q)
}
func (s fakeSubs) ExpireDue(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	return s.f.ExpireDueSubs(ctx, now, limit)
}

// recorder keeps the notifications and audit events the use cases sent.
type recorder struct {
	notices []Notification
	events  []AuditEvent
	failing bool
}

func (r *recorder) Notify(_ context.Context, m Notification) error {
	r.notices = append(r.notices, m)
	if r.failing {
		return context.DeadlineExceeded
	}
	return nil
}

func (r *recorder) Record(_ context.Context, e AuditEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) actions() []string {
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.Action
	}
	return out
}

// world builds every use case on one store.
type world struct {
	store *fakeStore
	rec   *recorder
	subs  *Subscriptions
	plans *Plans
	limit *Limits
	seed  *Seeder
}

var epoch = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func newWorld() *world {
	store := newStore(epoch)
	rec := &recorder{}
	deps := Deps{
		Tx: store, Plans: store, Subs: fakeSubs{store}, Accounts: store, Usage: store, Notifier: rec, Audit: rec, Clock: store,
	}
	return &world{store: store, rec: rec, subs: NewSubscriptions(deps), plans: NewPlans(deps), limit: NewLimits(deps), seed: NewSeeder(deps)}
}

func sortedCodes(ps []domain.Plan) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	sort.Strings(out)
	return out
}
