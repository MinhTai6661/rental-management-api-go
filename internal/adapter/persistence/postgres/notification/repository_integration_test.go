//go:build integration

package notificationpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

type world struct {
	pool  *pgxpool.Pool
	repo  *NotificationRepository
	alice uuid.UUID
	bob   uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	pool := pg.NewDB(t)
	user := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return &world{pool: pool, repo: NewNotificationRepository(pool), alice: user("a@example.vn"), bob: user("b@example.vn")}
}

func (w *world) insert(t *testing.T, user uuid.UUID, kind domain.Kind, audience domain.Audience) uuid.UUID {
	t.Helper()
	ids, err := w.repo.Insert(context.Background(), []uuid.UUID{user}, kind, map[string]string{"room": "P1"}, "/rooms", audience)
	if err != nil || len(ids) != 1 {
		t.Fatalf("insert: %v %v", ids, err)
	}
	return ids[0]
}

func TestInsertManyAndListRoundTrip(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ids, err := w.repo.Insert(ctx, []uuid.UUID{w.alice, w.bob}, domain.InvoiceSent, map[string]string{"room": "P1", "amount_vnd": "100"}, "/x", domain.AudienceTenant)
	if err != nil || len(ids) != 2 {
		t.Fatalf("one statement, two rows: %v %v", ids, err)
	}
	rows, err := w.repo.List(ctx, w.alice, "", false, false, nil, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	n := rows[0]
	if n.UserID != w.alice || n.Kind != domain.InvoiceSent || n.Params["amount_vnd"] != "100" || n.URL != "/x" || n.Audience != domain.AudienceTenant || n.ReadAt != nil {
		t.Errorf("round trip: %+v", n)
	}

	// No url and no audience stay NULL and come back empty.
	id := w.insert(t, w.alice, domain.ContractEnded, domain.AudienceNone)
	var url, audience *string
	if err := w.pool.QueryRow(ctx, `SELECT url, audience_role FROM notifications WHERE id = $1`, id).Scan(&url, &audience); err != nil || audience != nil || url == nil {
		t.Errorf("audience is NULL: %v %v", audience, err)
	}
	ids, err = w.repo.Insert(ctx, []uuid.UUID{w.alice}, domain.ContractEnded, nil, "", domain.AudienceNone)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := w.repo.List(ctx, w.alice, "", false, false, nil, 1, 0)
	if len(ids) != 1 || got[0].ID != ids[0] || got[0].URL != "" || len(got[0].Params) != 0 {
		t.Errorf("empty url and params: %+v", got)
	}
}

func TestListFiltersByAudienceUnreadKindAndOrder(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tenantMsg := w.insert(t, w.alice, domain.InvoiceSent, domain.AudienceTenant)
	landlordMsg := w.insert(t, w.alice, domain.PaymentSubmitted, domain.AudienceLandlord)
	everyone := w.insert(t, w.alice, domain.ContractEnded, domain.AudienceNone)

	ids := func(rows []domain.Notification) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	all, _ := w.repo.List(ctx, w.alice, "", false, false, nil, 10, 0)
	if got := ids(all); len(got) != 3 || got[0] != everyone || got[2] != tenantMsg {
		t.Errorf("newest first: %v", got)
	}
	tenant, _ := w.repo.List(ctx, w.alice, domain.AudienceTenant, true, false, nil, 10, 0)
	if got := ids(tenant); len(got) != 2 || got[0] != everyone || got[1] != tenantMsg {
		t.Errorf("tenant side keeps its own and the shared ones: %v", got)
	}
	landlord, _ := w.repo.List(ctx, w.alice, domain.AudienceLandlord, true, false, nil, 10, 0)
	if got := ids(landlord); len(got) != 2 || got[1] != landlordMsg {
		t.Errorf("landlord side: %v", got)
	}
	if n, _ := w.repo.UnreadCount(ctx, w.alice, domain.AudienceTenant, true); n != 2 {
		t.Errorf("tenant unread: %d", n)
	}
	if n, _ := w.repo.UnreadCount(ctx, w.alice, "", false); n != 3 {
		t.Errorf("staff sees all: %d", n)
	}

	kind := domain.PaymentSubmitted
	if rows, _ := w.repo.List(ctx, w.alice, "", false, false, &kind, 10, 0); len(rows) != 1 {
		t.Errorf("kind filter: %d", len(rows))
	}
	if _, err := w.repo.MarkRead(ctx, w.alice, tenantMsg, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rows, _ := w.repo.List(ctx, w.alice, "", false, true, nil, 10, 0); len(rows) != 2 {
		t.Errorf("unread only: %d", len(rows))
	}
	if rows, _ := w.repo.List(ctx, w.alice, "", false, false, nil, 1, 1); len(rows) != 1 || rows[0].ID != landlordMsg {
		t.Errorf("limit and offset: %+v", rows)
	}
}

func TestMarkReadKeepsFirstReadTimeAndIsScoped(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := w.insert(t, w.alice, domain.InvoiceSent, domain.AudienceTenant)
	first := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	n, err := w.repo.MarkRead(ctx, w.alice, id, first)
	if err != nil || n.ReadAt == nil || !n.ReadAt.Equal(first) {
		t.Fatalf("%+v %v", n, err)
	}
	n, _ = w.repo.MarkRead(ctx, w.alice, id, first.Add(time.Hour))
	if !n.ReadAt.Equal(first) {
		t.Errorf("a second read keeps the first time, got %v", n.ReadAt)
	}
	if _, err := w.repo.MarkRead(ctx, w.bob, id, first); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("somebody else's message: %v", err)
	}
	if deleted, err := w.repo.Delete(ctx, w.bob, id); err != nil || deleted {
		t.Errorf("bob must not delete alice's message: %v %v", deleted, err)
	}
	if deleted, err := w.repo.Delete(ctx, w.alice, id); err != nil || !deleted {
		t.Errorf("alice deletes hers: %v %v", deleted, err)
	}
}

func TestBulkClearAndPrune(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a1 := w.insert(t, w.alice, domain.InvoiceSent, domain.AudienceTenant)
	a2 := w.insert(t, w.alice, domain.PaymentSubmitted, domain.AudienceLandlord)
	w.insert(t, w.alice, domain.ContractEnded, domain.AudienceNone)
	b1 := w.insert(t, w.bob, domain.InvoiceSent, domain.AudienceTenant)
	now := time.Now()

	if n, _ := w.repo.MarkManyRead(ctx, w.alice, []uuid.UUID{a1, a2, b1}, now); n != 2 {
		t.Errorf("only alice's own are changed: %d", n)
	}
	if n, _ := w.repo.MarkAllRead(ctx, w.alice, domain.AudienceTenant, true, now); n != 1 {
		t.Errorf("only the tenant side's unread message (a3) is changed: %d", n)
	}
	if n, _ := w.repo.UnreadCount(ctx, w.bob, "", false); n != 1 {
		t.Errorf("bob's message is untouched: %d", n)
	}
	if n, _ := w.repo.DeleteMany(ctx, w.alice, []uuid.UUID{a1, b1}); n != 1 {
		t.Errorf("only alice's own are deleted: %d", n)
	}
	if n, _ := w.repo.DeleteByScope(ctx, w.alice, true); n != 2 {
		t.Errorf("both left are read: %d", n)
	}
	if n, _ := w.repo.DeleteByScope(ctx, w.bob, false); n != 1 {
		t.Errorf("all of bob's: %d", n)
	}

	// Prune: rows older than the cutoff go, in batches.
	for i := 0; i < 5; i++ {
		w.insert(t, w.alice, domain.ContractEnded, domain.AudienceNone)
	}
	if _, err := w.pool.Exec(ctx, `UPDATE notifications SET created_at = now() - interval '100 days' WHERE id IN (SELECT id FROM notifications LIMIT 3)`); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	if n, _ := w.repo.PruneBefore(ctx, cutoff, 2); n != 2 {
		t.Errorf("one batch of 2: %d", n)
	}
	if n, _ := w.repo.PruneBefore(ctx, cutoff, 2); n != 1 {
		t.Errorf("the last old one: %d", n)
	}
	if n, _ := w.repo.PruneBefore(ctx, cutoff, 2); n != 0 {
		t.Errorf("nothing old left: %d", n)
	}
	if n, _ := w.repo.UnreadCount(ctx, w.alice, "", false); n != 2 {
		t.Errorf("the recent ones stay: %d", n)
	}
}

func TestAdminIDsAndHasKind(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true)`); err != nil {
		t.Fatal(err)
	}
	grant := func(user uuid.UUID, role string) {
		if _, err := w.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, user, role); err != nil {
			t.Fatal(err)
		}
	}
	var inactive uuid.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO users (email, full_name, status) VALUES ('i@example.vn', 'I', 'inactive') RETURNING id`).Scan(&inactive); err != nil {
		t.Fatal(err)
	}
	grant(w.alice, "admin")
	grant(w.bob, "super_admin")
	grant(inactive, "admin")
	ids, err := w.repo.AdminIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != w.alice {
		t.Errorf("only the active admin, not the super admin: %v %v", ids, err)
	}

	if has, _ := w.repo.HasKind(ctx, w.alice, domain.ListingSubmitted); has {
		t.Error("nothing yet")
	}
	w.insert(t, w.alice, domain.ListingSubmitted, domain.AudienceNone)
	if has, _ := w.repo.HasKind(ctx, w.alice, domain.ListingSubmitted); !has {
		t.Error("now there is one")
	}
	if has, _ := w.repo.HasKind(ctx, w.bob, domain.ListingSubmitted); has {
		t.Error("not bob's")
	}
}

func TestTheDatabaseRefusesGarbage(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for name, q := range map[string]string{
		"bad kind":     `INSERT INTO notifications (user_id, kind) VALUES ($1, 'Not A Kind')`,
		"bad audience": `INSERT INTO notifications (user_id, kind, audience_role) VALUES ($1, 'invoice_sent', 'admin')`,
		"array params": `INSERT INTO notifications (user_id, kind, params) VALUES ($1, 'invoice_sent', '[]')`,
	} {
		if _, err := w.pool.Exec(ctx, q, w.alice); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	// A message goes with its user.
	w.insert(t, w.alice, domain.InvoiceSent, domain.AudienceTenant)
	if _, err := w.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, w.alice); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&n); err != nil || n != 0 {
		t.Errorf("deleted with the user: %d %v", n, err)
	}
}
