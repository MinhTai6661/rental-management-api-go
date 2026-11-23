//go:build integration

package planpg

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

type repos struct {
	pool     *pgxpool.Pool
	plans    *PlanRepository
	subs     *SubscriptionRepository
	accounts *Accounts
	usage    *Usage
	tx       *postgres.TxManager
	landlord uuid.UUID
	tenant   uuid.UUID
}

func newRepos(t *testing.T) *repos {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	for _, q := range []string{
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('landlord', 3, false), ('tenant', 5, false)`,
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
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
		for _, r := range roles {
			if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	return &repos{
		pool: pool, plans: NewPlanRepository(pool), subs: NewSubscriptionRepository(pool), accounts: NewAccounts(pool),
		usage: NewUsage(pool), tx: postgres.NewTxManager(pool),
		landlord: user("l@example.vn", "Landlord", "landlord", "tenant"), tenant: user("t@example.vn", "Tenant", "tenant"),
	}
}

func n(v int) *int { return &v }

func (r *repos) plan(t *testing.T, code string, a domain.Audience, price int64, mod func(*domain.Plan)) domain.Plan {
	t.Helper()
	p := domain.Plan{
		Code: code, Name: code, Audience: a, PriceVND: price, Interval: domain.IntervalMonth, Features: []domain.Feature{}, IsActive: true,
	}
	if mod != nil {
		mod(&p)
	}
	id, err := r.plans.Insert(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.plans.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r *repos) sub(t *testing.T, user uuid.UUID, p domain.Plan, status domain.Status, starts time.Time, ends *time.Time) uuid.UUID {
	t.Helper()
	id, err := r.subs.Insert(context.Background(), domain.Subscription{
		UserID: user, PlanID: p.ID, Audience: p.Audience, Status: status, StartsAt: starts, EndsAt: ends,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var base = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func TestPlanRoundTripKeepsLimitsFeaturesAndNulls(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	desc := "For growing houses"
	p := r.plan(t, "landlord_pro", domain.AudienceLandlord, 199_000, func(p *domain.Plan) {
		p.Description = &desc
		p.Limits = domain.Limits{MaxRooms: n(100), MaxBuildings: nil, MaxImagesPerRoom: n(10), MaxActiveContracts: n(0)}
		p.Features = []domain.Feature{domain.FeatureAutoInvoicing, domain.FeatureVIPBadge}
		p.SearchPriority, p.DisplayOrder = 10, 2
	})
	if p.ID == uuid.Nil || p.CreatedAt.IsZero() || p.Description == nil || *p.Description != desc || p.PriceVND != 199_000 || p.Interval != domain.IntervalMonth {
		t.Fatalf("stored = %+v", p)
	}
	if *p.Limits.MaxRooms != 100 || p.Limits.MaxBuildings != nil || *p.Limits.MaxActiveContracts != 0 || p.Limits.MaxPendingRegistrations != nil {
		t.Errorf("limits (nil = unlimited, 0 stays 0): %+v", p.Limits)
	}
	if !slices.Equal(p.Features, []domain.Feature{domain.FeatureAutoInvoicing, domain.FeatureVIPBadge}) || p.SearchPriority != 10 {
		t.Errorf("features %v priority %d", p.Features, p.SearchPriority)
	}

	p.Name, p.Description, p.Limits.MaxRooms, p.Limits.MaxBuildings, p.Features, p.IsActive = "Pro+", nil, nil, n(5), []domain.Feature{}, false
	if err := r.plans.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := r.plans.GetByCode(ctx, "landlord_pro")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Pro+" || got.Description != nil || got.Limits.MaxRooms != nil || *got.Limits.MaxBuildings != 5 || len(got.Features) != 0 || got.IsActive {
		t.Errorf("updated = %+v", got)
	}
	if _, err := r.plans.Get(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := r.plans.GetByCode(ctx, "nope"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
}

func TestPlanCodeAndDefaultAreUnique(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	free := r.plan(t, "landlord_free", domain.AudienceLandlord, 0, func(p *domain.Plan) { p.IsDefault = true })

	_, err := r.plans.Insert(ctx, domain.Plan{Code: "landlord_free", Name: "x", Audience: domain.AudienceTenant, Interval: domain.IntervalMonth, Features: []domain.Feature{}})
	var ce *shared.ConstraintError
	if !errors.As(err, &ce) || ce.Kind != shared.Unique || ce.Constraint != "plans_code_key" {
		t.Fatalf("duplicate code: %v", err)
	}
	_, err = r.plans.Insert(ctx, domain.Plan{Code: "second_default", Name: "x", Audience: domain.AudienceLandlord, Interval: domain.IntervalMonth, Features: []domain.Feature{}, IsDefault: true})
	if !errors.As(err, &ce) || ce.Constraint != "plans_audience_default_key" {
		t.Fatalf("two defaults of one audience: %v", err)
	}
	// Another audience may have its own default, and clearing first lets a new default in.
	r.plan(t, "tenant_free", domain.AudienceTenant, 0, func(p *domain.Plan) { p.IsDefault = true })
	if err := r.plans.ClearDefault(ctx, domain.AudienceLandlord, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	r.plan(t, "second_default", domain.AudienceLandlord, 0, func(p *domain.Plan) { p.IsDefault = true })
	if _, err := r.plans.Default(ctx, domain.AudienceLandlord); err != nil {
		t.Fatal(err)
	}
	// ClearDefault spares the plan named.
	if err := r.plans.ClearDefault(ctx, domain.AudienceTenant, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.plans.Default(ctx, domain.AudienceTenant); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("cleared: %v", err)
	}
	_ = free
}

func TestDefaultIgnoresAnInactivePlan(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	r.plan(t, "old_default", domain.AudienceTenant, 0, func(p *domain.Plan) { p.IsDefault, p.IsActive = true, false })
	if _, err := r.plans.Default(ctx, domain.AudienceTenant); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("an inactive default is no default: %v", err)
	}
}

func TestSearchPlansFiltersOrdersAndPages(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, func(p *domain.Plan) { p.DisplayOrder = 3 })
	r.plan(t, "l_free", domain.AudienceLandlord, 0, func(p *domain.Plan) { p.DisplayOrder = 1 })
	r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, func(p *domain.Plan) { p.DisplayOrder = 2 })
	r.plan(t, "l_old", domain.AudienceLandlord, 0, func(p *domain.Plan) { p.IsActive = false })
	r.plan(t, "t_plus", domain.AudienceTenant, 49_000, nil)

	landlord := domain.AudienceLandlord
	got, total, err := r.plans.Search(ctx, usecaseQuery(&landlord, true, 10, 0))
	if err != nil || total != 3 {
		t.Fatalf("active landlord plans: %d, %v", total, err)
	}
	codes := func(ps []domain.Plan) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.Code
		}
		return out
	}
	if !slices.Equal(codes(got), []string{"l_free", "l_pro", "l_vip"}) {
		t.Errorf("display order: %v", codes(got))
	}
	all, total, _ := r.plans.Search(ctx, usecaseQuery(nil, false, 100, 0))
	if total != 5 || len(all) != 5 {
		t.Errorf("the whole catalog: %d", total)
	}
	page, total, _ := r.plans.Search(ctx, usecaseQuery(&landlord, true, 2, 2))
	if total != 3 || len(page) != 1 || page[0].Code != "l_vip" {
		t.Errorf("second page: %v of %d", codes(page), total)
	}
}

func TestOneActiveAndOnePendingSubscriptionPerUserAndAudience(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, nil)
	vip := r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, nil)
	plus := r.plan(t, "t_plus", domain.AudienceTenant, 49_000, nil)

	r.sub(t, r.landlord, pro, domain.StatusActive, base, nil)
	_, err := r.subs.Insert(ctx, domain.Subscription{UserID: r.landlord, PlanID: vip.ID, Audience: vip.Audience, Status: domain.StatusActive, StartsAt: base})
	if !shared.IsUnique(err) {
		t.Fatalf("a second active plan of one audience: %v", err)
	}
	// The tenant side of the same user is separate; so is a pending request; canceled ones pile up freely.
	r.sub(t, r.landlord, plus, domain.StatusActive, base, nil)
	r.sub(t, r.landlord, vip, domain.StatusPending, base, nil)
	if _, err := r.subs.Insert(ctx, domain.Subscription{UserID: r.landlord, PlanID: pro.ID, Audience: pro.Audience, Status: domain.StatusPending, StartsAt: base}); !shared.IsUnique(err) {
		t.Fatalf("a second pending request: %v", err)
	}
	r.sub(t, r.landlord, pro, domain.StatusCanceled, base, nil)
	r.sub(t, r.landlord, pro, domain.StatusCanceled, base, nil)
}

func TestRunningPendingAndTheDetailOfASubscription(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, func(p *domain.Plan) { p.Limits.MaxRooms = n(50) })
	future := base.Add(time.Hour)
	id := r.sub(t, r.landlord, pro, domain.StatusActive, base.Add(-time.Hour), &future)

	got, err := r.subs.Running(ctx, r.landlord, domain.AudienceLandlord, base)
	if err != nil || got.ID != id || got.Plan.Code != "l_pro" || *got.Plan.Limits.MaxRooms != 50 || got.EndsAt == nil {
		t.Fatalf("running = %+v, %v", got, err)
	}
	for name, at := range map[string]time.Time{"after the end": future, "before the start": base.Add(-2 * time.Hour)} {
		if _, err := r.subs.Running(ctx, r.landlord, domain.AudienceLandlord, at); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.subs.Running(ctx, r.landlord, domain.AudienceTenant, base); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("the tenant side has none: %v", err)
	}

	vip := r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, nil)
	pid := r.sub(t, r.landlord, vip, domain.StatusPending, base, nil)
	pending, err := r.subs.Pending(ctx, r.landlord, domain.AudienceLandlord)
	if err != nil || pending.ID != pid || pending.Plan.Code != "l_vip" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	detail, err := r.subs.Get(ctx, pid)
	if err != nil || detail.UserName != "Landlord" || detail.UserEmail == nil || *detail.UserEmail != "l@example.vn" || detail.Status != domain.StatusPending {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if _, err := r.subs.Get(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestEndOpenCancelsActiveAndPendingBySideAndSparesOne(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, nil)
	vip := r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, nil)
	plus := r.plan(t, "t_plus", domain.AudienceTenant, 49_000, nil)
	active := r.sub(t, r.landlord, pro, domain.StatusActive, base, nil)
	pending := r.sub(t, r.landlord, vip, domain.StatusPending, base, nil)
	tenantSide := r.sub(t, r.landlord, plus, domain.StatusActive, base, nil)
	other := r.sub(t, r.tenant, plus, domain.StatusActive, base, nil)

	landlord := domain.AudienceLandlord
	ended, err := r.subs.EndOpen(ctx, r.landlord, &landlord, pending, base)
	if err != nil || ended != 1 {
		t.Fatalf("ended %d, %v", ended, err)
	}
	status := func(id uuid.UUID) domain.Status { d, _ := r.subs.Get(ctx, id); return d.Status }
	if status(active) != domain.StatusCanceled || status(pending) != domain.StatusPending || status(tenantSide) != domain.StatusActive || status(other) != domain.StatusActive {
		t.Fatal("only the active landlord subscription ended")
	}
	d, _ := r.subs.Get(ctx, active)
	if d.CanceledAt == nil || d.EndsAt == nil || !d.EndsAt.Equal(base) {
		t.Errorf("canceled at the moment: %+v", d.Subscription)
	}
	if ended, _ := r.subs.EndOpen(ctx, r.landlord, nil, uuid.Nil, base); ended != 2 {
		t.Errorf("every side: ended %d", ended)
	}
	if ended, _ := r.subs.EndOpen(ctx, r.landlord, nil, uuid.Nil, base); ended != 0 {
		t.Errorf("nothing left: ended %d", ended)
	}
	if status(other) != domain.StatusActive {
		t.Error("another user is untouched")
	}
}

func TestCancelPendingAndActivate(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	vip := r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, nil)
	admin := r.tenant
	id := r.sub(t, r.landlord, vip, domain.StatusPending, base, nil)

	end := base.AddDate(0, 1, 0)
	later := base.Add(48 * time.Hour)
	if err := r.subs.Activate(ctx, id, later, &end, admin); err != nil {
		t.Fatal(err)
	}
	got, _ := r.subs.Get(ctx, id)
	if got.Status != domain.StatusActive || !got.StartsAt.Equal(later) || got.EndsAt == nil || !got.EndsAt.Equal(end) || got.GrantedByID == nil || *got.GrantedByID != admin {
		t.Fatalf("activated = %+v", got.Subscription)
	}
	// Activating again changes nothing: only a pending row can be activated.
	if err := r.subs.Activate(ctx, id, base, nil, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.subs.Get(ctx, id); !again.StartsAt.Equal(later) {
		t.Error("an active subscription is not activated again")
	}

	other := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, nil)
	pending := r.sub(t, r.tenant2(t), other, domain.StatusPending, base, nil)
	owner, _ := r.subs.Get(ctx, pending)
	if err := r.subs.CancelPending(ctx, owner.UserID, domain.AudienceLandlord, base); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.subs.Get(ctx, pending); d.Status != domain.StatusCanceled || d.CanceledAt == nil {
		t.Errorf("canceled: %+v", d.Subscription)
	}
}

func (r *repos) tenant2(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.pool.QueryRow(context.Background(), `INSERT INTO users (email, full_name) VALUES ('t2@example.vn', 'Tenant Two') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSearchSubscriptionsFiltersAndPagesNewestFirst(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, nil)
	plus := r.plan(t, "t_plus", domain.AudienceTenant, 49_000, nil)
	first := r.sub(t, r.landlord, pro, domain.StatusPending, base, nil)
	second := r.sub(t, r.tenant, plus, domain.StatusPending, base, nil)
	r.sub(t, r.landlord, plus, domain.StatusActive, base, nil)

	pending := domain.StatusPending
	got, total, err := r.subs.Search(ctx, subQuery(&pending, nil, nil, 10, 0))
	if err != nil || total != 2 || len(got) != 2 || got[0].ID != second || got[1].ID != first {
		t.Fatalf("pending, newest first: %d of %d, %v", len(got), total, err)
	}
	if got[0].UserName != "Tenant" || got[0].Plan.Code != "t_plus" {
		t.Errorf("detail = %+v", got[0])
	}
	page, total, _ := r.subs.Search(ctx, subQuery(&pending, nil, nil, 1, 1))
	if total != 2 || len(page) != 1 || page[0].ID != first {
		t.Errorf("page 2: %d of %d", len(page), total)
	}
	aud, user := domain.AudienceTenant, r.landlord
	byUser, total, _ := r.subs.Search(ctx, subQuery(nil, &aud, &user, 10, 0))
	if total != 1 || byUser[0].Plan.Code != "t_plus" || byUser[0].Status != domain.StatusActive {
		t.Errorf("by audience and user: %+v", byUser)
	}
}

func TestExpireDueMarksLapsedActiveSubscriptionsInBatches(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	plus := r.plan(t, "t_plus", domain.AudienceTenant, 49_000, nil)
	free := r.plan(t, "t_free", domain.AudienceTenant, 0, nil)
	past, future := base.Add(-time.Hour), base.Add(time.Hour)
	lapsed := r.sub(t, r.tenant, plus, domain.StatusActive, base.Add(-2*time.Hour), &past)
	live := r.sub(t, r.landlord, plus, domain.StatusActive, base.Add(-2*time.Hour), &future)
	r.sub(t, r.tenant2(t), free, domain.StatusActive, base.Add(-2*time.Hour), nil)

	ids, err := r.subs.ExpireDue(ctx, base, 10)
	if err != nil || len(ids) != 1 || ids[0] != lapsed {
		t.Fatalf("expired %v, %v", ids, err)
	}
	if d, _ := r.subs.Get(ctx, live); d.Status != domain.StatusActive {
		t.Error("a subscription not yet over stays active")
	}
	if ids, _ := r.subs.ExpireDue(ctx, base, 10); len(ids) != 0 {
		t.Errorf("a second run finds nothing: %v", ids)
	}
	// The limit bounds a batch.
	if ids, _ := r.subs.ExpireDue(ctx, future.Add(time.Minute), 1); len(ids) != 1 {
		t.Errorf("batch of one: %v", ids)
	}
}

func TestAccountsLockReadsRolesAndTheUsageCountsWhatThePlanLimits(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)

	a, err := r.accounts.Lock(ctx, r.landlord)
	if err != nil || a.Name != "Landlord" || !slices.Equal(a.Roles, []string{"landlord", "tenant"}) {
		t.Fatalf("account = %+v, %v", a, err)
	}
	if _, err := r.accounts.Lock(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}

	// Two rooms (one deleted), a building, a running contract with a tenant, a blank one, and a waiting registration.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := r.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	room := func(name string, deleted bool) (id uuid.UUID) {
		t.Helper()
		var del *time.Time
		if deleted {
			now := time.Now()
			del = &now
		}
		err := r.pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, deleted_at)
			VALUES ($1, $2, 4, 'x', 1000000, 1, $3) RETURNING id`, r.landlord, name, del).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	live, other, _ := room("A1", false), room("A2", false), room("A3", true)
	exec(`INSERT INTO buildings (landlord_id, name, ward_code, address_detail, display_order) VALUES ($1, 'B', 4, 'x', 1)`, r.landlord)
	contract := func(roomID uuid.UUID, tenant *uuid.UUID, status string) {
		exec(`INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
			VALUES ($1, $2, $3, $4, 1000000, current_date, now(), 1)`, roomID, r.landlord, tenant, status)
	}
	contract(live, &r.tenant, "active")
	contract(other, nil, "active")
	other2 := room("A4", false)
	contract(other2, &r.tenant, "pending")

	u, err := r.usage.Landlord(ctx, r.landlord)
	if err != nil || u.Rooms != 3 || u.Buildings != 1 || u.ActiveContracts != 1 {
		t.Fatalf("landlord usage = %+v, %v (the deleted room and the blank contract do not count)", u, err)
	}
	if pending, err := r.usage.PendingRegistrations(ctx, r.tenant); err != nil || pending != 1 {
		t.Errorf("pending registrations = %d, %v", pending, err)
	}
}

func TestTheTransactionOfTheContextRollsBackSubscriptions(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, nil)
	boom := errors.New("boom")
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := r.subs.Insert(ctx, domain.Subscription{UserID: r.landlord, PlanID: pro.ID, Audience: pro.Audience, Status: domain.StatusActive, StartsAt: base}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := r.subs.Running(ctx, r.landlord, domain.AudienceLandlord, base.Add(time.Minute)); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("the rolled back subscription is gone: %v", err)
	}
}

func TestRunningPrioritiesAnswerForSeveralUsersOnOneSide(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	pro := r.plan(t, "l_pro", domain.AudienceLandlord, 199_000, func(p *domain.Plan) { p.SearchPriority = 10 })
	vip := r.plan(t, "l_vip", domain.AudienceLandlord, 499_000, func(p *domain.Plan) { p.SearchPriority = 100 })
	plus := r.plan(t, "t_plus", domain.AudienceTenant, 49_000, func(p *domain.Plan) { p.SearchPriority = 7 })
	lapsed, past := r.tenant, base.Add(-time.Minute)
	r.sub(t, r.landlord, pro, domain.StatusActive, base.Add(-time.Hour), nil)
	r.sub(t, r.landlord, plus, domain.StatusActive, base.Add(-time.Hour), nil)
	r.sub(t, lapsed, vip, domain.StatusActive, base.Add(-time.Hour), &past)

	got, err := r.subs.RunningPriorities(ctx, []uuid.UUID{r.landlord, lapsed, uuid.New()}, domain.AudienceLandlord, base)
	if err != nil || len(got) != 1 || got[r.landlord] != 10 {
		t.Errorf("landlord side: %v, %v (a lapsed plan and a stranger are left out; the tenant plan is another side)", got, err)
	}
	got, err = r.subs.RunningPriorities(ctx, []uuid.UUID{r.landlord}, domain.AudienceTenant, base)
	if err != nil || len(got) != 1 || got[r.landlord] != 7 {
		t.Errorf("tenant side: %v, %v", got, err)
	}
	if got, err := r.subs.RunningPriorities(ctx, nil, domain.AudienceLandlord, base); err != nil || len(got) != 0 {
		t.Errorf("nobody: %v, %v", got, err)
	}
}
