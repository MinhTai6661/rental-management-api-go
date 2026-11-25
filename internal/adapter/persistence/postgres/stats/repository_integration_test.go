//go:build integration

package statspg

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "rental-management/internal/domain/stats"
)

// The figures are checked against numbers worked out by hand from the rows below.

type world struct {
	t    *testing.T
	pool *pgxpool.Pool
	repo *Repository
	seq  int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := pg.NewDB(t)
	return &world{t: t, pool: pool, repo: NewRepository(pool)}
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.pool.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%v\n%s", err, sql)
	}
}

func (w *world) id(sql string, args ...any) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	if err := w.pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("%v\n%s", err, sql)
	}
	return id
}

func (w *world) next() int { w.seq++; return w.seq }

func (w *world) user(email string, roles ...string) uuid.UUID {
	for _, r := range roles {
		w.exec(`INSERT INTO roles (code, seniority, is_staff) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, r, map[string]int{"super_admin": 1, "admin": 2, "landlord": 3, "manager": 4, "tenant": 5}[r], r == "admin" || r == "super_admin")
	}
	id := w.id(`INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id`, email)
	for _, r := range roles {
		w.exec(`INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r)
	}
	return id
}

func (w *world) building(landlord uuid.UUID, name string) uuid.UUID {
	return w.id(`INSERT INTO buildings (landlord_id, name, address_detail, display_order) VALUES ($1, $2, 'x', $3) RETURNING id`, landlord, name, w.next())
}

func (w *world) room(landlord uuid.UUID, building *uuid.UUID, name, status, created string) uuid.UUID {
	return w.id(`INSERT INTO rooms (landlord_id, building_id, name, address_detail, price_vnd, status, display_order, created_at)
		VALUES ($1, $2, $3, 'x', 1000000, $4, $5, $6::timestamptz) RETURNING id`, landlord, building, name, status, w.next(), created)
}

func (w *world) contract(room, landlord uuid.UUID, tenant *uuid.UUID, status, starts string, ends *string) uuid.UUID {
	return w.id(`INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, ends_on, display_order, confirmed_at)
		VALUES ($1, $2, $3, $4, 1000000, $5::date, $6::date, $7, now()) RETURNING id`, room, landlord, tenant, status, starts, ends, w.next())
}

func (w *world) invoice(contract uuid.UUID, period, status string, total int64, due string) uuid.UUID {
	n := w.next()
	sent, via, paid := "2026-01-01T00:00:00Z", "manual", "2026-01-02T00:00:00Z"
	if status == "draft" {
		return w.id(`INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end, total_amount_vnd, due_at, status)
			VALUES ($1, $2, 'inv', 'LBL', $3, $4::date, $4::date + 27, $5, $6::timestamptz, 'draft') RETURNING id`, contract, fmt.Sprint("N", n), fmt.Sprint("C", n), period, total, due)
	}
	var paidAt *string
	if status == "paid" {
		paidAt = &paid
	}
	return w.id(`INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end, total_amount_vnd, due_at, status, sent_at, sent_via, paid_at)
		VALUES ($1, $2, 'inv', 'LBL', $3, $4::date, $4::date + 27, $5, $6::timestamptz, $7, $8::timestamptz, $9, $10::timestamptz) RETURNING id`,
		contract, fmt.Sprint("N", n), fmt.Sprint("C", n), period, total, due, status, sent, via, paidAt)
}

func (w *world) payment(invoice uuid.UUID, amount int64, status, at string) {
	reviewed := "2026-01-03T00:00:00Z"
	if status == "pending" {
		w.exec(`INSERT INTO payments (invoice_id, method, amount_vnd, status) VALUES ($1, 'manual_proof', $2, 'pending')`, invoice, amount)
		return
	}
	w.exec(`INSERT INTO payments (invoice_id, method, amount_vnd, status, paid_at, reviewed_at) VALUES ($1, 'manual_proof', $2, $3, $4::timestamptz, $5::timestamptz)`,
		invoice, amount, status, nilIf(status == "rejected", at), reviewed)
}

func nilIf(cond bool, v string) *string {
	if cond {
		return nil
	}
	return &v
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// landlordWorld is the rows of one landlord (L) and, to prove nothing leaks, of another (M).
type landlordWorld struct {
	*world
	L, M                  uuid.UUID
	tower                 uuid.UUID
	roomA, roomB, roomOld uuid.UUID
	contractA, contractB  uuid.UUID
}

func newLandlordWorld(t *testing.T) *landlordWorld {
	w := newWorld(t)
	l := &landlordWorld{world: w}
	l.L, l.M = w.user("l@example.vn", "landlord"), w.user("m@example.vn", "landlord")
	tenant := w.user("t@example.vn", "tenant")
	l.tower = w.building(l.L, "Tower")
	l.roomA = w.room(l.L, &l.tower, "A", "rented", "2026-01-01T00:00:00+07:00")
	l.roomB = w.room(l.L, nil, "B", "rented", "2026-02-01T00:00:00+07:00")
	l.roomOld = w.room(l.L, nil, "Old", "available", "2025-01-01T00:00:00+07:00")
	w.exec(`UPDATE rooms SET deleted_at = '2026-06-30T12:00:00+07:00' WHERE id = $1`, l.roomOld)
	l.contractA = w.contract(l.roomA, l.L, &tenant, "active", "2026-01-01", nil)
	l.contractB = w.contract(l.roomB, l.L, nil, "active", "2026-03-01", nil) // a blank contract: nobody with an account

	// A: September paid in full; October sent, 600k of 2M confirmed (one of them at 01:00 on 1 Nov in Vietnam), 300k waits;
	// November is only a draft.
	i1 := w.invoice(l.contractA, "2026-09-01", "paid", 1_000_000, "2026-09-10T00:00:00Z")
	w.payment(i1, 1_000_000, "confirmed", "2026-10-02T03:00:00Z")
	i2 := w.invoice(l.contractA, "2026-10-01", "sent", 2_000_000, "2026-10-10T00:00:00Z")
	w.payment(i2, 500_000, "confirmed", "2026-10-15T03:00:00Z")
	w.payment(i2, 100_000, "confirmed", "2026-10-31T18:00:00Z")
	w.payment(i2, 300_000, "pending", "")
	w.payment(i2, 50_000, "rejected", "")
	w.invoice(l.contractA, "2026-11-01", "draft", 9_999_999, "2026-11-10T00:00:00Z")
	// B: October sent, nothing paid, due far away.
	w.invoice(l.contractB, "2026-10-01", "sent", 700_000, "2099-01-01T00:00:00Z")

	// Meters of A: electricity 100.5 x 3500 and water 10 x 15000 in October, electricity 20 x 3500 in November.
	elec := w.id(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Electricity', 'electricity', 3500, 'per_kwh', 1) RETURNING id`, l.roomA)
	water := w.id(`INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Water', 'water', 15000, 'per_cubic_meter', 2) RETURNING id`, l.roomA)
	reading := func(charge, invoice uuid.UUID, on string, value, used float64, price int64) {
		w.exec(`INSERT INTO meter_readings (charge_id, invoice_id, read_on, reading_value, consumption, unit_price_vnd) VALUES ($1, $2, $3::date, $4, $5, $6)`, charge, invoice, on, value, used, price)
	}
	reading(elec, i2, "2026-10-05", 300.5, 100.5, 3500)
	reading(water, i2, "2026-10-20", 50, 10, 15000)
	reading(elec, i1, "2026-11-02", 320.5, 20, 3500)

	// M's room bills 5M in October: it must not show up for L.
	mt := w.user("mt@example.vn", "tenant")
	other := w.room(l.M, nil, "X", "rented", "2026-01-01T00:00:00+07:00")
	oc := w.contract(other, l.M, &mt, "active", "2026-01-01", nil)
	oi := w.invoice(oc, "2026-10-01", "sent", 5_000_000, "2026-10-10T00:00:00Z")
	w.payment(oi, 5_000_000, "confirmed", "2026-10-05T03:00:00Z")
	return l
}

func TestBilledCollectedAndOutstandingByMonth(t *testing.T) {
	l := newLandlordWorld(t)
	ctx, scope := context.Background(), domain.Scope{LandlordID: l.L}
	from, to := day(2026, 9, 1), day(2026, 11, 30)

	billed, err := l.repo.BilledByMonth(ctx, scope, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// September 1M; October 2M + 0.7M; the November draft is not billed.
	if billed["2026-09"] != 1_000_000 || billed["2026-10"] != 2_700_000 || billed["2026-11"] != 0 || len(billed) != 2 {
		t.Errorf("billed %v", billed)
	}
	collected, err := l.repo.CollectedByMonth(ctx, scope, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// 1M + 500k paid in October; 100k paid at 01:00 on 1 Nov in Vietnam is November's; pending and rejected are not money.
	if collected["2026-10"] != 1_500_000 || collected["2026-11"] != 100_000 || len(collected) != 2 {
		t.Errorf("collected %v", collected)
	}
	outstanding, err := l.repo.OutstandingByMonth(ctx, scope, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// October: 2M - 600k + 700k = 2.1M. September's invoice is paid and not listed.
	if outstanding["2026-10"] != 2_100_000 || len(outstanding) != 1 {
		t.Errorf("outstanding %v", outstanding)
	}
}

func TestOwedAtANow(t *testing.T) {
	l := newLandlordWorld(t)
	owed, err := l.repo.Owed(context.Background(), domain.Scope{LandlordID: l.L}, time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// 2.1M outstanding, of which A's October (1.4M) is past its due date; B's is due in 2099.
	if owed != (domain.Owed{OutstandingVND: 2_100_000, OverdueVND: 1_400_000, OverdueCount: 1}) {
		t.Errorf("%+v", owed)
	}
	none, err := l.repo.Owed(context.Background(), domain.Scope{LandlordID: uuid.New()}, time.Now())
	if err != nil || none != (domain.Owed{}) {
		t.Errorf("%+v %v", none, err)
	}
}

func TestMoneyByRoom(t *testing.T) {
	l := newLandlordWorld(t)
	ctx, scope := context.Background(), domain.Scope{LandlordID: l.L}
	billed, err := l.repo.BilledByRoom(ctx, scope, day(2026, 9, 1), day(2026, 11, 30))
	if err != nil {
		t.Fatal(err)
	}
	if billed[l.roomA] != 3_000_000 || billed[l.roomB] != 700_000 || len(billed) != 2 {
		t.Errorf("%v", billed)
	}
	collected, err := l.repo.CollectedByRoom(ctx, scope, day(2026, 9, 1), day(2026, 11, 30))
	if err != nil {
		t.Fatal(err)
	}
	if collected[l.roomA] != 1_600_000 || len(collected) != 1 {
		t.Errorf("%v", collected)
	}
	// October only: the November payment drops out.
	october, _ := l.repo.CollectedByRoom(ctx, scope, day(2026, 10, 1), day(2026, 10, 31))
	if october[l.roomA] != 1_500_000 {
		t.Errorf("%v", october)
	}
}

func TestTheBuildingFilterKeepsOneBuilding(t *testing.T) {
	l := newLandlordWorld(t)
	ctx, scope := context.Background(), domain.Scope{LandlordID: l.L, BuildingID: &l.tower}
	billed, err := l.repo.BilledByMonth(ctx, scope, day(2026, 9, 1), day(2026, 11, 30))
	if err != nil {
		t.Fatal(err)
	}
	if billed["2026-10"] != 2_000_000 {
		t.Errorf("only room A: %v", billed)
	}
	rooms, err := l.repo.Rooms(ctx, scope, day(2026, 9, 1))
	if err != nil || len(rooms) != 1 || rooms[0].ID != l.roomA {
		t.Errorf("%+v %v", rooms, err)
	}
}

func TestRoomsAndTheirRentedDays(t *testing.T) {
	l := newLandlordWorld(t)
	ctx, scope := context.Background(), domain.Scope{LandlordID: l.L}
	rooms, err := l.repo.Rooms(ctx, scope, day(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 3 {
		t.Fatalf("a room that was deleted after the window starts is still counted: %+v", rooms)
	}
	byName := map[string]domain.RoomFact{}
	for _, r := range rooms {
		byName[r.Name] = r
	}
	a, old := byName["A"], byName["Old"]
	if a.BuildingName == nil || *a.BuildingName != "Tower" || !a.CreatedOn.Equal(day(2026, 1, 1)) || a.Deleted() {
		t.Errorf("%+v", a)
	}
	if !old.Deleted() || !old.DeletedOn.Equal(day(2026, 6, 30)) {
		t.Errorf("%+v", old)
	}
	// Starting after the old room was deleted drops it.
	later, _ := l.repo.Rooms(ctx, scope, day(2026, 8, 1))
	if len(later) != 2 {
		t.Errorf("%+v", later)
	}
	contracts, err := l.repo.RentedContracts(ctx, scope, day(2026, 9, 1), day(2026, 11, 30))
	if err != nil || len(contracts) != 2 {
		t.Errorf("%+v %v", contracts, err)
	}
	// A contract that ended before the window is not read.
	l.exec(`UPDATE contracts SET status = 'ended', ends_on = '2026-08-31' WHERE id = $1`, l.contractB)
	contracts, _ = l.repo.RentedContracts(ctx, scope, day(2026, 9, 1), day(2026, 11, 30))
	if len(contracts) != 1 || contracts[0].RoomID != l.roomA {
		t.Errorf("%+v", contracts)
	}
}

func TestContractCounts(t *testing.T) {
	l := newLandlordWorld(t)
	ctx, scope := context.Background(), domain.Scope{LandlordID: l.L}
	tenant2 := l.user("t2@example.vn", "tenant")
	l.exec(`UPDATE contracts SET ends_on = '2026-12-10' WHERE id = $1`, l.contractA) // 15 days after 25 Nov
	pendingRoom := l.room(l.L, nil, "P", "available", "2026-01-01T00:00:00+07:00")
	l.contract(pendingRoom, l.L, &tenant2, "pending", "2026-12-01", nil)
	got, err := l.repo.ContractCounts(ctx, scope, day(2026, 11, 25), day(2026, 12, 25))
	if err != nil {
		t.Fatal(err)
	}
	if got != (domain.ContractCounts{Active: 2, Pending: 1, ExpiringSoon: 1}) {
		t.Errorf("%+v", got)
	}
}

func TestUtilitiesByMonth(t *testing.T) {
	l := newLandlordWorld(t)
	uses, err := l.repo.Utilities(context.Background(), domain.Scope{LandlordID: l.L}, day(2026, 9, 1), day(2026, 11, 30))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]domain.UtilityUse{}
	for _, u := range uses {
		got[u.Month+string(u.Kind)] = u
	}
	if len(got) != 3 {
		t.Fatalf("%+v", uses)
	}
	// 100.5 kWh at 3500 = 351,750; 10 m3 at 15,000 = 150,000; 20 kWh at 3500 = 70,000.
	if e := got["2026-10electricity"]; e.Consumption != 100.5 || e.CostVND != 351_750 {
		t.Errorf("%+v", e)
	}
	if w := got["2026-10water"]; w.Consumption != 10 || w.CostVND != 150_000 {
		t.Errorf("%+v", w)
	}
	if e := got["2026-11electricity"]; e.Consumption != 20 || e.CostVND != 70_000 {
		t.Errorf("%+v", e)
	}
}

func TestPlatformFacts(t *testing.T) {
	w := newWorld(t)
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, 11, 1, 0, 0, 0, 0, domain.Zone)
	previous := time.Date(2026, 10, 1, 0, 0, 0, 0, domain.Zone)

	a := w.user("a@example.vn", "tenant", "landlord") // two roles: counted under each
	b := w.user("b@example.vn", "tenant")
	gone := w.user("gone@example.vn", "tenant")
	w.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, gone)
	w.exec(`UPDATE users SET status = 'inactive' WHERE id = $1`, b)
	w.exec(`UPDATE users SET created_at = '2026-11-10T00:00:00Z' WHERE id = $1`, a)
	w.exec(`UPDATE users SET created_at = '2026-10-10T00:00:00Z' WHERE id = $1`, b)

	// Applications: one waiting since 20 Nov (5 days at 25 Nov 10:00), one approved after 10 hours.
	w.exec(`INSERT INTO landlord_applications (user_id, business_name, phone, status, created_at) VALUES ($1, 'x', '1', 'pending_review', '2026-11-20T03:00:00Z')`, b)
	w.exec(`INSERT INTO landlord_applications (user_id, business_name, phone, status, created_at, reviewed_at) VALUES ($1, 'x', '1', 'approved', '2026-11-01T00:00:00Z', '2026-11-01T10:00:00Z')`, a)

	// Plans and people on them.
	w.exec(`INSERT INTO plans (code, name, audience, price_vnd, billing_interval) VALUES ('free', 'Free', 'tenant', 0, 'month'), ('pro', 'Pro', 'landlord', 100000, 'month')`)
	w.exec(`INSERT INTO subscriptions (user_id, plan_id, audience, status, starts_at) SELECT $1, id, 'landlord', 'active', now() FROM plans WHERE code = 'pro'`, a)
	w.exec(`INSERT INTO subscriptions (user_id, plan_id, audience, status, starts_at) SELECT $1, id, 'tenant', 'pending', now() FROM plans WHERE code = 'free'`, b)

	// An announcement to 4 people, 1 of whom read it, and another that was recalled.
	ann := w.id(`INSERT INTO announcements (sender_role, audience, title, body, send_notification, show_popup, recipient_count, sent_at, published_at)
		VALUES ('admin', 'everyone', '{}', '{}', true, false, 4, '2026-11-12T00:00:00Z', '2026-11-12T00:00:00Z') RETURNING id`)
	w.exec(`INSERT INTO announcements (sender_role, audience, title, body, send_notification, show_popup, recipient_count, sent_at, published_at, recalled_at)
		VALUES ('admin', 'everyone', '{}', '{}', true, false, 100, '2026-11-13T00:00:00Z', '2026-11-13T00:00:00Z', now())`)
	w.exec(`INSERT INTO notifications (user_id, kind, announcement_id, read_at) VALUES ($1, 'announcement', $3, now()), ($2, 'announcement', $3, NULL)`, a, b, ann)

	f, err := w.repo.Facts(context.Background(), monthStart, previous, now.AddDate(0, 0, -90), now)
	if err != nil {
		t.Fatal(err)
	}
	if f.Users != (domain.UserCounts{Total: 2, Active: 1, NewThisMonth: 1, NewPreviousMonth: 1}) {
		t.Errorf("users %+v", f.Users)
	}
	if f.UsersByRole["tenant"] != 2 || f.UsersByRole["landlord"] != 1 {
		t.Errorf("by role %v", f.UsersByRole)
	}
	if f.ApplicationsByStatus["pending_review"] != 1 || f.ApplicationsByStatus["approved"] != 1 {
		t.Errorf("applications %v", f.ApplicationsByStatus)
	}
	if f.ReviewedApplications != 1 || f.AverageReviewHours != 10 || f.WaitingApplications != 1 || f.OldestWaitingDays != 5 {
		t.Errorf("review %+v", f)
	}
	if f.PendingSubscriptions != 1 {
		t.Errorf("pending %d", f.PendingSubscriptions)
	}
	if f.AnnouncementsSent != 1 || f.AnnouncementReached != 4 || f.AnnouncementRead != 1 {
		t.Errorf("announcements %+v", f)
	}

	users, apps, err := w.repo.SignUps(context.Background(), previous, monthStart.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if users["2026-11"] != 1 || users["2026-10"] != 1 || apps["2026-11"] != 2 {
		t.Errorf("sign-ups %v %v", users, apps)
	}
}

func TestSystemFacts(t *testing.T) {
	w := newWorld(t)
	w.user("s@example.vn", "super_admin")
	w.user("a1@example.vn", "admin")
	w.user("a2@example.vn", "admin")
	w.exec(`INSERT INTO plans (code, name, audience, price_vnd, billing_interval, display_order) VALUES ('year', 'Year', 'landlord', 1200000, 'year', 2), ('free', 'Free', 'tenant', 0, 'month', 1)`)
	u := w.user("p@example.vn", "tenant")
	w.exec(`INSERT INTO subscriptions (user_id, plan_id, audience, status, starts_at) SELECT $1, id, 'landlord', 'active', now() FROM plans WHERE code = 'year'`, u)
	for i := 0; i < 6; i++ {
		w.exec(`INSERT INTO audit_logs (action, changes) VALUES ('setting.changed', $1::jsonb)`, fmt.Sprintf(`{"n":%d}`, i))
	}
	w.exec(`INSERT INTO audit_logs (action) VALUES ('room.created')`)
	w.exec(`INSERT INTO audit_activity (action) VALUES ('auth.login'), ('auth.login')`)
	w.exec(`INSERT INTO audit_logs (action, created_at) VALUES ('room.created', now() - interval '60 days')`)

	f, err := w.repo.System(context.Background(), time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if f.Admins != 2 || f.SuperAdmins != 1 {
		t.Errorf("staff %d %d", f.Admins, f.SuperAdmins)
	}
	if len(f.Plans) != 2 || f.Plans[0].Code != "free" || f.Plans[1].Code != "year" || f.Plans[1].Subscribers != 1 {
		t.Errorf("plans %+v", f.Plans)
	}
	if domain.MonthlyRecurring(f.Plans) != 100_000 {
		t.Errorf("a yearly plan of 1.2M is 100k a month: %d", domain.MonthlyRecurring(f.Plans))
	}
	if len(f.SettingChanges) != 5 || string(f.SettingChanges[0].Changes) != `{"n": 5}` {
		t.Errorf("the five latest, newest first: %+v", f.SettingChanges)
	}
	if f.AuditImportant != 7 || f.AuditRoutine != 2 {
		t.Errorf("audit %d %d", f.AuditImportant, f.AuditRoutine)
	}
}
