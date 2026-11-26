//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"rental-management/pkg/authz"
)

// The conversation socket is tested end to end: real WebSocket clients against the test server, the real use cases on
// Postgres, the one-time tickets and the fan-out through Redis Pub/Sub.

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (e *reportStack) wsURL(report reportRow, ticket string) string {
	return "ws" + strings.TrimPrefix(e.server.URL, "http") + "/v1/reports/" + report.ID.String() + "/ws?ticket=" + ticket
}

func (e *reportStack) ticketFor(user uuid.UUID, role string) string {
	e.t.Helper()
	ticket, err := e.tickets.Issue(context.Background(), user.String(), "", role)
	if err != nil {
		e.t.Fatal(err)
	}
	return ticket
}

// dialWith opens the socket with the ticket and the origin; the response says why a refused upgrade was refused.
func (e *reportStack) dialWith(report reportRow, ticket, origin string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(e.wsURL(report, ticket), header)
}

// mustDial opens the socket with the allowed origin.
func (e *reportStack) mustDial(report reportRow, ticket string) *websocket.Conn {
	e.t.Helper()
	conn, resp, err := e.dialWith(report, ticket, testReportOrigin)
	if err != nil {
		e.t.Fatalf("dial: %v", err)
	}
	_ = resp.Body.Close()
	return conn
}

// refusedWith tries to open the socket and returns the status of the refusal (0 when it was not refused).
func (e *reportStack) refusedWith(report reportRow, ticket, origin string) int {
	e.t.Helper()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return statusOfRefusedDial(e.wsURL(report, ticket), header)
}

func statusOfRefusedDial(url string, header http.Header) int {
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err == nil {
		_ = conn.Close()
		return 0
	}
	if resp == nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// join opens the socket of a person with the allowed origin and reads the ready frame.
func (e *reportStack) join(report reportRow, user uuid.UUID, role string) *wsClient {
	e.t.Helper()
	conn := e.mustDial(report, e.ticketFor(user, role))
	e.t.Cleanup(func() { _ = conn.Close() })
	c := &wsClient{t: e.t, conn: conn}
	if ready := c.next(); ready["type"] != "ready" || ready["v"] != float64(1) || ready["report_id"] != report.ID.String() {
		e.t.Fatalf("ready = %v", ready)
	}
	return c
}

func (c *wsClient) send(frame map[string]any) {
	c.t.Helper()
	if err := c.conn.WriteJSON(frame); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) next() map[string]any {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		c.t.Fatalf("decode %s: %v", data, err)
	}
	return frame
}

// nextTypes reads the next n frames (a sender gets an ack and the reply, in either order) keyed by type.
func (c *wsClient) nextTypes(n int) map[string]map[string]any {
	c.t.Helper()
	out := map[string]map[string]any{}
	for range n {
		frame := c.next()
		out[frame["type"].(string)] = frame
	}
	return out
}

func (c *wsClient) expectClosed(code int) {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, code) {
				c.t.Fatalf("closed with %v, want code %d", err, code)
			}
			return
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSocketConversationReachesTheOtherSideIsStoredAndNotified(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	tenant := e.join(made, e.tenant, authz.RoleTenant)
	landlord := e.join(made, e.landlord, authz.RoleLandlord)

	// The author writes over the socket: an ack for them, the reply for both.
	clientID := uuid.NewString()
	tenant.send(map[string]any{"type": "reply", "client_id": clientID, "body": "Still dripping"})
	own := tenant.nextTypes(2)
	if own["ack"]["client_id"] != clientID || own["ack"]["reply_id"] == "" {
		t.Errorf("ack = %v", own["ack"])
	}
	reply := own["reply"]["reply"].(map[string]any)
	if reply["body"] != "Still dripping" || reply["id"] != own["ack"]["reply_id"] || reply["author"].(map[string]any)["role"] != "tenant" {
		t.Errorf("reply frame = %v", own["reply"])
	}
	heard := landlord.next()
	if heard["type"] != "reply" || heard["reply"].(map[string]any)["body"] != "Still dripping" {
		t.Errorf("landlord heard %v", heard)
	}
	// Stored, audited, and the recipient told, exactly as for POST /replies.
	page := decodeFull[replyList](t, e.call(http.MethodGet, e.path(made, "/replies"), nil, "landlord"))
	if page.Meta.Total != 1 || page.Data[0].Body != "Still dripping" {
		t.Errorf("replies = %+v", page)
	}
	if got := kinds(e.inboxOf("landlord", "").Data); got["report_replied"] != 1 {
		t.Errorf("landlord inbox = %v", got)
	}
	if e.auditCount("report.replied") != 1 {
		t.Error("audited once")
	}

	// The recipient answers over the socket: the author is notified.
	landlord.send(map[string]any{"type": "reply", "client_id": uuid.NewString(), "body": "Plumber on Thursday"})
	landlord.nextTypes(2)
	if heard := tenant.next(); heard["type"] != "reply" || heard["reply"].(map[string]any)["author"].(map[string]any)["role"] != "landlord" {
		t.Errorf("tenant heard %v", heard)
	}
	if got := kinds(e.inboxOf("tenant", "").Data); got["report_answered"] != 1 {
		t.Errorf("tenant inbox = %v", got)
	}

	// Typing reaches only the other party and is never stored.
	tenant.send(map[string]any{"type": "typing"})
	if typing := landlord.next(); typing["type"] != "typing" || typing["user_id"] != e.tenant.String() {
		t.Errorf("typing = %v", typing)
	}
	// (The typing never comes back to the sender: the next frame the tenant reads is the HTTP reply below.)

	// The HTTP routes publish to the same sockets.
	posted := e.call(http.MethodPost, e.path(made, "/replies"), map[string]any{"body": "By HTTP"}, "tenant")
	if posted.Code != http.StatusCreated {
		t.Fatalf("POST reply: %d", posted.Code)
	}
	for name, c := range map[string]*wsClient{"tenant": tenant, "landlord": landlord} {
		if f := c.next(); f["type"] != "reply" || f["reply"].(map[string]any)["body"] != "By HTTP" {
			t.Errorf("%s heard %v", name, f)
		}
	}
	e.call(http.MethodPut, e.path(made, "/status"), map[string]any{"status": "resolved"}, "landlord")
	for name, c := range map[string]*wsClient{"tenant": tenant, "landlord": landlord} {
		if f := c.next(); f["type"] != "status" || f["status"] != "resolved" {
			t.Errorf("%s heard %v", name, f)
		}
	}
	if n := e.hub.Open(); n != 2 {
		t.Errorf("open sockets = %d", n)
	}
}

func TestSocketRefusesWhoMayNotJoinAndBadTickets(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	for name, who := range map[string]struct {
		user uuid.UUID
		role string
	}{
		"another landlord":         {e.landlord2, authz.RoleLandlord},
		"an admin":                 {e.admin, authz.RoleAdmin},
		"the landlord as a tenant": {e.landlord, authz.RoleTenant},
	} {
		if status := e.refusedWith(made, e.ticketFor(who.user, who.role), testReportOrigin); status != http.StatusNotFound {
			t.Errorf("%s: %d", name, status)
		}
	}
	// A super admin joins (they see everything).
	e.join(made, e.super, authz.RoleSuperAdmin)

	// A ticket works once; unknown and missing ones are refused.
	ticket := e.ticketFor(e.tenant, authz.RoleTenant)
	_ = e.mustDial(made, ticket).Close()
	for name, ticket := range map[string]string{"used": ticket, "unknown": "nope", "missing": ""} {
		if status := e.refusedWith(made, ticket, testReportOrigin); status != http.StatusUnauthorized {
			t.Errorf("%s ticket: %d", name, status)
		}
	}

	// A page of another origin is refused before the ticket is used up.
	fresh := e.ticketFor(e.tenant, authz.RoleTenant)
	if status := e.refusedWith(made, fresh, "http://evil.example"); status != http.StatusForbidden {
		t.Errorf("bad origin: %d", status)
	}
	_ = e.mustDial(made, fresh).Close() // the refused origin did not burn the ticket
	// A report nobody has.
	unknown := "ws" + strings.TrimPrefix(e.server.URL, "http") + "/v1/reports/" + uuid.NewString() + "/ws?ticket=" + e.ticketFor(e.tenant, authz.RoleTenant)
	if status := statusOfRefusedDial(unknown, nil); status != http.StatusNotFound {
		t.Errorf("unknown report: %d", status)
	}
}

func TestSocketAnswersBadFramesWithErrorFramesAndClosesAbusers(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	tenant := e.join(made, e.tenant, authz.RoleTenant)

	tenant.send(map[string]any{"type": "reply", "client_id": "not-a-uuid", "body": "x"})
	if f := tenant.next(); f["type"] != "error" || f["code"] != "validation.failed" || f["fields"] == nil || f["message"] == "" {
		t.Errorf("bad client id = %v", f)
	}
	clientID := uuid.NewString()
	tenant.send(map[string]any{"type": "reply", "client_id": clientID, "body": "   "})
	if f := tenant.next(); f["type"] != "error" || f["code"] != "validation.failed" || f["client_id"] != clientID {
		t.Errorf("blank body = %v", f)
	}
	tenant.send(map[string]any{"type": "shout"})
	if f := tenant.next(); f["code"] != "validation.failed" {
		t.Errorf("unknown type = %v", f)
	}
	if err := tenant.conn.WriteMessage(websocket.TextMessage, []byte("{nope")); err != nil {
		t.Fatal(err)
	}
	if f := tenant.next(); f["code"] != "request.malformed" {
		t.Errorf("malformed = %v", f)
	}
	if e.auditCount("report.replied") != 0 {
		t.Error("nothing was stored")
	}

	// A frame over the limit closes the socket.
	big := e.join(made, e.tenant, authz.RoleTenant)
	_ = big.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"reply","client_id":"`+uuid.NewString()+`","body":"`+strings.Repeat("a", 2000)+`"}`))
	big.expectClosed(websocket.CloseMessageTooBig)

	// A client that sends too many frames is closed as a policy violation.
	spam := e.join(made, e.tenant, authz.RoleTenant)
	for range 25 {
		if err := spam.conn.WriteJSON(map[string]any{"type": "typing"}); err != nil {
			break
		}
	}
	spam.expectClosed(websocket.ClosePolicyViolation)
}

func TestSocketOnADeletedReportIsReadOnly(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	if got := e.call(http.MethodDelete, e.path(made, ""), nil, "tenant").Code; got != http.StatusNoContent {
		t.Fatal(got)
	}
	// The author is gone from it; the recipient follows it read only.
	if status := e.refusedWith(made, e.ticketFor(e.tenant, authz.RoleTenant), testReportOrigin); status != http.StatusNotFound {
		t.Errorf("author: %d", status)
	}
	conn := e.mustDial(made, e.ticketFor(e.landlord, authz.RoleLandlord))
	defer func() { _ = conn.Close() }()
	landlord := &wsClient{t: t, conn: conn}
	if ready := landlord.next(); ready["deleted"] != true {
		t.Errorf("ready = %v", ready)
	}
	clientID := uuid.NewString()
	landlord.send(map[string]any{"type": "reply", "client_id": clientID, "body": "late"})
	if f := landlord.next(); f["type"] != "error" || f["code"] != "report.read_only" || f["client_id"] != clientID {
		t.Errorf("reply on a deleted report = %v", f)
	}
}

func TestSocketsAreCountedAndLeaveNothingBehind(t *testing.T) {
	e := newReportStack(t)
	made := e.roomReport()
	round := func() {
		a := e.join(made, e.tenant, authz.RoleTenant)
		b := e.join(made, e.landlord, authz.RoleLandlord)
		a.send(map[string]any{"type": "reply", "client_id": uuid.NewString(), "body": "hi"})
		a.nextTypes(2)
		b.next()
		if e.hub.Open() != 2 {
			t.Fatalf("open = %d", e.hub.Open())
		}
		_ = a.conn.Close()
		_ = b.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = b.conn.Close()
		eventually(t, "the hub to be empty", func() bool { return e.hub.Open() == 0 })
	}
	round() // warms up the connections of the test infrastructure
	time.Sleep(300 * time.Millisecond)
	before := runtime.NumGoroutine()
	round()
	eventually(t, "the goroutines of the sockets to end", func() bool { return runtime.NumGoroutine() <= before })

	// The cap of 5 sockets per user and per report.
	var open []*wsClient
	for range 5 {
		open = append(open, e.join(made, e.tenant, authz.RoleTenant))
	}
	if status := e.refusedWith(made, e.ticketFor(e.tenant, authz.RoleTenant), testReportOrigin); status != http.StatusTooManyRequests {
		t.Errorf("over the cap: %d", status)
	}
	// Closing the hub (the API shuts down) ends every socket with a going-away frame.
	e.hub.Close()
	for _, c := range open {
		c.expectClosed(websocket.CloseGoingAway)
	}
	eventually(t, "the hub to be empty", func() bool { return e.hub.Open() == 0 })
}
