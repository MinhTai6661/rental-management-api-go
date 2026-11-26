package report

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

type fakeTx struct{}

func (fakeTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeRepo struct {
	reports    map[uuid.UUID]*domain.View
	replies    map[uuid.UUID][]domain.Reply
	names      map[uuid.UUID]string
	landlordOf map[uuid.UUID]uuid.UUID // room -> landlord
}

func newRepo() *fakeRepo {
	return &fakeRepo{reports: map[uuid.UUID]*domain.View{}, replies: map[uuid.UUID][]domain.Reply{}, names: map[uuid.UUID]string{}, landlordOf: map[uuid.UUID]uuid.UUID{}}
}

func (r *fakeRepo) Insert(_ context.Context, n domain.NewReport) (uuid.UUID, error) {
	id := uuid.New()
	v := &domain.View{Report: domain.Report{ID: id, ReporterID: n.ReporterID, Kind: n.Kind, RoomID: n.RoomID, Subject: n.Subject, Body: n.Body, Status: domain.StatusOpen, CreatedAt: n.At},
		Reporter: domain.Person{ID: n.ReporterID, Name: r.names[n.ReporterID]}}
	if n.RoomID != nil {
		l := r.landlordOf[*n.RoomID]
		v.RoomLandlordID, v.RoomName = &l, "P101"
	}
	r.reports[id] = v
	return id, nil
}

func (r *fakeRepo) Get(_ context.Context, id uuid.UUID) (domain.View, error) {
	v, ok := r.reports[id]
	if !ok {
		return domain.View{}, shared.ErrNotFound
	}
	out := *v
	out.RepliesCount = len(r.replies[id])
	return out, nil
}

func (r *fakeRepo) Lock(ctx context.Context, id uuid.UUID) (domain.Report, error) {
	v, err := r.Get(ctx, id)
	return v.Report, err
}

func (r *fakeRepo) SetStatus(_ context.Context, id uuid.UUID, s domain.Status, _ time.Time) error {
	r.reports[id].Status = s
	return nil
}

func (r *fakeRepo) SoftDelete(_ context.Context, id uuid.UUID, at time.Time) error {
	r.reports[id].DeletedAt = &at
	return nil
}

func (r *fakeRepo) ListMine(_ context.Context, author uuid.UUID, f domain.Filter, limit, offset int) ([]domain.View, int, error) {
	var out []domain.View
	for _, v := range r.reports {
		if v.ReporterID == author && !v.Deleted() && strings.Contains(strings.ToLower(v.Subject), strings.ToLower(f.Search)) {
			out = append(out, *v)
		}
	}
	return out, len(out), nil
}

func (r *fakeRepo) ListInbox(_ context.Context, scope domain.InboxScope, _ domain.Filter, limit, offset int) ([]domain.View, int, error) {
	var out []domain.View
	for _, v := range r.reports {
		switch {
		case scope.Everything,
			scope.Features && v.Kind == domain.KindFeature,
			scope.LandlordID != nil && v.Kind == domain.KindRoom && *v.RoomLandlordID == *scope.LandlordID:
			out = append(out, *v)
		}
	}
	return out, len(out), nil
}

func (r *fakeRepo) InsertReply(_ context.Context, reportID, authorID uuid.UUID, body string, at time.Time) (domain.Reply, error) {
	reply := domain.Reply{ID: uuid.New(), ReportID: reportID, Body: body, CreatedAt: at, Author: domain.Person{ID: authorID, Name: r.names[authorID]}}
	r.replies[reportID] = append(r.replies[reportID], reply)
	return reply, nil
}

func (r *fakeRepo) ListReplies(_ context.Context, reportID uuid.UUID, limit, offset int) ([]domain.Reply, int, error) {
	list := r.replies[reportID]
	return list, len(list), nil
}

type fakeRooms struct{ rented map[[2]uuid.UUID]RoomRef }

func (f fakeRooms) RentedBy(_ context.Context, tenant, room uuid.UUID) (RoomRef, error) {
	if ref, ok := f.rented[[2]uuid.UUID{tenant, room}]; ok {
		return ref, nil
	}
	return RoomRef{}, shared.ErrNotFound
}

func (f fakeRooms) FirstRentedFrom(context.Context, uuid.UUID, uuid.UUID) (RoomRef, error) {
	return RoomRef{}, shared.ErrNotFound
}

type recorder struct {
	notes  []Notification
	audits []AuditEvent
	events []ReportEvent
}

func (r *recorder) Notify(_ context.Context, n Notification) error {
	r.notes = append(r.notes, n)
	return nil
}
func (r *recorder) Record(_ context.Context, e AuditEvent) error {
	r.audits = append(r.audits, e)
	return nil
}
func (r *recorder) Publish(_ context.Context, e ReportEvent) error {
	r.events = append(r.events, e)
	return nil
}

type world struct {
	uc                                *Reports
	repo                              *fakeRepo
	rec                               *recorder
	tenant, landlord, admin, stranger domain.Viewer
	room                              uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{repo: newRepo(), rec: &recorder{}, room: uuid.New()}
	w.tenant = domain.Viewer{UserID: uuid.New(), Role: authz.RoleTenant}
	w.landlord = domain.Viewer{UserID: uuid.New(), Role: authz.RoleLandlord}
	w.admin = domain.Viewer{UserID: uuid.New(), Role: authz.RoleAdmin}
	w.stranger = domain.Viewer{UserID: uuid.New(), Role: authz.RoleLandlord}
	for v, name := range map[uuid.UUID]string{w.tenant.UserID: "Tenant", w.landlord.UserID: "Landlord", w.admin.UserID: "Admin"} {
		w.repo.names[v] = name
	}
	w.repo.landlordOf[w.room] = w.landlord.UserID
	rooms := fakeRooms{rented: map[[2]uuid.UUID]RoomRef{{w.tenant.UserID, w.room}: {ID: w.room, Name: "P101", LandlordID: w.landlord.UserID}}}
	w.uc = NewReports(Deps{
		Tx: fakeTx{}, Reports: w.repo, Rooms: rooms, Notifier: w.rec, Audit: w.rec, Events: w.rec, Clock: ClockFunc(time.Now),
	})
	return w
}

func (w *world) roomReport(t *testing.T) domain.View {
	t.Helper()
	v, err := w.uc.Create(context.Background(), w.tenant, CreateInput{Kind: domain.KindRoom, RoomID: &w.room, Subject: "Leak", Body: "Tap"})
	if err != nil {
		t.Fatal(err)
	}
	w.rec.notes, w.rec.audits, w.rec.events = nil, nil, nil
	return v
}

func (w *world) featureReport(t *testing.T) domain.View {
	t.Helper()
	v, err := w.uc.Create(context.Background(), w.tenant, CreateInput{Kind: domain.KindFeature, Subject: "Dark", Body: "Mode"})
	if err != nil {
		t.Fatal(err)
	}
	w.rec.notes, w.rec.audits, w.rec.events = nil, nil, nil
	return v
}

func TestCreateRoomReportTellsTheLandlord(t *testing.T) {
	w := newWorld(t)
	v, err := w.uc.Create(context.Background(), w.tenant, CreateInput{Kind: domain.KindRoom, RoomID: &w.room, Subject: "Leak", Body: "Tap"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != domain.StatusOpen || v.Reporter.Name != "Tenant" {
		t.Errorf("view = %+v", v)
	}
	if len(w.rec.notes) != 1 {
		t.Fatalf("notes = %+v", w.rec.notes)
	}
	n := w.rec.notes[0]
	if n.UserID != w.landlord.UserID || n.ToAdmins || n.Kind != NotifyReceived || n.URL != "/reports?open="+v.ID.String() ||
		n.Params["subject"] != "Leak" || n.Params["reporter"] != "Tenant" || n.Params["room"] != "P101" {
		t.Errorf("note = %+v", n)
	}
	if len(w.rec.audits) != 1 || w.rec.audits[0].Action != AuditCreated || w.rec.audits[0].OwnerID != w.landlord.UserID || w.rec.audits[0].RoomID != w.room {
		t.Errorf("audits = %+v", w.rec.audits)
	}
	if len(w.rec.events) != 0 {
		t.Errorf("a new report is not published live: %+v", w.rec.events)
	}
}

func TestCreateFeatureReportTellsTheAdmins(t *testing.T) {
	w := newWorld(t)
	for _, author := range []domain.Viewer{w.tenant, w.landlord} {
		w.rec.notes, w.rec.events = nil, nil
		if _, err := w.uc.Create(context.Background(), author, CreateInput{Kind: domain.KindFeature, Subject: "S", Body: "B"}); err != nil {
			t.Fatal(err)
		}
		if len(w.rec.notes) != 1 || !w.rec.notes[0].ToAdmins {
			t.Errorf("notes = %+v", w.rec.notes)
		}
	}
}

func TestCreateRefusals(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	other := uuid.New()
	cases := []struct {
		name   string
		viewer domain.Viewer
		in     CreateInput
		want   error
	}{
		{"landlord reports a room", w.landlord, CreateInput{Kind: domain.KindRoom, RoomID: &w.room}, domain.ErrForbidden},
		{"admin reports a feature", w.admin, CreateInput{Kind: domain.KindFeature}, domain.ErrForbidden},
		{"room without a room", w.tenant, CreateInput{Kind: domain.KindRoom}, domain.ErrRoomRequired},
		{"room never rented", w.tenant, CreateInput{Kind: domain.KindRoom, RoomID: &other}, domain.ErrRoomNotYours},
	}
	for _, c := range cases {
		if _, err := w.uc.Create(ctx, c.viewer, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if len(w.rec.notes)+len(w.rec.audits)+len(w.rec.events) != 0 || len(w.repo.reports) != 0 {
		t.Error("a refused report leaves nothing behind")
	}
}

func TestReplyByTheAuthorTellsTheRecipient(t *testing.T) {
	w := newWorld(t)
	v := w.roomReport(t)
	got, err := w.uc.Reply(context.Background(), w.tenant, v.ID, "Still leaking")
	if err != nil {
		t.Fatal(err)
	}
	if got.View.RepliesCount != 1 || got.Reply.Body != "Still leaking" || got.Reply.Author.Name != "Tenant" {
		t.Errorf("replied = %+v", got)
	}
	if len(w.rec.notes) != 1 || w.rec.notes[0].Kind != NotifyReplied || w.rec.notes[0].UserID != w.landlord.UserID || w.rec.notes[0].Params["author"] != "Tenant" {
		t.Errorf("notes = %+v", w.rec.notes)
	}
	if len(w.rec.audits) != 1 || w.rec.audits[0].Action != AuditReplied || w.rec.audits[0].Meta["length"] != 13 {
		t.Errorf("audits = %+v", w.rec.audits)
	}
	if len(w.rec.events) != 1 || w.rec.events[0].Kind != EventReply || w.rec.events[0].Reply == nil || w.rec.events[0].Reply.Body != "Still leaking" || w.rec.events[0].Report.ID != v.ID {
		t.Errorf("events = %+v", w.rec.events)
	}
}

func TestReplyByTheRecipientTellsTheAuthor(t *testing.T) {
	w := newWorld(t)
	room, feature := w.roomReport(t), w.featureReport(t)
	if _, err := w.uc.Reply(context.Background(), w.landlord, room.ID, "On it"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.uc.Reply(context.Background(), w.admin, feature.ID, "Noted"); err != nil {
		t.Fatal(err)
	}
	if len(w.rec.notes) != 2 {
		t.Fatalf("notes = %+v", w.rec.notes)
	}
	for _, n := range w.rec.notes {
		if n.Kind != NotifyAnswered || n.UserID != w.tenant.UserID || n.ToAdmins {
			t.Errorf("note = %+v", n)
		}
	}
}

func TestReplyAndStatusPermissions(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	v := w.roomReport(t)
	if _, err := w.uc.Reply(ctx, w.stranger, v.ID, "hi"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger reply: %v", err)
	}
	if _, err := w.uc.Reply(ctx, w.admin, v.ID, "hi"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("admin on a room report: %v", err)
	}
	if _, err := w.uc.Reply(ctx, w.tenant, uuid.New(), "hi"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown report: %v", err)
	}
	// The author sees the report but does not change its status.
	if _, err := w.uc.SetStatus(ctx, w.tenant, v.ID, domain.StatusResolved); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("author sets status: %v", err)
	}
	if len(w.rec.notes)+len(w.rec.audits)+len(w.rec.events) != 0 {
		t.Error("refused actions leave nothing behind")
	}
}

func TestSetStatusByTheRecipient(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	v := w.roomReport(t)
	got, err := w.uc.SetStatus(ctx, w.landlord, v.ID, domain.StatusInProgress)
	if err != nil || got.Status != domain.StatusInProgress {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(w.rec.notes) != 0 {
		t.Error("only resolving tells the author")
	}
	ch, _ := w.rec.audits[0].Changes["status"].(map[string]any)
	if w.rec.audits[0].Action != AuditStatusChanged || ch["from"] != "open" || ch["to"] != "in_progress" {
		t.Errorf("audit = %+v", w.rec.audits[0])
	}
	if _, err := w.uc.SetStatus(ctx, w.landlord, v.ID, domain.StatusResolved); err != nil {
		t.Fatal(err)
	}
	if len(w.rec.notes) != 1 || w.rec.notes[0].Kind != NotifyAnswered || w.rec.notes[0].UserID != w.tenant.UserID || w.rec.notes[0].Params["author"] != "" {
		t.Errorf("notes = %+v", w.rec.notes)
	}
	last := w.rec.events[len(w.rec.events)-1]
	if last.Kind != EventStatus || last.Report.Status != domain.StatusResolved {
		t.Errorf("the event carries the new status: %+v", last)
	}
	// Same status again: nothing happens.
	before := len(w.rec.audits)
	if _, err := w.uc.SetStatus(ctx, w.landlord, v.ID, domain.StatusResolved); err != nil || len(w.rec.audits) != before {
		t.Errorf("repeat: %v, audits %d", err, len(w.rec.audits))
	}
}

func TestSoftDelete(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	v := w.roomReport(t)
	if err := w.uc.Delete(ctx, w.landlord, v.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("recipient deletes: %v", err)
	}
	if err := w.uc.Delete(ctx, w.stranger, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger deletes: %v", err)
	}
	if err := w.uc.Delete(ctx, w.tenant, v.ID); err != nil {
		t.Fatal(err)
	}
	if w.rec.audits[len(w.rec.audits)-1].Action != AuditDeleted || len(w.rec.events) != 0 {
		t.Errorf("audits %+v pushes %+v", w.rec.audits, w.rec.events)
	}
	// Gone for the author.
	if _, err := w.uc.Get(ctx, w.tenant, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("author sees it: %v", err)
	}
	if _, total, _ := w.uc.Mine(ctx, w.tenant, domain.Filter{}, 10, 0); total != 0 {
		t.Errorf("mine = %d", total)
	}
	if err := w.uc.Delete(ctx, w.tenant, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// Still there for the recipient, read only.
	got, err := w.uc.Get(ctx, w.landlord, v.ID)
	if err != nil || !got.Deleted() {
		t.Fatalf("recipient: %+v %v", got, err)
	}
	if _, total, _ := w.uc.Inbox(ctx, w.landlord, domain.Filter{}, 10, 0); total != 1 {
		t.Errorf("inbox = %d", total)
	}
	if _, err := w.uc.Reply(ctx, w.landlord, v.ID, "late"); !errors.Is(err, domain.ErrReadOnly) {
		t.Errorf("reply: %v", err)
	}
	if _, err := w.uc.SetStatus(ctx, w.landlord, v.ID, domain.StatusResolved); !errors.Is(err, domain.ErrReadOnly) {
		t.Errorf("status: %v", err)
	}
	if _, err := w.uc.Replies(ctx, w.landlord, v.ID, 20, 0); err != nil {
		t.Errorf("recipient reads the conversation: %v", err)
	}
	if _, err := w.uc.Replies(ctx, w.tenant, v.ID, 20, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("author reads the conversation: %v", err)
	}
}

func TestInboxByRole(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.roomReport(t)
	w.featureReport(t)
	super := domain.Viewer{UserID: uuid.New(), Role: authz.RoleSuperAdmin}
	counts := map[string]int{}
	for name, v := range map[string]domain.Viewer{"landlord": w.landlord, "stranger": w.stranger, "admin": w.admin, "super": super, "tenant": w.tenant} {
		_, total, err := w.uc.Inbox(ctx, v, domain.Filter{}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		counts[name] = total
	}
	want := map[string]int{"landlord": 1, "stranger": 0, "admin": 1, "super": 2, "tenant": 0}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("%s inbox = %d, want %d", k, counts[k], v)
		}
	}
}

func TestGetIsolation(t *testing.T) {
	w := newWorld(t)
	v := w.roomReport(t)
	for name, viewer := range map[string]domain.Viewer{"stranger": w.stranger, "admin": w.admin} {
		if _, err := w.uc.Get(context.Background(), viewer, v.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := w.uc.Get(context.Background(), w.landlord, v.ID); err != nil {
		t.Errorf("landlord: %v", err)
	}
}

func TestTypingReachesTheEventsOnlyForThoseWhoMayWrite(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	v := w.roomReport(t)
	if err := w.uc.Typing(ctx, w.tenant, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.rec.events) != 1 || w.rec.events[0].Kind != EventTyping || w.rec.events[0].UserID != w.tenant.UserID {
		t.Errorf("events = %+v", w.rec.events)
	}
	if err := w.uc.Typing(ctx, w.stranger, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	if err := w.uc.Typing(ctx, w.tenant, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := w.uc.Delete(ctx, w.tenant, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.uc.Typing(ctx, w.landlord, v.ID); !errors.Is(err, domain.ErrReadOnly) {
		t.Errorf("deleted: %v", err)
	}
	if len(w.rec.events) != 1 {
		t.Errorf("events = %+v", w.rec.events)
	}
}
