//go:build integration

package auditpg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	domain "rental-management/internal/domain/audit"
	"rental-management/internal/domain/shared"
)

type world struct {
	pool *pgxpool.Pool
	repo *Repository
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := pg.NewDB(t)
	return &world{pool: pool, repo: NewRepository(pool)}
}

var seniority = map[string]int{"super_admin": 1, "admin": 2, "landlord": 3, "tenant": 5}

func (w *world) user(t *testing.T, email string, roles ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id`, email, "Name of "+email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if _, err := w.pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, r, seniority[r], r == "admin" || r == "super_admin"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

var base = time.Now().UTC().Truncate(time.Second)

func entry(action domain.Action, actor *uuid.UUID, role string, at time.Time) domain.Entry {
	return domain.Entry{Action: action, ActorID: actor, ActorRole: role, OccurredAt: at}
}

func (w *world) search(t *testing.T, q domain.Query) []domain.Record {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.BranchLimit == 0 {
		q.BranchLimit = 101
	}
	if !q.Important && !q.Routine {
		q.Important, q.Routine = true, true
	}
	rows, err := w.repo.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAppendAndSearchRoundTripBothTiers(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	actor := w.user(t, "a@example.vn")
	target, room, session := w.user(t, "t@example.vn"), uuid.New(), uuid.New()
	err := w.repo.Append(ctx, []domain.Entry{
		{
			Action: domain.RoomUpdated, ActorID: &actor, ActorRole: "landlord", SessionID: &session, RequestID: "req-1", IP: "10.0.0.1", UserAgent: "ua",
			Refs: domain.References{RoomID: &room, TargetUserID: &target}, ActingFor: &target, OccurredAt: base,
			Changes: map[string]any{"name": map[string]any{"from": "A", "to": "B"}}, Meta: map[string]any{"k": "v"},
		},
		entry(domain.AuthLogin, &actor, "landlord", base.Add(time.Second)),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := w.search(t, domain.Query{})
	if len(rows) != 2 || rows[0].Action != domain.AuthLogin || rows[0].Tier != domain.Routine || rows[1].Tier != domain.Important {
		t.Fatalf("newest first, both tiers: %+v", rows)
	}
	got := rows[1]
	if *got.ActorID != actor || got.ActorRole != "landlord" || got.ActorName != "Name of a@example.vn" || got.TargetName != "Name of t@example.vn" ||
		*got.Refs.RoomID != room || *got.SessionID != session || got.IP != "10.0.0.1" || got.UserAgent != "ua" || got.RequestID != "req-1" ||
		*got.ActingFor != target || got.Meta["k"] != "v" || got.Changes["name"].(map[string]any)["to"] != "B" {
		t.Errorf("%+v", got)
	}
	if only := w.search(t, domain.Query{Important: true}); len(only) != 1 || only[0].Action != domain.RoomUpdated {
		t.Errorf("one tier only: %+v", only)
	}
	if only := w.search(t, domain.Query{Routine: true}); len(only) != 1 || only[0].Action != domain.AuthLogin {
		t.Errorf("the other tier only: %+v", only)
	}
	if rows[0].Changes != nil || rows[0].Meta != nil {
		t.Errorf("no payload reads back as nil: %+v", rows[0])
	}
}

func TestSearchFilters(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ann, bob := w.user(t, "ann@example.vn"), w.user(t, "bob@example.vn")
	roomA, roomB := uuid.New(), uuid.New()
	rows := []domain.Entry{
		{Action: domain.RoomCreated, ActorID: &ann, ActorRole: "landlord", Refs: domain.References{RoomID: &roomA}, OccurredAt: base.Add(-72 * time.Hour)},
		{Action: domain.RoomUpdated, ActorID: &ann, ActorRole: "landlord", Refs: domain.References{RoomID: &roomA}, OccurredAt: base.Add(-24 * time.Hour)},
		{Action: domain.RoomUpdated, ActorID: &bob, ActorRole: "manager", Refs: domain.References{RoomID: &roomB, TargetUserID: &ann}, OccurredAt: base},
	}
	if err := w.repo.Append(ctx, rows, false); err != nil {
		t.Fatal(err)
	}
	count := func(q domain.Query) int { return len(w.search(t, q)) }
	if n := count(domain.Query{Filter: domain.Filter{RoomID: &roomA}}); n != 2 {
		t.Errorf("by room: %d", n)
	}
	if n := count(domain.Query{Filter: domain.Filter{ActorID: &bob}}); n != 1 {
		t.Errorf("by actor: %d", n)
	}
	if n := count(domain.Query{Filter: domain.Filter{ActorRole: "manager"}}); n != 1 {
		t.Errorf("by role: %d", n)
	}
	updated := domain.RoomUpdated
	if n := count(domain.Query{Filter: domain.Filter{Action: &updated}}); n != 2 {
		t.Errorf("by action: %d", n)
	}
	if n := count(domain.Query{Involving: &ann}); n != 3 { // two she did, one done to her
		t.Errorf("involving: %d", n)
	}
	if n := count(domain.Query{Filter: domain.Filter{TargetUserID: &ann}}); n != 1 {
		t.Errorf("by target: %d", n)
	}
	from, before := base.Add(-30*time.Hour), base.Add(-time.Hour)
	if n := count(domain.Query{From: &from, Before: &before}); n != 1 {
		t.Errorf("by range: %d", n)
	}
}

func TestSearchPagesCutEachTierBeforeTheMerge(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	var entries []domain.Entry
	for i := 0; i < 5; i++ {
		entries = append(entries,
			entry(domain.RoomUpdated, nil, "landlord", base.Add(time.Duration(2*i)*time.Second)),
			entry(domain.AuthLogin, nil, "tenant", base.Add(time.Duration(2*i+1)*time.Second)))
	}
	if err := w.repo.Append(ctx, entries, false); err != nil {
		t.Fatal(err)
	}
	// Page 2 of size 3: rows 4..6 of the merged list; each tier contributes at most 2*3+1 rows.
	rows := w.search(t, domain.Query{BranchLimit: 7, Limit: 4, Offset: 3})
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].OccurredAt.After(rows[i-1].OccurredAt) {
			t.Errorf("newest first: %v", rows)
		}
	}
	if !rows[0].OccurredAt.Equal(base.Add(6 * time.Second)) {
		t.Errorf("starts at the fourth newest: %v", rows[0].OccurredAt)
	}
}

func TestSearchCanLeaveStaffAndSettingsOut(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	admin, tenant := w.user(t, "admin@example.vn", "admin"), w.user(t, "tenant@example.vn", "tenant")
	super := w.user(t, "super@example.vn", "super_admin")
	landlord := w.user(t, "land@example.vn", "landlord")
	if err := w.repo.Append(ctx, []domain.Entry{
		entry(domain.RoomCreated, &landlord, "landlord", base),
		{Action: domain.UserRolesChanged, ActorID: &admin, ActorRole: "admin", Refs: domain.References{TargetUserID: &tenant}, OccurredAt: base.Add(time.Second)},
		{Action: domain.UserRolesChanged, ActorID: &landlord, ActorRole: "landlord", Refs: domain.References{TargetUserID: &admin}, OccurredAt: base.Add(2 * time.Second)},
		{Action: domain.UserRolesChanged, ActorID: &super, ActorRole: "super_admin", Refs: domain.References{TargetUserID: &tenant}, OccurredAt: base.Add(3 * time.Second)},
		// a role label alone is enough to hide the entry, even when the account is gone from user_roles
		{Action: domain.UserDeleted, ActorRole: "admin", Refs: domain.References{TargetUserID: &tenant}, OccurredAt: base.Add(4 * time.Second)},
		entry(domain.SettingChanged, &super, "", base.Add(5*time.Second)),
	}, false); err != nil {
		t.Fatal(err)
	}
	if n := len(w.search(t, domain.Query{})); n != 6 {
		t.Fatalf("all: %d", n)
	}
	rows := w.search(t, domain.Query{ExcludeStaff: true, ExcludeSettings: true})
	if len(rows) != 1 || rows[0].Action != domain.RoomCreated {
		t.Errorf("an admin sees neither staff, nor what was done to staff, nor the settings: %+v", rows)
	}
	if rows := w.search(t, domain.Query{ExcludeSettings: true}); len(rows) != 5 {
		t.Errorf("settings only: %d", len(rows))
	}
}

func TestAnIsolatedWriteThatFailsLeavesTheTransactionUsable(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tx := postgres.NewTxManager(w.pool)
	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := w.repo.Append(ctx, []domain.Entry{entry(domain.RoomCreated, nil, "landlord", base)}, false); err != nil {
			return err
		}
		bad := entry(domain.Action(strings.Repeat("x", 100)), nil, "", base) // longer than the column
		if err := w.repo.Append(ctx, []domain.Entry{bad}, true); err == nil {
			t.Error("the oversized action must fail")
		}
		// The transaction is still alive: this entry and the first one commit together.
		return w.repo.Append(ctx, []domain.Entry{entry(domain.RoomDeleted, nil, "landlord", base.Add(time.Second))}, false)
	})
	if err != nil {
		t.Fatalf("the savepoint must absorb the failure: %v", err)
	}
	if n := len(w.search(t, domain.Query{})); n != 2 {
		t.Errorf("two entries committed: %d", n)
	}
	// Without isolation the same failure aborts the transaction and takes the first entry with it.
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := w.repo.Append(ctx, []domain.Entry{entry(domain.RoomReordered, nil, "landlord", base)}, false); err != nil {
			return err
		}
		return w.repo.Append(ctx, []domain.Entry{entry(domain.Action(strings.Repeat("x", 100)), nil, "", base)}, false)
	})
	if err == nil {
		t.Fatal("must fail")
	}
	if n := len(w.search(t, domain.Query{})); n != 2 {
		t.Errorf("the rolled back entry is gone: %d", n)
	}
}

func TestEntriesRollBackWithTheirAction(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := postgres.NewTxManager(w.pool).WithinTx(ctx, func(ctx context.Context) error {
		if err := w.repo.Append(ctx, []domain.Entry{entry(domain.PaymentConfirmed, nil, "landlord", base)}, false); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || len(w.search(t, domain.Query{})) != 0 {
		t.Errorf("the entry belongs to the transaction of the action: %v", err)
	}
}

func TestScrubBlanksOnlyThePersonalDataOfThatPerson(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	gone, other, session := w.user(t, "gone@example.vn"), w.user(t, "other@example.vn"), uuid.New()
	mine := func(action domain.Action, who uuid.UUID) domain.Entry {
		e := entry(action, &who, "tenant", base)
		e.IP, e.UserAgent, e.SessionID = "1.2.3.4", "ua", &session
		return e
	}
	if err := w.repo.Append(ctx, []domain.Entry{mine(domain.RoomCreated, gone), mine(domain.AuthLogin, gone), mine(domain.RoomCreated, other)}, false); err != nil {
		t.Fatal(err)
	}
	if err := w.repo.ScrubPersonalData(ctx, gone); err != nil {
		t.Fatal(err)
	}
	for _, r := range w.search(t, domain.Query{}) {
		scrubbed := *r.ActorID == gone
		if scrubbed != (r.IP == "" && r.UserAgent == "" && r.SessionID == nil) {
			t.Errorf("%v: %+v", scrubbed, r)
		}
		if *r.ActorID == gone && r.Action == "" {
			t.Error("the rest of the entry stays")
		}
	}
}

func TestPartitionsAreMadeListedAndDropped(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	parts, err := w.repo.Partitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has := func(name string) bool {
		for _, p := range parts {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	now := time.Now()
	for _, table := range []string{domain.TableImportant, domain.TableRoutine} {
		if !has(table + "_default") {
			t.Errorf("%s has its default partition", table)
		}
		for _, m := range domain.MonthsAhead(now, 3) {
			if !has(domain.PartitionName(table, m)) {
				t.Errorf("the migration makes %s", domain.PartitionName(table, m))
			}
		}
	}

	far := time.Date(2031, 5, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ { // twice: the second run changes nothing
		if err := w.repo.CreatePartition(ctx, domain.TableImportant, far); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.repo.Append(ctx, []domain.Entry{entry(domain.RoomCreated, nil, "landlord", far)}, false); err != nil {
		t.Fatal(err)
	}
	var inPartition int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs_2031_05`).Scan(&inPartition); err != nil || inPartition != 1 {
		t.Fatalf("the row belongs to its month: %d %v", inPartition, err)
	}
	var strayInDefault int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs_default`).Scan(&strayInDefault); err != nil || strayInDefault != 0 {
		t.Errorf("default: %d %v", strayInDefault, err)
	}

	if err := w.repo.DropPartition(ctx, "audit_logs_default"); err == nil {
		t.Error("the default partition is never dropped")
	}
	if err := w.repo.DropPartition(ctx, "users"); err == nil {
		t.Error("only audit partitions are dropped")
	}
	if err := w.repo.DropPartition(ctx, "audit_logs_2031_05"); err != nil {
		t.Fatal(err)
	}
	if n := len(w.search(t, domain.Query{})); n != 0 {
		t.Errorf("the partition went with its rows: %d", n)
	}
	// Rows of a month that is already in the default partition block making that month: the error says so.
	if err := w.repo.Append(ctx, []domain.Entry{entry(domain.RoomCreated, nil, "landlord", far)}, false); err != nil {
		t.Fatal(err)
	}
	if err := w.repo.CreatePartition(ctx, domain.TableImportant, far); err == nil {
		t.Error("rows of that month sit in the default partition")
	}
	if err := w.repo.CreatePartition(ctx, "users", far); err == nil {
		t.Error("only the audit tables are partitioned")
	}
}

func TestOwnerOfARoom(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	landlord := w.user(t, "l@example.vn")
	var room uuid.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, display_order) VALUES ($1, 'P1', 'x', 1, 1) RETURNING id`, landlord).Scan(&room); err != nil {
		t.Fatal(err)
	}
	if owner, err := w.repo.OwnerOf(ctx, domain.SubjectRoom, room); err != nil || owner != landlord {
		t.Errorf("%v %v", owner, err)
	}
	for _, s := range []domain.Subject{domain.SubjectRoom, domain.SubjectContract, domain.SubjectInvoice} {
		if _, err := w.repo.OwnerOf(ctx, s, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", s, err)
		}
	}
}
