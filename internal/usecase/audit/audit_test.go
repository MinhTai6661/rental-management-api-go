package audit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/reqctx"
)

var fixedNow = time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)

type fakeRepo struct {
	appended   [][]domain.Entry
	isolated   []bool
	appendErr  error
	rows       []domain.Record
	lastQuery  domain.Query
	partitions []domain.Partition
	created    []string
	dropped    []string
	createErr  map[string]error
	scrubbed   []uuid.UUID
}

func (f *fakeRepo) Append(_ context.Context, e []domain.Entry, isolated bool) error {
	f.appended, f.isolated = append(f.appended, e), append(f.isolated, isolated)
	return f.appendErr
}
func (f *fakeRepo) Search(_ context.Context, q domain.Query) ([]domain.Record, error) {
	f.lastQuery = q
	return f.rows, nil
}
func (f *fakeRepo) ScrubPersonalData(_ context.Context, id uuid.UUID) error {
	f.scrubbed = append(f.scrubbed, id)
	return nil
}
func (f *fakeRepo) CreatePartition(_ context.Context, table string, m time.Time) error {
	name := domain.PartitionName(table, m)
	if err := f.createErr[name]; err != nil {
		return err
	}
	f.created = append(f.created, name)
	return nil
}
func (f *fakeRepo) Partitions(context.Context) ([]domain.Partition, error) { return f.partitions, nil }
func (f *fakeRepo) DropPartition(_ context.Context, name string) error {
	f.dropped = append(f.dropped, name)
	return nil
}

type fakeObjects map[uuid.UUID]uuid.UUID

func (o fakeObjects) OwnerOf(_ context.Context, _ domain.Subject, id uuid.UUID) (uuid.UUID, error) {
	if owner, ok := o[id]; ok {
		return owner, nil
	}
	return uuid.Nil, shared.ErrNotFound
}

func newRecorder(repo *fakeRepo) *Recorder {
	return NewRecorder(repo, ClockFunc(func() time.Time { return fixedNow }), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRecordTakesTheActorFromTheRequest(t *testing.T) {
	repo := &fakeRepo{}
	actor, session, owner := uuid.New(), uuid.New(), uuid.New()
	ctx := reqctx.With(context.Background(), reqctx.Info{
		RequestID: "req-1", IP: "10.0.0.1", UserAgent: "agent", ActorID: actor.String(), Role: "manager", SessionID: session.String(),
	})
	err := newRecorder(repo).Record(ctx, Event{
		Action: domain.RoomUpdated, OwnerID: &owner,
		Changes: map[string]any{"name": map[string]any{"from": "A", "to": "B"}, "password": "x"},
		Meta:    map[string]any{"account_number": "1234567890"},
	})
	if err != nil || len(repo.appended) != 1 || repo.isolated[0] {
		t.Fatalf("%v %v %v", err, repo.appended, repo.isolated)
	}
	e := repo.appended[0][0]
	if *e.ActorID != actor || e.ActorRole != "manager" || *e.SessionID != session || e.RequestID != "req-1" || e.IP != "10.0.0.1" || e.UserAgent != "agent" {
		t.Errorf("request facts: %+v", e)
	}
	if e.ActingFor == nil || *e.ActingFor != owner {
		t.Errorf("somebody else acted on the owner's data: %v", e.ActingFor)
	}
	if e.Changes["password"] != "[redacted]" || e.Meta["account_number"] != "****7890" {
		t.Errorf("scrubbed: %v %v", e.Changes, e.Meta)
	}
	if !e.OccurredAt.Equal(fixedNow) {
		t.Errorf("time %v", e.OccurredAt)
	}
}

func TestRecordOwnActionIsNotDoneForSomebodyElse(t *testing.T) {
	repo := &fakeRepo{}
	actor := uuid.New()
	ctx := reqctx.With(context.Background(), reqctx.Info{ActorID: actor.String(), Role: "landlord"})
	if err := newRecorder(repo).Record(ctx, Event{Action: domain.RoomCreated, OwnerID: &actor}); err != nil {
		t.Fatal(err)
	}
	if repo.appended[0][0].ActingFor != nil {
		t.Error("the owner acting on their own data is not acting for anybody")
	}
}

func TestRecordASystemJobHasNoRequest(t *testing.T) {
	repo := &fakeRepo{}
	if err := newRecorder(repo).Record(context.Background(), Event{Action: domain.SubscriptionExpired, Actor: &Actor{}}); err != nil {
		t.Fatal(err)
	}
	e := repo.appended[0][0]
	if e.ActorID != nil || e.ActorRole != domain.SystemRole {
		t.Errorf("%+v", e)
	}
}

func TestRecordAnAnonymousRequestHasNoRole(t *testing.T) {
	repo := &fakeRepo{}
	ctx := reqctx.With(context.Background(), reqctx.Info{IP: "9.9.9.9"})
	if err := newRecorder(repo).Record(ctx, Event{Action: domain.AuthLoginFailed}); err != nil {
		t.Fatal(err)
	}
	if e := repo.appended[0][0]; e.ActorID != nil || e.ActorRole != "" || e.IP != "9.9.9.9" {
		t.Errorf("%+v", e)
	}
}

func TestRecordALoginNamesItsActorAndSession(t *testing.T) {
	repo := &fakeRepo{}
	user, session := uuid.New(), uuid.New()
	ctx := reqctx.With(context.Background(), reqctx.Info{IP: "9.9.9.9"})
	err := newRecorder(repo).Record(ctx, Event{Action: domain.AuthLogin, Actor: &Actor{ID: user, Role: "tenant", SessionID: &session}})
	if err != nil {
		t.Fatal(err)
	}
	e := repo.appended[0][0]
	if *e.ActorID != user || e.ActorRole != "tenant" || *e.SessionID != session || !repo.isolated[0] {
		t.Errorf("a routine entry is written in a savepoint: %+v %v", e, repo.isolated)
	}
}

func TestRecordTheEventsActorWinsOverTheRequestsRole(t *testing.T) {
	repo := &fakeRepo{}
	caller, other := uuid.New(), uuid.New()
	ctx := reqctx.With(context.Background(), reqctx.Info{ActorID: caller.String(), Role: "admin"})
	if err := newRecorder(repo).Record(ctx, Event{Action: domain.AuthPasswordChanged, Actor: &Actor{ID: other}}); err != nil {
		t.Fatal(err)
	}
	if e := repo.appended[0][0]; *e.ActorID != other || e.ActorRole != "" {
		t.Errorf("the role of the request is the caller's, not the named actor's: %+v", e)
	}
}

func TestAnImportantFailureFailsTheActionARoutineOneDoesNot(t *testing.T) {
	boom := errors.New("disk full")
	repo := &fakeRepo{appendErr: boom}
	rec := newRecorder(repo)
	if err := rec.Record(context.Background(), Event{Action: domain.PaymentConfirmed}); !errors.Is(err, boom) {
		t.Errorf("important: %v", err)
	}
	if err := rec.Record(context.Background(), Event{Action: domain.AuthLogout}); err != nil {
		t.Errorf("routine: %v", err)
	}
}

func TestRecordManySplitsTheTiersAndRefusesUnknownActions(t *testing.T) {
	repo := &fakeRepo{}
	rec := newRecorder(repo)
	if err := rec.RecordMany(context.Background(), []Event{{Action: domain.RoomDeleted}, {Action: domain.AuthLogin}, {Action: domain.RoomDeleted}}); err != nil {
		t.Fatal(err)
	}
	if len(repo.appended) != 2 || len(repo.appended[0]) != 2 || len(repo.appended[1]) != 1 || repo.isolated[0] || !repo.isolated[1] {
		t.Errorf("one write per tier: %v %v", repo.appended, repo.isolated)
	}
	if err := rec.Record(context.Background(), Event{Action: "room.exploded"}); !errors.Is(err, domain.ErrUnknownAction) {
		t.Errorf("%v", err)
	}
}

func TestAdminSeesNoStaffNoSettingsAndNoMoney(t *testing.T) {
	repo := &fakeRepo{rows: []domain.Record{{Entry: domain.Entry{
		Action: domain.InvoiceUpdated, Changes: map[string]any{"total_amount": map[string]any{"from": 1.0, "to": 2.0}, "note": "n"},
		Meta: map[string]any{"amount": 5.0},
	}}, {Entry: domain.Entry{Action: domain.PlanSaved, Meta: map[string]any{"price": 9.0}}}}}
	reader := NewReader(repo, nil)
	feed, err := reader.Admin(context.Background(), Viewer{UserID: uuid.New(), Role: "admin"}, domain.Filter{}, Page{Page: 2, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	q := repo.lastQuery
	if !q.ExcludeStaff || !q.ExcludeSettings || !q.Important || !q.Routine || q.Limit != 21 || q.Offset != 20 || q.BranchLimit != 41 {
		t.Errorf("query %+v", q)
	}
	if got := feed.Items[0].Changes["total_amount"].(map[string]any)["to"]; got != domain.MoneyMask || feed.Items[0].Meta["amount"] != domain.MoneyMask {
		t.Errorf("money must be hidden: %v", feed.Items[0])
	}
	if feed.Items[1].Meta["price"] != 9.0 {
		t.Error("the platform's own prices stay visible")
	}
	if _, err := reader.Admin(context.Background(), Viewer{Role: "super_admin"}, domain.Filter{}, Page{Page: 1, PerPage: 20}); err != nil {
		t.Fatal(err)
	}
	if q := repo.lastQuery; q.ExcludeStaff || q.ExcludeSettings {
		t.Errorf("a super admin sees all: %+v", q)
	}
	if _, err := reader.Admin(context.Background(), Viewer{Role: "admin"}, domain.Filter{Tier: domain.Routine}, Page{Page: 1, PerPage: 20}); err != nil || repo.lastQuery.Important || !repo.lastQuery.Routine {
		t.Errorf("tier filter: %v %+v", err, repo.lastQuery)
	}
	day := func(d int) *time.Time { v := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC); return &v }
	if _, err := reader.Admin(context.Background(), Viewer{Role: "admin"}, domain.Filter{From: day(5), To: day(1)}, Page{Page: 1, PerPage: 20}); !errors.Is(err, domain.ErrRangeBackwards) {
		t.Errorf("%v", err)
	}
}

func TestFeedSaysWhetherMoreFollows(t *testing.T) {
	rows := make([]domain.Record, 4)
	repo := &fakeRepo{rows: rows}
	feed, err := NewReader(repo, nil).Admin(context.Background(), Viewer{Role: "admin"}, domain.Filter{}, Page{Page: 1, PerPage: 3})
	if err != nil || !feed.HasMore || len(feed.Items) != 3 {
		t.Errorf("%v %+v", err, feed)
	}
	repo.rows = nil
	feed, _ = NewReader(repo, nil).Admin(context.Background(), Viewer{Role: "admin"}, domain.Filter{}, Page{Page: 1, PerPage: 3})
	if feed.HasMore || feed.Items == nil {
		t.Errorf("an empty feed is an empty list: %+v", feed)
	}
	feed, _ = NewReader(repo, nil).Admin(context.Background(), Viewer{Role: "admin"}, domain.Filter{}, Page{Page: 5000, PerPage: 100})
	if len(feed.Items) != 0 {
		t.Error("too deep gives nothing")
	}
}

func TestMineShowsOtherPeoplesActionsAsARole(t *testing.T) {
	me, admin := uuid.New(), uuid.New()
	repo := &fakeRepo{rows: []domain.Record{
		{Entry: domain.Entry{Action: domain.UserRolesChanged, ActorID: &admin, ActorRole: "admin", IP: "1.1.1.1", OccurredAt: fixedNow}},
		{Entry: domain.Entry{Action: domain.AuthLogin, ActorID: &me, ActorRole: "tenant", IP: "2.2.2.2", OccurredAt: fixedNow}},
	}}
	items, more, err := NewReader(repo, nil).Mine(context.Background(), me, Page{Page: 1, PerPage: 20})
	if err != nil || more || len(items) != 2 {
		t.Fatalf("%v %v %v", items, more, err)
	}
	if items[0].ActorID != nil || items[0].IP != "" || items[0].ActorRole != "admin" || items[1].IP != "2.2.2.2" {
		t.Errorf("%+v", items)
	}
	if q := repo.lastQuery; q.Involving == nil || *q.Involving != me || !q.Important || !q.Routine {
		t.Errorf("%+v", q)
	}
}

func TestHistoryIsForTheOwnerOnly(t *testing.T) {
	owner, stranger, room := uuid.New(), uuid.New(), uuid.New()
	repo := &fakeRepo{}
	reader := NewReader(repo, fakeObjects{room: owner})
	if _, err := reader.History(context.Background(), Viewer{UserID: owner}, domain.SubjectRoom, room, Page{Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	if q := repo.lastQuery; q.Filter.RoomID == nil || *q.Filter.RoomID != room || q.Routine || !q.Important {
		t.Errorf("only the important tier, by room: %+v", q)
	}
	if _, err := reader.History(context.Background(), Viewer{UserID: stranger}, domain.SubjectRoom, room, Page{Page: 1, PerPage: 10}); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a stranger: %v", err)
	}
	if _, err := reader.History(context.Background(), Viewer{UserID: owner}, domain.SubjectInvoice, uuid.New(), Page{Page: 1, PerPage: 10}); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a missing object: %v", err)
	}
}

func TestMaintainMakesTheComingMonthsAndDropsTheExpired(t *testing.T) {
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	repo := &fakeRepo{partitions: []domain.Partition{
		{Table: domain.TableImportant, Name: "audit_logs_default"},
		{Table: domain.TableImportant, Name: "audit_logs_2026_11", Month: month(2026, 11)},
		{Table: domain.TableImportant, Name: "audit_logs_2024_10", Month: month(2024, 10)},
		{Table: domain.TableImportant, Name: "audit_logs_2024_11", Month: month(2024, 11)},
		{Table: domain.TableRoutine, Name: "audit_activity_2026_11", Month: month(2026, 11)},
		{Table: domain.TableRoutine, Name: "audit_activity_2026_07", Month: month(2026, 7)},
		{Table: domain.TableRoutine, Name: "audit_activity_2026_08", Month: month(2026, 8)},
	}}
	m := NewMaintainer(repo, ClockFunc(func() time.Time { return fixedNow }), slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetentionOptions{PartitionsAhead: 3, ImportantMonths: 24, RoutineDays: 90})
	res, err := m.Run(context.Background())
	if err != nil || res.Created != 6 || res.Dropped != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	wantDropped := map[string]bool{"audit_logs_2024_10": true, "audit_activity_2026_07": true}
	for _, d := range repo.dropped {
		if !wantDropped[d] {
			t.Errorf("dropped %s", d)
		}
	}
	// A month that cannot be made (rows already in the default partition) does not stop the rest.
	repo2 := &fakeRepo{createErr: map[string]error{"audit_logs_2026_12": errors.New("default partition has rows")}}
	m2 := NewMaintainer(repo2, ClockFunc(func() time.Time { return fixedNow }), slog.New(slog.NewTextHandler(io.Discard, nil)), RetentionOptions{PartitionsAhead: 1, ImportantMonths: 24, RoutineDays: 90})
	res, err = m2.Run(context.Background())
	if err == nil || res.Created != 3 {
		t.Errorf("%+v %v", res, err)
	}
}

func TestScrub(t *testing.T) {
	repo := &fakeRepo{}
	id := uuid.New()
	if err := newRecorder(repo).Scrub(context.Background(), id); err != nil || len(repo.scrubbed) != 1 || repo.scrubbed[0] != id {
		t.Errorf("%v %v", repo.scrubbed, err)
	}
}
