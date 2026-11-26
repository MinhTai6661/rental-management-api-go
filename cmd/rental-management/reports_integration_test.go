//go:build integration

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	reporthttp "rental-management/internal/adapter/http/report"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/realtime"
	redisstore "rental-management/internal/adapter/redis"
)

// The report context is tested through the real composition code: the routes, the use cases on Postgres, the inbox of
// the notification context, the audit trail and the realtime hub.

type reportStack struct {
	*bridgeEnv
	hub     *realtime.ReportHub
	tickets *redisstore.StreamTickets
	server  *httptest.Server
	room    room
}

// testReportOrigin is the web origin the stack allows (the other tests of the sockets use one that is not allowed).
const testReportOrigin = "http://localhost:2001"

func newReportStack(t *testing.T) *reportStack {
	t.Helper()
	e := &reportStack{bridgeEnv: newBridgeEnv(t), hub: realtime.NewReportHub(5, 5, 32)}
	client := rds.Client(t)
	log := slog.New(slog.DiscardHandler)
	broadcaster := redisstore.NewReportBroadcaster(client, "test:", log)
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	go func() {
		close(listening)
		_ = broadcaster.Listen(ctx, e.hub.Deliver)
	}()
	<-listening
	time.Sleep(200 * time.Millisecond) // the subscription opens asynchronously
	e.tickets = redisstore.NewStreamTickets(client, "test:", time.Minute)
	reports := newReporting(log, e.pool, e.inbox.deliveries, reporthttp.NewEventPublisher(broadcaster))
	v1 := e.engine.Group("/v1")
	reporthttp.MountReports(v1, reporthttp.NewReportHandler(reports.reports, httpx.NewBinder()), e.auth)
	reporthttp.MountReportSocket(v1, reporthttp.NewSocketHandler(reports.reports, e.tickets, e.hub, reporthttp.SocketSettings{
		Heartbeat: time.Second, MaxAge: time.Hour, WriteTimeout: 5 * time.Second, MaxMessageBytes: 1024, FramesPerMinute: 20,
		AllowedOrigins: []string{testReportOrigin},
	}, log), e.auth)
	e.server = httptest.NewServer(e.engine)
	t.Cleanup(func() {
		e.hub.Close()
		e.server.Close()
		cancel()
	})
	e.room = e.newRoom("P1", "rented")
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, display_order)
		VALUES ($1, $2, $3, 'active', 2000000, current_date, 0)`, e.room.ID, e.landlord, e.tenant); err != nil {
		t.Fatal(err)
	}
	return e
}

type reportRow struct {
	ID       uuid.UUID `json:"id"`
	Type     string    `json:"type"`
	Subject  string    `json:"subject"`
	Status   string    `json:"status"`
	Deleted  bool      `json:"deleted"`
	Reporter struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"reporter"`
	Room *struct {
		Name string `json:"name"`
	} `json:"room"`
	RepliesCount int `json:"replies_count"`
}

type reportList struct {
	Data []reportRow `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

type replyList struct {
	Data []struct {
		Body   string `json:"body"`
		Author struct {
			Role string `json:"role"`
		} `json:"author"`
	} `json:"data"`
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
}

func (e *reportStack) create(bearer string, body map[string]any) reportRow {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/reports", body, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusCreated)
	return testenv.DecodeData[reportRow](e.t, w)
}

func (e *reportStack) roomReport() reportRow {
	return e.create("tenant", map[string]any{"type": "room", "room_id": e.room.ID, "subject": "Leak", "body": "The tap leaks"})
}

func (e *reportStack) path(r reportRow, suffix string) string {
	return "/v1/reports/" + r.ID.String() + suffix
}

func (e *reportStack) listOf(path, bearer string) reportList {
	e.t.Helper()
	w := e.call(http.MethodGet, path, nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return decodeFull[reportList](e.t, w)
}

func (e *reportStack) auditCount(action string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE action = $1`, action).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestCreatingReportsFollowsWhoMayWriteWhat(t *testing.T) {
	e := newReportStack(t)
	room := map[string]any{"type": "room", "room_id": e.room.ID, "subject": "Leak", "body": "Tap"}

	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", room, "landlord"), http.StatusForbidden, "auth.forbidden")
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", room, "admin"), http.StatusForbidden, "auth.forbidden")

	notMine := map[string]any{"type": "room", "room_id": uuid.New(), "subject": "x", "body": "y"}
	if body := testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", notMine, "tenant"), http.StatusUnprocessableEntity, "validation.failed"); body.Error.Fields["room_id"] == nil {
		t.Errorf("room_id not named: %+v", body)
	}
	// A room of somebody else's rented by nobody: the landlord's own tenant account is not a renter of it either.
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", room, "landlord-as-tenant"), http.StatusUnprocessableEntity, "validation.failed")
	missing := map[string]any{"type": "room", "subject": "x", "body": "y"}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", missing, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	blank := map[string]any{"type": "feature", "subject": "  ", "body": "y"}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", blank, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	badType := map[string]any{"type": "other", "subject": "x", "body": "y"}
	testenv.ExpectError(t, e.call(http.MethodPost, "/v1/reports", badType, "tenant"), http.StatusUnprocessableEntity, "validation.failed")

	made := e.roomReport()
	if made.Type != "room" || made.Status != "open" || made.Room == nil || made.Room.Name != "P1" || made.Reporter.Role != "tenant" {
		t.Errorf("report = %+v", made)
	}
	feature := e.create("landlord", map[string]any{"type": "feature", "subject": "Export", "body": "Please"})
	if feature.Room != nil || feature.Reporter.Role != "landlord" {
		t.Errorf("feature report = %+v", feature)
	}
	if e.auditCount("report.created") != 2 {
		t.Error("each report is in the audit trail")
	}
}

func TestListsAreScopedPagedAndFiltered(t *testing.T) {
	e := newReportStack(t)
	room := e.roomReport()
	e.create("tenant", map[string]any{"type": "feature", "subject": "Dark mode", "body": "Please"})
	e.create("landlord", map[string]any{"type": "feature", "subject": "Export", "body": "CSV"})

	mine := e.listOf("/v1/reports?per_page=1", "tenant")
	if mine.Meta.Total != 2 || len(mine.Data) != 1 || mine.Meta.PerPage != 1 || mine.Meta.Page != 1 {
		t.Errorf("tenant mine = %+v", mine)
	}
	if got := e.listOf("/v1/reports?type=room", "tenant"); got.Meta.Total != 1 || got.Data[0].ID != room.ID {
		t.Errorf("filter by type = %+v", got)
	}
	if got := e.listOf("/v1/reports?search=DARK", "tenant"); got.Meta.Total != 1 {
		t.Errorf("search = %+v", got)
	}
	if got := e.listOf("/v1/reports?search=%25", "tenant"); got.Meta.Total != 0 {
		t.Errorf("a wildcard is not a wildcard: %+v", got)
	}
	if got := e.listOf("/v1/reports?status=resolved", "tenant"); got.Meta.Total != 0 {
		t.Errorf("filter by status = %+v", got)
	}
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/reports?status=nope", nil, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/reports?per_page=500", nil, "tenant"), http.StatusUnprocessableEntity, "validation.failed")

	// Inboxes: the landlord of the room answers the room report, the admins the feature reports, a super admin all.
	if got := e.listOf("/v1/reports/inbox", "landlord"); got.Meta.Total != 1 || got.Data[0].ID != room.ID {
		t.Errorf("landlord inbox = %+v", got)
	}
	if got := e.listOf("/v1/reports/inbox", "landlord2"); got.Meta.Total != 0 {
		t.Errorf("another landlord inbox = %+v", got)
	}
	if got := e.listOf("/v1/reports/inbox", "admin"); got.Meta.Total != 2 || got.Data[0].Type != "feature" {
		t.Errorf("admin inbox = %+v", got)
	}
	if got := e.listOf("/v1/reports/inbox", "super"); got.Meta.Total != 3 {
		t.Errorf("super admin inbox = %+v", got)
	}
	if got := e.listOf("/v1/reports/inbox?type=room", "super"); got.Meta.Total != 1 {
		t.Errorf("super admin room inbox = %+v", got)
	}
	// The landlord account acting as a tenant answers nothing and has the permission of none: 403.
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/reports/inbox", nil, "tenant"), http.StatusForbidden, "auth.forbidden")
	// A landlord's own list is what they wrote.
	if got := e.listOf("/v1/reports", "landlord"); got.Meta.Total != 1 || got.Data[0].Type != "feature" {
		t.Errorf("landlord mine = %+v", got)
	}
}

func TestEveryoneSeesOnlyTheirOwnReports(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	for bearer, want := range map[string]int{
		"tenant": http.StatusOK, "landlord": http.StatusOK, "super": http.StatusOK,
		"landlord2": http.StatusNotFound, "admin": http.StatusNotFound, "landlord-as-tenant": http.StatusNotFound,
	} {
		if got := e.call(http.MethodGet, e.path(made, ""), nil, bearer).Code; got != want {
			t.Errorf("%s: GET = %d, want %d", bearer, got, want)
		}
		wantReplies := want
		if got := e.call(http.MethodGet, e.path(made, "/replies"), nil, bearer).Code; got != wantReplies {
			t.Errorf("%s: GET replies = %d, want %d", bearer, got, wantReplies)
		}
	}
	for _, bearer := range []string{"landlord2", "admin", "landlord-as-tenant"} {
		if got := e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "hi"}, bearer).Code; got != http.StatusNotFound {
			t.Errorf("%s: reply = %d", bearer, got)
		}
		if got := e.call(http.MethodDelete, e.path(made, ""), nil, bearer).Code; got != http.StatusNotFound {
			t.Errorf("%s: delete = %d", bearer, got)
		}
	}
	if got := e.call(http.MethodGet, "/v1/reports/"+uuid.NewString(), nil, "tenant").Code; got != http.StatusNotFound {
		t.Errorf("unknown id = %d", got)
	}
	if got := e.call(http.MethodGet, "/v1/reports/not-a-uuid", nil, "tenant").Code; got != http.StatusNotFound {
		t.Errorf("bad id = %d", got)
	}
}

func TestTheConversationTellsEachSide(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	// The landlord was told of the new report, in the landlord inbox, with a link to it.
	inbox := e.inboxOf("landlord", "")
	if got := kinds(inbox.Data); got["report_received"] != 1 {
		t.Fatalf("landlord inbox = %v", got)
	}
	row := inbox.Data[0]
	if row.URL == nil || *row.URL != "/reports?open="+made.ID.String() || row.Params["subject"] != "Leak" || row.Params["reporter"] == "" || row.Params["room"] != "P1" {
		t.Errorf("message = %+v", row)
	}
	if kinds(e.inboxOf("landlord-as-tenant", "").Data)["report_received"] != 0 {
		t.Error("the landlord's tenant inbox does not get it")
	}

	// The landlord answers; the tenant answers back.
	testenv.ExpectStatus(t, e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "On it"}, "landlord"), http.StatusCreated)
	if got := kinds(e.inboxOf("tenant", "").Data); got["report_answered"] != 1 {
		t.Errorf("tenant inbox = %v", got)
	}
	w := e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "Thanks"}, "tenant")
	testenv.ExpectStatus(t, w, http.StatusCreated)
	if got := testenv.DecodeData[reportRow](t, w); got.RepliesCount != 2 {
		t.Errorf("replies_count = %d", got.RepliesCount)
	}
	if got := kinds(e.inboxOf("landlord", "").Data); got["report_replied"] != 1 {
		t.Errorf("landlord inbox = %v", got)
	}

	// The conversation: newest first, paged, with the side of each author; the report carries only the count.
	page := decodeFull[replyList](t, e.call(http.MethodGet, e.path(made, "/replies?per_page=1"), nil, "tenant"))
	if page.Meta.Total != 2 || len(page.Data) != 1 || page.Data[0].Body != "Thanks" || page.Data[0].Author.Role != "tenant" {
		t.Errorf("page 1 = %+v", page)
	}
	page = decodeFull[replyList](t, e.call(http.MethodGet, e.path(made, "/replies?per_page=1&page=2"), nil, "landlord"))
	if len(page.Data) != 1 || page.Data[0].Body != "On it" || page.Data[0].Author.Role != "landlord" {
		t.Errorf("page 2 = %+v", page)
	}
	testenv.ExpectError(t, e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": ""}, "tenant"), http.StatusUnprocessableEntity, "validation.failed")
	if e.auditCount("report.replied") != 2 {
		t.Error("each message is in the audit trail")
	}

	// Only the recipient changes the status; resolving tells the author.
	testenv.ExpectError(t, e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "resolved"}, "tenant"), http.StatusForbidden, "auth.forbidden")
	testenv.ExpectError(t, e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "bogus"}, "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	if got := e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "resolved"}, "landlord2").Code; got != http.StatusNotFound {
		t.Errorf("another landlord sets the status: %d", got)
	}
	w = e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "resolved"}, "landlord")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[reportRow](t, w); got.Status != "resolved" {
		t.Errorf("status = %s", got.Status)
	}
	if got := kinds(e.inboxOf("tenant", "").Data); got["report_answered"] != 2 {
		t.Errorf("tenant inbox = %v", got)
	}
	if e.auditCount("report.status_changed") != 1 {
		t.Error("the status change is in the audit trail")
	}
}

func TestDeletingHidesTheReportFromItsAuthorOnly(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	testenv.ExpectStatus(t, e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "On it"}, "landlord"), http.StatusCreated)

	testenv.ExpectError(t, e.call(http.MethodDelete, e.path(made, ""), nil, "landlord"), http.StatusForbidden, "auth.forbidden")
	testenv.ExpectStatus(t, e.call(http.MethodDelete, e.path(made, ""), nil, "tenant"), http.StatusNoContent)
	if e.auditCount("report.deleted") != 1 {
		t.Error("the delete is in the audit trail")
	}

	// Gone for the author.
	for _, p := range []string{"", "/replies"} {
		if got := e.call(http.MethodGet, e.path(made, p), nil, "tenant").Code; got != http.StatusNotFound {
			t.Errorf("author GET %q = %d", p, got)
		}
	}
	if got := e.listOf("/v1/reports", "tenant"); got.Meta.Total != 0 {
		t.Errorf("author list = %+v", got)
	}
	if got := e.call(http.MethodDelete, e.path(made, ""), nil, "tenant").Code; got != http.StatusNotFound {
		t.Errorf("delete twice = %d", got)
	}

	// Still there for the recipient, read only.
	w := e.call(http.MethodGet, e.path(made, ""), nil, "landlord")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[reportRow](t, w); !got.Deleted || got.RepliesCount != 1 {
		t.Errorf("recipient view = %+v", got)
	}
	if got := e.listOf("/v1/reports/inbox", "landlord"); got.Meta.Total != 1 || !got.Data[0].Deleted {
		t.Errorf("recipient inbox = %+v", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, e.path(made, "/replies"), nil, "landlord"), http.StatusOK)
	testenv.ExpectError(t, e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "late"}, "landlord"), http.StatusForbidden, "report.read_only")
	testenv.ExpectError(t, e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "resolved"}, "landlord"), http.StatusForbidden, "report.read_only")
}

func TestFeatureReportsGoToTheAdmins(t *testing.T) {
	e := newReportStack(t)
	made := e.create("landlord", map[string]any{"type": "feature", "subject": "Export", "body": "CSV please"})
	if got := kinds(e.inboxOf("admin", "").Data); got["report_received"] != 1 {
		t.Errorf("admin inbox = %v", got)
	}
	// The landlord of a room cannot read a feature report that is not theirs; a tenant neither.
	if got := e.call(http.MethodGet, e.path(made, ""), nil, "landlord2").Code; got != http.StatusNotFound {
		t.Errorf("another landlord = %d", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "Noted"}, "admin"), http.StatusCreated)
	if got := kinds(e.inboxOf("landlord", "").Data); got["report_answered"] != 1 {
		t.Errorf("landlord inbox = %v", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "in_progress"}, "admin"), http.StatusOK)
	testenv.ExpectError(t, e.call(http.MethodDelete, e.path(made, ""), nil, "landlord"), http.StatusForbidden, "auth.forbidden")
}
