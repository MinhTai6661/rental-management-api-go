//go:build integration

package adminpg

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
)

var now = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

type world struct {
	ctx  context.Context
	pool *pgxpool.Pool
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi'), (2, 'Hue', 'city', 'hue')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh'), (5, 2, 'Phu Hoi', 'ward', 'phu-hoi')`,
		`INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true), ('landlord', 3, false), ('tenant', 5, false)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return &world{ctx: ctx, pool: pool}
}

func (w *world) user(t *testing.T, name, email string, ward *int32, roles ...string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := w.pool.QueryRow(w.ctx, `INSERT INTO users (email, full_name, ward_code) VALUES ($1, $2, $3) RETURNING id`, email, name, ward).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if _, err := w.pool.Exec(w.ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func ptr[T any](v T) *T { return &v }

func ids(accounts []domain.Account) []uuid.UUID {
	out := make([]uuid.UUID, len(accounts))
	for i, a := range accounts {
		out[i] = a.ID
	}
	return out
}

func TestUserListFiltersAndVisibility(t *testing.T) {
	w := newWorld(t)
	repo := NewUserRepository(w.pool)
	viewer := w.user(t, "Viewer", "viewer@x.vn", nil, "admin")
	staff := w.user(t, "Other Admin", "staff@x.vn", nil, "admin")
	hanoi := w.user(t, "An 100%", "an@x.vn", ptr(int32(4)), "tenant")
	hue := w.user(t, "Binh_X", "binh@x.vn", ptr(int32(5)), "landlord", "tenant")
	gone := w.user(t, "Gone", "gone@x.vn", nil, "tenant")
	if err := repo.SoftDelete(w.ctx, gone, now); err != nil {
		t.Fatal(err)
	}

	list := func(f domain.UserFilter) []uuid.UUID {
		f.ViewerID = viewer
		if f.Sort == "" {
			f.Sort = domain.SortName
		}
		got, total, err := repo.List(w.ctx, f, 50, 0)
		if err != nil || total != len(got) {
			t.Fatalf("list: %v total=%d got=%d", err, total, len(got))
		}
		return ids(got)
	}
	if got := list(domain.UserFilter{}); !slices.Equal(got, []uuid.UUID{hanoi, hue}) {
		t.Errorf("no staff, no viewer, no deleted: %v", got)
	}
	if got := list(domain.UserFilter{IncludeStaff: true}); len(got) != 3 || !slices.Contains(got, staff) {
		t.Errorf("a super admin also sees staff: %v", got)
	}
	if got := list(domain.UserFilter{ProvinceCode: ptr(int32(2))}); !slices.Equal(got, []uuid.UUID{hue}) {
		t.Errorf("province: %v", got)
	}
	if got := list(domain.UserFilter{WardCode: ptr(int32(4))}); !slices.Equal(got, []uuid.UUID{hanoi}) {
		t.Errorf("ward: %v", got)
	}
	if got := list(domain.UserFilter{RoleName: ptr("landlord")}); !slices.Equal(got, []uuid.UUID{hue}) {
		t.Errorf("role: %v", got)
	}
	// Wildcards in the text are plain characters.
	if got := list(domain.UserFilter{Search: domain.EscapeLike("100%")}); !slices.Equal(got, []uuid.UUID{hanoi}) {
		t.Errorf("a percent sign: %v", got)
	}
	if got := list(domain.UserFilter{Search: domain.EscapeLike("%")}); !slices.Equal(got, []uuid.UUID{hanoi}) {
		t.Errorf("a lone percent sign only finds the name that has one: %v", got)
	}
	if got := list(domain.UserFilter{Search: domain.EscapeLike("binh_")}); !slices.Equal(got, []uuid.UUID{hue}) {
		t.Errorf("an underscore: %v", got)
	}
	if got := list(domain.UserFilter{Search: domain.EscapeLike("BINH@X")}); !slices.Equal(got, []uuid.UUID{hue}) {
		t.Errorf("email, any case: %v", got)
	}
	if _, err := repo.Get(w.ctx, gone); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a deleted account is not found: %v", err)
	}
}

func TestReplaceRolesAndSessions(t *testing.T) {
	w := newWorld(t)
	repo := NewUserRepository(w.pool)
	id := w.user(t, "A", "a@x.vn", nil, "tenant")
	if err := repo.ReplaceRoles(w.ctx, id, []string{"landlord", "tenant"}, now); err != nil {
		t.Fatal(err)
	}
	acc, err := repo.Lock(w.ctx, id)
	if err != nil || !slices.Equal(acc.Roles, []string{"landlord", "tenant"}) {
		t.Fatalf("roles are returned most senior first: %v %v", acc.Roles, err)
	}
	if err := repo.ReplaceRoles(w.ctx, id, []string{"tenant"}, now); err != nil {
		t.Fatal(err)
	}
	if acc, _ = repo.Get(w.ctx, id); !slices.Equal(acc.Roles, []string{"tenant"}) {
		t.Fatalf("replaced: %v", acc.Roles)
	}
	for range 2 {
		if _, err := w.pool.Exec(w.ctx, `INSERT INTO user_sessions (id, user_id, active_role, created_at, last_used_at) VALUES ($1, $2, 'tenant', now(), now())`, uuid.New(), id); err != nil {
			t.Fatal(err)
		}
	}
	closed, err := repo.RevokeSessions(w.ctx, id, "account_deleted", now)
	if err != nil || len(closed) != 2 {
		t.Fatalf("both sessions close: %v %v", closed, err)
	}
	if again, _ := repo.RevokeSessions(w.ctx, id, "account_deleted", now); len(again) != 0 {
		t.Fatalf("a closed session closes once: %v", again)
	}
}

func TestApplicationLifecycle(t *testing.T) {
	w := newWorld(t)
	repo := NewApplicationRepository(w.pool)
	user := w.user(t, "Applicant", "ap@x.vn", nil, "tenant")
	reviewer := w.user(t, "Admin", "ad@x.vn", nil, "admin")
	app := domain.Application{ID: uuid.Must(uuid.NewV7()), UserID: user, BusinessName: "Nhà trọ An", Phone: "090", Status: domain.ApplicationPending, CreatedAt: now}
	if err := repo.Insert(w.ctx, app); err != nil {
		t.Fatal(err)
	}
	second := app
	second.ID = uuid.Must(uuid.NewV7())
	if err := repo.Insert(w.ctx, second); !shared.IsUnique(err) {
		t.Fatalf("a second open application is a unique violation: %v", err)
	}
	if open, _ := repo.HasOpen(w.ctx, user); !open {
		t.Fatal("open")
	}
	got, err := repo.Lock(w.ctx, app.ID)
	if err != nil || got.ApplicantName != "Applicant" || got.ApplicantEmail == nil || *got.ApplicantEmail != "ap@x.vn" {
		t.Fatalf("lock: %+v %v", got, err)
	}
	if err := repo.Review(w.ctx, app.ID, domain.ApplicationRejected, reviewer, "thiếu", now); err != nil {
		t.Fatal(err)
	}
	if open, _ := repo.HasOpen(w.ctx, user); open {
		t.Fatal("answered")
	}
	// After a rejection a new one may be sent.
	if err := repo.Insert(w.ctx, second); err != nil {
		t.Fatalf("apply again: %v", err)
	}
	counts, _ := repo.CountsByStatus(w.ctx)
	if counts[domain.ApplicationRejected] != 1 || counts[domain.ApplicationPending] != 1 {
		t.Fatalf("counts: %v", counts)
	}
	items, total, err := repo.List(w.ctx, domainQuery(domain.ApplicationRejected, "an"), 10, 0)
	if err != nil || total != 1 || items[0].ReviewNote == nil || *items[0].ReviewNote != "thiếu" {
		t.Fatalf("list: %+v %d %v", items, total, err)
	}
	history, _ := repo.History(w.ctx, user, second.ID)
	if len(history) != 1 || history[0].ID != app.ID {
		t.Fatalf("history is the other applications: %+v", history)
	}
	if removed, _ := repo.DeleteOpen(w.ctx, user); !removed {
		t.Fatal("the open one is withdrawn")
	}
	if removed, _ := repo.DeleteOpen(w.ctx, user); removed {
		t.Fatal("nothing left to withdraw")
	}
	if _, err := repo.Get(w.ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Fatal(err)
	}
}

func domainQuery(status domain.ApplicationStatus, search string) domain.ApplicationQuery {
	return domain.ApplicationQuery{Status: status, Search: domain.EscapeLike(search), Sort: domain.ApplicationNewest}
}

func TestProfileAndImpact(t *testing.T) {
	w := newWorld(t)
	repo := NewProfileRepository(w.pool)
	landlord := w.user(t, "L", "l@x.vn", nil, "landlord")
	if _, err := repo.Get(w.ctx, landlord); !errors.Is(err, shared.ErrNotFound) {
		t.Fatal(err)
	}
	if err := repo.InsertIfAbsent(w.ctx, landlord, domain.ProfileInput{BusinessName: "First"}, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertIfAbsent(w.ctx, landlord, domain.ProfileInput{BusinessName: "Second"}, now); err != nil {
		t.Fatal(err)
	}
	if p, _ := repo.Get(w.ctx, landlord); p.BusinessName != "First" {
		t.Fatalf("an existing profile is kept: %+v", p)
	}
	if err := repo.Upsert(w.ctx, landlord, domain.ProfileInput{BusinessName: "Third", Description: ptr("d")}, now); err != nil {
		t.Fatal(err)
	}
	if p, _ := repo.Get(w.ctx, landlord); p.BusinessName != "Third" || p.Description == nil {
		t.Fatalf("upsert: %+v", p)
	}
	impact, err := repo.Impact(w.ctx, landlord)
	if err != nil || impact != (domain.Impact{}) {
		t.Fatalf("a landlord with nothing: %+v %v", impact, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	w := newWorld(t)
	repo := NewSettingRepository(w.pool)
	by := w.user(t, "S", "s@x.vn", nil, "super_admin")
	if err := repo.Set(w.ctx, "uploads.client_quality", 0.7, by, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Set(w.ctx, "uploads.client_quality", 0.8, by, now); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Overrides(w.ctx)
	if err != nil || len(got) != 1 || got["uploads.client_quality"] != 0.8 {
		t.Fatalf("a second set replaces: %v %v", got, err)
	}
	if err := repo.Reset(w.ctx, "uploads.client_quality"); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.Overrides(w.ctx); len(got) != 0 {
		t.Fatalf("reset: %v", got)
	}
}

func legalDoc(kind domain.LegalKind, version, summary string, at time.Time, by *uuid.UUID) domain.Document {
	body := json.RawMessage(`{"type":"doc","content":[]}`)
	text := domain.LegalText{Title: "T", Body: body}
	return domain.Document{
		ID: uuid.Must(uuid.NewV7()), Kind: kind, Version: version, Content: domain.LegalContent{VI: text, EN: text},
		Summary: domain.ChangeSummary{VI: summary, EN: summary}, PublishedByID: by, PublishedAt: at,
	}
}

func TestLegalDocuments(t *testing.T) {
	w := newWorld(t)
	repo := NewLegalRepository(w.pool)
	by := w.user(t, "Boss", "boss@x.vn", nil, "super_admin")
	if _, err := repo.LatestVersion(w.ctx); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("nothing published: %v", err)
	}
	first := legalDoc(domain.LegalTerms, "2026-11-01.100000", "one", now, &by)
	second := legalDoc(domain.LegalPrivacy, "2026-11-02.100000", "two", now.Add(time.Hour), &by)
	third := legalDoc(domain.LegalTerms, "2026-11-03.100000", "three", now.Add(2*time.Hour), nil)
	for _, d := range []domain.Document{first, second, third} {
		if err := repo.Insert(w.ctx, d, d.PublishedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Insert(w.ctx, legalDoc(domain.LegalTerms, third.Version, "dup", now, nil), now); !shared.IsUnique(err) {
		t.Fatalf("a version is published once per kind: %v", err)
	}
	if v, _ := repo.LatestVersion(w.ctx); v != third.Version {
		t.Fatalf("the newest of any kind: %s", v)
	}
	if d, _ := repo.Latest(w.ctx, domain.LegalPrivacy); d.Version != second.Version || d.PublishedByName != "Boss" || d.Summary.VI != "two" {
		t.Fatalf("latest privacy: %+v", d)
	}
	docs, total, err := repo.List(w.ctx, domain.LegalTerms, 10, 0)
	if err != nil || total != 2 || docs[0].Version != third.Version {
		t.Fatalf("terms, newest first: %+v %d %v", docs, total, err)
	}
	if _, err := repo.Get(w.ctx, domain.LegalPrivacy, first.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("an id of the other kind: %v", err)
	}
	after, _ := repo.After(w.ctx, now.Add(30*time.Minute), 10)
	if len(after) != 2 || after[0].Version != third.Version {
		t.Fatalf("after: %+v", after)
	}
	newest, _ := repo.NewestPerKind(w.ctx)
	if len(newest) != 2 {
		t.Fatalf("one per kind: %+v", newest)
	}

	user := w.user(t, "T", "t@x.vn", nil, "tenant")
	if at, err := repo.AcceptedAt(w.ctx, user); err != nil || at != nil {
		t.Fatalf("never accepted: %v %v", at, err)
	}
	if err := repo.SetAccepted(w.ctx, user, third.Version, now); err != nil {
		t.Fatal(err)
	}
	if at, _ := repo.AcceptedAt(w.ctx, user); at == nil || !at.Equal(now) {
		t.Fatalf("accepted at: %v", at)
	}
}

func TestEraseKeepsContractsAndBlanksTheAccount(t *testing.T) {
	w := newWorld(t)
	repo := NewPersonalDataRepository(w.pool)
	landlord := w.user(t, "Landlord", "ll@x.vn", ptr(int32(4)), "landlord", "tenant")
	tenant := w.user(t, "Tenant", "tn@x.vn", ptr(int32(4)), "tenant")
	var room uuid.UUID
	if err := w.pool.QueryRow(w.ctx, `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order) VALUES ($1, 'P1', 'addr', 1000000, 1) RETURNING id`, landlord).Scan(&room); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(w.ctx, `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, display_order) VALUES ($1, $2, $3, 'ended', 1000000, '2026-01-01', 1)`, room, landlord, tenant); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO notifications (user_id, kind) VALUES ($1, 'contract_ended')`,
		`INSERT INTO landlord_profiles (user_id, business_name) VALUES ($1, 'B')`,
		`INSERT INTO landlord_payment_methods (landlord_id, method, config_sealed) VALUES ($1, 'manual_proof', 'x')`,
	} {
		if _, err := w.pool.Exec(w.ctx, q, landlord); err != nil {
			t.Fatal(err)
		}
	}

	facts, err := repo.Facts(w.ctx, tenant)
	if err != nil || facts.AsTenant || facts.AsLandlord {
		t.Fatalf("an ended contract blocks nothing: %+v %v", facts, err)
	}
	if _, err := w.pool.Exec(w.ctx, `UPDATE contracts SET status = 'active'`); err != nil {
		t.Fatal(err)
	}
	if facts, _ = repo.Facts(w.ctx, tenant); !facts.AsTenant || facts.AsLandlord {
		t.Fatalf("a running contract blocks the tenant: %+v", facts)
	}
	if facts, _ = repo.Facts(w.ctx, landlord); facts.AsTenant || !facts.AsLandlord {
		t.Fatalf("and the landlord: %+v", facts)
	}
	if _, err := w.pool.Exec(w.ctx, `UPDATE contracts SET status = 'ended'`); err != nil {
		t.Fatal(err)
	}

	export, err := repo.Export(w.ctx, landlord)
	if err != nil || export.Account.Email == nil || len(export.Rooms) != 1 || export.Landlord == nil {
		t.Fatalf("export: %+v %v", export, err)
	}
	if err := repo.Erase(w.ctx, landlord, now); err != nil {
		t.Fatal(err)
	}
	var contracts, profiles, methods, notes int
	if err := w.pool.QueryRow(w.ctx, `SELECT (SELECT count(*) FROM contracts), (SELECT count(*) FROM landlord_profiles), (SELECT count(*) FROM landlord_payment_methods), (SELECT count(*) FROM notifications)`).Scan(&contracts, &profiles, &methods, &notes); err != nil {
		t.Fatal(err)
	}
	if contracts != 1 || profiles != 0 || methods != 0 || notes != 0 {
		t.Fatalf("contracts stay, personal rows go: %d %d %d %d", contracts, profiles, methods, notes)
	}
	if _, err := NewUserRepository(w.pool).Get(w.ctx, landlord); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("an erased account is gone for the app: %v", err)
	}
}
