//go:build integration

package notificationpg

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// audienceWorld is the fixture of the audience truth table: a landlord with rooms in ward 10, a tenant renting in
// province 2, a tenant on a paid plan, a new account, one with a recent session, an inactive one, and two staff.
type audienceWorld struct {
	pool                                               *pgxpool.Pool
	repo                                               *AnnouncementRepository
	super, admin, landlord, renter, pro, fresh, active uuid.UUID
	plain, inactive                                    uuid.UUID
	now                                                time.Time
}

func (w *audienceWorld) all() []uuid.UUID {
	return []uuid.UUID{w.super, w.admin, w.landlord, w.renter, w.pro, w.fresh, w.active, w.plain, w.inactive}
}

func newAudienceWorld(t *testing.T) *audienceWorld {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'A', 'city', 'a'), (2, 'B', 'city', 'b')`)
	exec(`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (10, 1, 'W10', 'ward', 'w10'), (20, 2, 'W20', 'ward', 'w20'), (30, 2, 'W30', 'ward', 'w30')`)
	exec(`INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true), ('landlord', 3, false), ('manager', 4, false), ('tenant', 5, false)`)
	w := &audienceWorld{pool: pool, repo: NewAnnouncementRepository(pool), now: time.Now()}
	user := func(email string, ward *int, status string, ageDays int, roles ...string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name, ward_code, status, created_at) VALUES ($1, 'N', $2, $3, now() - make_interval(days => $4)) RETURNING id`,
			email, ward, status, ageDays).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, r := range roles {
			exec(`INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r)
		}
		return id
	}
	ten := 10
	w.super = user("s@x.vn", nil, "active", 100, "super_admin")
	w.admin = user("a@x.vn", nil, "active", 100, "admin")
	w.landlord = user("l@x.vn", nil, "active", 100, "landlord", "tenant")
	w.renter = user("r@x.vn", nil, "active", 100, "tenant")
	w.pro = user("p@x.vn", nil, "active", 100, "tenant")
	w.fresh = user("f@x.vn", nil, "active", 3, "tenant")
	w.active = user("c@x.vn", nil, "active", 100, "tenant")
	w.plain = user("u@x.vn", &ten, "active", 100, "tenant")
	w.inactive = user("i@x.vn", nil, "inactive", 100, "tenant")

	// The landlord has a room in ward 10 and one in ward 30 that the renter rents (a running confirmed contract).
	var roomA, roomB uuid.UUID
	for i, ward := range []int{10, 30} {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order) VALUES ($1, $2, $3, 'x', 1000, $4) RETURNING id`,
			w.landlord, []string{"A", "B"}[i], ward, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			roomA = id
		} else {
			roomB = id
		}
	}
	_ = roomA
	exec(`INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order) VALUES ($1, $2, $3, 'active', 1000, current_date, now(), 0)`,
		roomB, w.landlord, w.renter)
	// A pending registration is not a tenant of the landlord.
	exec(`INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order) VALUES ($1, $2, $3, 'pending', 1000, current_date, now(), 1)`,
		roomA, w.landlord, w.plain)

	var planID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO plans (code, name, audience, price_vnd) VALUES ('pro', 'Pro', 'tenant', 1000) RETURNING id`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO subscriptions (user_id, plan_id, audience, status, starts_at) VALUES ($1, $2, 'tenant', 'active', now() - interval '1 day')`, w.pro, planID)
	sid := uuid.New()
	exec(`INSERT INTO user_sessions (id, user_id, active_role, created_at, last_used_at) VALUES ($1, $2, 'tenant', now() - interval '30 days', now() - interval '1 day')`, sid, w.active)
	return w
}

var (
	platformAdmin = func(id uuid.UUID) domain.Sender { return domain.Sender{UserID: id, Role: "admin"} }
	platformSuper = func(id uuid.UUID) domain.Sender { return domain.Sender{UserID: id, Role: "super_admin"} }
)

func day(n int) *int { return &n }

// members returns who of the fixture the filter reaches, asking the three questions the code asks (count, filter,
// delivery) and failing when they disagree.
func (w *audienceWorld) members(t *testing.T, f domain.AudienceFilter) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	f.Now = w.now
	got, err := w.repo.FilterPeople(ctx, f, w.all())
	if err != nil {
		t.Fatal(err)
	}
	count, err := w.repo.CountAudience(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(got) {
		t.Fatalf("count %d disagrees with the filter %d", count, len(got))
	}
	// Delivery goes through a real announcement.
	id, err := w.repo.Insert(ctx, domain.Announcement{
		SenderRole: "admin", Scope: domain.ScopeEveryone, Type: domain.TypeNotice, Title: domain.Texts{"vi": "t"}, Body: domain.Texts{"vi": "b"},
		SendNotification: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := w.repo.DeliverNotifications(ctx, id, f, map[string]string{"x": "y"}, domain.AudienceNone)
	if err != nil {
		t.Fatal(err)
	}
	if len(delivered) != len(got) {
		t.Fatalf("delivery %d disagrees with the filter %d", len(delivered), len(got))
	}
	for _, u := range delivered {
		if !slices.Contains(got, u) {
			t.Fatalf("delivered to somebody outside the audience: %s", u)
		}
	}
	return got
}

func sameUsers(t *testing.T, name string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d people, want %d", name, len(got), len(want))
		return
	}
	for _, u := range want {
		if !slices.Contains(got, u) {
			t.Errorf("%s: %s is missing", name, u)
		}
	}
}

func (w *audienceWorld) resolve(t *testing.T, scope domain.Scope, s domain.Sender, c domain.Criteria, picked ...uuid.UUID) domain.AudienceFilter {
	t.Helper()
	f, err := domain.ResolveFilter(scope, s, c, picked, w.now)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAudienceTruthTable(t *testing.T) {
	w := newAudienceWorld(t)
	adm := platformAdmin(w.admin)
	sup := platformSuper(w.super)
	landlord := domain.Sender{UserID: w.landlord, Role: "landlord"}
	nonStaff := []uuid.UUID{w.landlord, w.renter, w.pro, w.fresh, w.active, w.plain}
	crit := func(c domain.Criteria) domain.Criteria { return c }

	cases := []struct {
		name string
		f    domain.AudienceFilter
		want []uuid.UUID
	}{
		{"everyone: active non-staff", w.resolve(t, domain.ScopeEveryone, adm, domain.Criteria{}), nonStaff},
		{"everyone by a super admin reaches no staff", w.resolve(t, domain.ScopeEveryone, sup, domain.Criteria{}), nonStaff},
		{"tenants", w.resolve(t, domain.ScopeTenants, adm, domain.Criteria{}), nonStaff},
		{"landlords", w.resolve(t, domain.ScopeLandlords, adm, domain.Criteria{}), []uuid.UUID{w.landlord}},
		{"custom role landlord", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{Roles: []string{"landlord"}})), []uuid.UUID{w.landlord}},
		{"custom: a super admin names admins", w.resolve(t, domain.ScopeCustom, sup, crit(domain.Criteria{Roles: []string{"admin"}})), []uuid.UUID{w.admin}},
		{"custom: an admin naming admins reaches nobody", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{Roles: []string{"admin"}})), nil},
		{"province 2: the renter (a room they rent is there) and the landlord (a room is there)", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{ProvinceCodes: []int{2}})), []uuid.UUID{w.renter, w.landlord}},
		{"ward 10: the landlord's room and a user's own ward", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{WardCodes: []int{10}})), []uuid.UUID{w.landlord, w.plain}},
		{"plan pro", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{PlanCodes: []string{"pro"}})), []uuid.UUID{w.pro}},
		{"no plan", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{PlanCodes: []string{"none"}})), []uuid.UUID{w.landlord, w.renter, w.fresh, w.active, w.plain}},
		{"pro or no plan", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{PlanCodes: []string{"pro", "none"}})), nonStaff},
		{"registered within 7 days", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{RegisteredWithinDays: day(7)})), []uuid.UUID{w.fresh}},
		{"registered at least 30 days ago", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{RegisteredAtLeastDays: day(30)})), []uuid.UUID{w.landlord, w.renter, w.pro, w.active, w.plain}},
		{"active within 7 days", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{ActiveWithinDays: day(7)})), []uuid.UUID{w.active}},
		{"user ids", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{UserIDs: []uuid.UUID{w.plain, w.inactive}})), []uuid.UUID{w.plain}},
		{"criteria are ANDed", w.resolve(t, domain.ScopeCustom, adm, crit(domain.Criteria{Roles: []string{"tenant"}, ProvinceCodes: []int{2}})), []uuid.UUID{w.renter, w.landlord}},
		{"landlord: my tenants (the pending registration is not one)", w.resolve(t, domain.ScopeMyTenants, landlord, domain.Criteria{}), []uuid.UUID{w.renter}},
		{"landlord: my people", w.resolve(t, domain.ScopeMyPeople, landlord, domain.Criteria{}), []uuid.UUID{w.renter}},
		{"landlord: picked ones that are theirs", w.resolve(t, domain.ScopeSelectedPeople, landlord, domain.Criteria{}, w.renter, w.plain), []uuid.UUID{w.renter}},
		{"landlord: nobody picked of theirs", w.resolve(t, domain.ScopeSelectedPeople, landlord, domain.Criteria{}, w.plain), nil},
	}
	for _, c := range cases {
		sameUsers(t, c.name, w.members(t, c.f), c.want...)
	}
}

func TestDeliveryIsExactlyOncePerPersonAndRecallRemovesIt(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	tx := postgres.NewTxManager(w.pool)
	id, err := w.repo.Insert(ctx, domain.Announcement{
		SenderRole: "admin", Scope: domain.ScopeTenants, Type: domain.TypeNotice, Title: domain.Texts{"vi": "t"}, Body: domain.Texts{"vi": "b"},
		SendNotification: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.repo.SetPublished(ctx, id, &w.now); err != nil {
		t.Fatal(err)
	}
	f, err := domain.ResolveFilter(domain.ScopeTenants, platformAdmin(w.admin), domain.Criteria{}, nil, w.now)
	if err != nil {
		t.Fatal(err)
	}

	// Two deliveries race: exactly one claims.
	var wg sync.WaitGroup
	var mu sync.Mutex
	claims, delivered := 0, 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tx.WithinTx(ctx, func(ctx context.Context) error {
				ok, err := w.repo.ClaimDelivery(ctx, id, w.now)
				if err != nil || !ok {
					return err
				}
				ids, err := w.repo.DeliverNotifications(ctx, id, f, map[string]string{"announcement_id": id.String()}, domain.AudienceTenant)
				mu.Lock()
				claims++
				delivered += len(ids)
				mu.Unlock()
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if claims != 1 || delivered != 6 {
		t.Fatalf("claims %d, delivered %d", claims, delivered)
	}
	var rows int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE announcement_id = $1`, id).Scan(&rows); err != nil || rows != 6 {
		t.Fatalf("rows %d %v", rows, err)
	}
	// A repeat of the very same statement adds nothing (the unique index and ON CONFLICT).
	if again, err := w.repo.DeliverNotifications(ctx, id, f, nil, domain.AudienceTenant); err != nil || len(again) != 0 {
		t.Errorf("repeat: %v %v", again, err)
	}
	var kind, audience string
	if err := w.pool.QueryRow(ctx, `SELECT kind, audience_role FROM notifications WHERE announcement_id = $1 LIMIT 1`, id).Scan(&kind, &audience); err != nil || kind != "announcement" || audience != "tenant" {
		t.Errorf("kind %q audience %q %v", kind, audience, err)
	}

	// Read counts, then recall deletes every notification, once.
	if _, err := w.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE announcement_id = $1 AND user_id = ANY($2)`, id, []uuid.UUID{w.pro, w.plain}); err != nil {
		t.Fatal(err)
	}
	d, err := w.repo.Get(ctx, id)
	if err != nil || d.ReadCount != 2 {
		t.Fatalf("read count %d %v", d.ReadCount, err)
	}
	n, err := w.repo.SetRecalled(ctx, id, w.now)
	if err != nil || n != 6 {
		t.Fatalf("recall deleted %d %v", n, err)
	}
	if _, err := w.repo.SetRecalled(ctx, id, w.now); !errors.Is(err, domain.ErrRecalled) {
		t.Errorf("recall again: %v", err)
	}
	if ok, err := w.repo.ClaimDelivery(ctx, id, w.now); err != nil || ok {
		t.Errorf("a recalled message cannot be claimed: %v %v", ok, err)
	}
}

func TestAnnouncementRoundTripListAndDue(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	days := 14
	before := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	later := w.now.Add(time.Hour)
	in := domain.Announcement{
		CreatedByID: &w.admin, SenderRole: "admin", Scope: domain.ScopeCustom,
		Criteria: domain.Criteria{Roles: []string{"tenant"}, ProvinceCodes: []int{1, 2}, RegisteredWithinDays: &days, RegisteredBefore: &before, UserIDs: []uuid.UUID{w.plain}},
		Type:     domain.TypeWelcome, Title: domain.Texts{"vi": "Chào", "en": "Hi"}, Body: domain.Texts{"vi": "Nội dung"},
		SendNotification: true, ShowPopup: true, StartsAt: &later, Frequency: domain.FrequencyEveryDays, FrequencyDays: &days,
		MaxPerPeriod: &days, Priority: 3, PopupSize: domain.SizeLarge, CTALabel: domain.Texts{"vi": "Mở"}, CTAURL: "/find-rooms", Revision: 1,
	}
	id, err := w.repo.Insert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != domain.ScopeCustom || got.Type != domain.TypeWelcome || got.Title["en"] != "Hi" || got.CTAURL != "/find-rooms" || got.CTALabel["vi"] != "Mở" ||
		got.Priority != 3 || got.PopupSize != domain.SizeLarge || got.FrequencyDays == nil || *got.FrequencyDays != 14 ||
		got.Criteria.RegisteredBefore == nil || !got.Criteria.RegisteredBefore.Equal(before) || len(got.Criteria.ProvinceCodes) != 2 ||
		got.CreatedBy == nil || got.CreatedBy.ID != w.admin || got.PublishedAt != nil {
		t.Fatalf("round trip: %+v", got)
	}

	// Update changes the content and the revision, not the dates.
	got.Title = domain.Texts{"vi": "Mới"}
	got.Revision = 2
	got.CTALabel, got.CTAURL = nil, ""
	got.Criteria = domain.Criteria{}
	got.Scope = domain.ScopeTenants
	if err := w.repo.Update(ctx, got.Announcement); err != nil {
		t.Fatal(err)
	}
	if err := w.repo.ReplaceRecipients(ctx, id, []uuid.UUID{w.plain, w.renter}); err != nil {
		t.Fatal(err)
	}
	upd, _ := w.repo.Get(ctx, id)
	if upd.Revision != 2 || upd.Title["vi"] != "Mới" || upd.CTAURL != "" || upd.CTALabel != nil || !upd.Criteria.IsEmpty() || len(upd.People) != 2 {
		t.Errorf("after update: %+v", upd)
	}
	if err := w.repo.Update(ctx, domain.Announcement{ID: uuid.New(), SenderRole: "admin", Scope: domain.ScopeTenants, Type: domain.TypeNotice, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1}); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("update of a missing message: %v", err)
	}

	// Due: only a published message whose start has come and that is not sent.
	if err := w.repo.SetPublished(ctx, id, &w.now); err != nil {
		t.Fatal(err)
	}
	if due, _ := w.repo.Due(ctx, w.now, 10); len(due) != 0 {
		t.Errorf("scheduled for later: %v", due)
	}
	if due, _ := w.repo.Due(ctx, later.Add(time.Second), 10); len(due) != 1 || due[0] != id {
		t.Errorf("due after the start: %v", due)
	}

	// The list: the platform's and a landlord's are apart, and the status filter reads the dates at "now".
	own, err := w.repo.Insert(ctx, domain.Announcement{
		CreatedByID: &w.landlord, LandlordID: &w.landlord, SenderRole: "landlord", Scope: domain.ScopeMyTenants, Type: domain.TypeNotice,
		Title: domain.Texts{"vi": "t"}, Body: domain.Texts{"vi": "b"}, SendNotification: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	platform, total, err := w.repo.List(ctx, domain.AnnouncementQuery{Limit: 10}, w.now)
	if err != nil || total != 1 || len(platform) != 1 || platform[0].ID != id {
		t.Fatalf("platform list: %d %v", total, err)
	}
	mine, total, err := w.repo.List(ctx, domain.AnnouncementQuery{LandlordID: &w.landlord, Limit: 10}, w.now)
	if err != nil || total != 1 || len(mine) != 1 || mine[0].ID != own {
		t.Fatalf("landlord list: %d %v", total, err)
	}
	for status, want := range map[domain.Status]int{domain.StatusScheduled: 1, domain.StatusActive: 0, domain.StatusDraft: 0, domain.StatusEnded: 0, domain.StatusRecalled: 0} {
		s := status
		rows, total, err := w.repo.List(ctx, domain.AnnouncementQuery{Status: &s, Limit: 10}, w.now)
		if err != nil || total != want || len(rows) != want {
			t.Errorf("status %s: %d %v", status, total, err)
		}
	}
	active := domain.StatusActive
	if _, total, _ := w.repo.List(ctx, domain.AnnouncementQuery{Status: &active, Limit: 10}, later.Add(time.Minute)); total != 1 {
		t.Errorf("active once the start has come: %d", total)
	}
	popup, notification := domain.ChannelPopup, domain.ChannelNotification
	if _, total, _ := w.repo.List(ctx, domain.AnnouncementQuery{Channel: &popup, Limit: 10}, w.now); total != 1 {
		t.Errorf("popup channel: %d", total)
	}
	if _, total, _ := w.repo.List(ctx, domain.AnnouncementQuery{Channel: &notification, Limit: 10}, w.now); total != 1 {
		t.Errorf("notification channel: %d", total)
	}
	if page, total, _ := w.repo.List(ctx, domain.AnnouncementQuery{Limit: 1, Offset: 5}, w.now); total != 1 || len(page) != 0 {
		t.Errorf("a page past the end: %d %d", total, len(page))
	}
}

func TestPeopleSearchEscapesLikeAndOnlyListsRunningTenants(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `UPDATE users SET full_name = '100% Nguyen_A' WHERE id = $1`, w.renter); err != nil {
		t.Fatal(err)
	}
	people, total, err := w.repo.People(ctx, w.landlord, "", 20, 0)
	if err != nil || total != 1 || len(people) != 1 || people[0].ID != w.renter {
		t.Fatalf("running tenants only: %d %v", total, err)
	}
	for search, want := range map[string]int{"100%": 1, "nguyen_a": 1, "%": 1, "_": 1, "zzz": 0, "x.vn": 1, "n%a": 0} {
		if _, total, err := w.repo.People(ctx, w.landlord, search, 20, 0); err != nil || total != want {
			t.Errorf("search %q: %d, want %d (%v)", search, total, want, err)
		}
	}
	if _, total, _ := w.repo.People(ctx, w.super, "", 20, 0); total != 0 {
		t.Errorf("somebody with no tenants: %d", total)
	}
}

func TestWelcomeExists(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	if ok, err := w.repo.WelcomeExists(ctx); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
	if _, err := w.repo.Insert(ctx, domain.Announcement{
		SenderRole: "admin", Scope: domain.ScopeCustom, Criteria: domain.Criteria{WardCodes: []int{10}}, Type: domain.TypeWelcome,
		Title: domain.Texts{"vi": "t"}, Body: domain.Texts{"vi": "b"}, ShowPopup: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.repo.WelcomeExists(ctx); err != nil || !ok {
		t.Errorf("%v %v", ok, err)
	}
}
