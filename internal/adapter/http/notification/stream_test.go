package notificationhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/internal/adapter/realtime"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

// fakeTickets issues sequential tickets and honours "once".
type fakeTickets struct {
	mu     sync.Mutex
	issued map[string][2]string // ticket -> user id, role
	next   int
}

func (f *fakeTickets) Issue(_ context.Context, userID, _, role string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	ticket := fmt.Sprintf("t%d", f.next)
	if f.issued == nil {
		f.issued = map[string][2]string{}
	}
	f.issued[ticket] = [2]string{userID, role}
	return ticket, nil
}

func (f *fakeTickets) Redeem(_ context.Context, ticket string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	who, ok := f.issued[ticket]
	if !ok {
		return "", "", "", shared.ErrNotFound
	}
	delete(f.issued, ticket)
	return who[0], "", who[1], nil
}

type bearerAuth struct{ user uuid.UUID }

func (a bearerAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	return authz.Principal{UserID: a.user.String(), Role: authz.RoleTenant}, nil
}

type allowAllLimiter struct{}

func (allowAllLimiter) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

type streamEnv struct {
	server  *httptest.Server
	hub     *realtime.Hub
	tickets *fakeTickets
	user    uuid.UUID
}

func newStreamEnv(t *testing.T, maxPerUser int, settings StreamSettings) *streamEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	env := &streamEnv{hub: realtime.NewHub(maxPerUser), tickets: &fakeTickets{}, user: uuid.New()}
	engine := gin.New()
	engine.Use(httpx.Locale(catalog), httpx.Recover())
	MountRealtime(engine.Group("/v1"), NewStreamHandler(env.tickets, env.hub, settings), bearerAuth{user: env.user}, allowAllLimiter{})
	env.server = httptest.NewServer(engine)
	t.Cleanup(func() {
		env.hub.Close()
		env.server.Close()
	})
	return env
}

func (env *streamEnv) ticketFor(role string) string {
	ticket, _ := env.tickets.Issue(context.Background(), env.user.String(), "", role)
	return ticket
}

// lines reads the response body line by line into a channel that closes with the body.
func lines(resp *http.Response) <-chan string {
	out := make(chan string, 64)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			out <- scanner.Text()
		}
	}()
	return out
}

func waitFor(t *testing.T, in <-chan string, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case line, open := <-in:
			if !open {
				t.Fatalf("stream ended before %q", want)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no %q within 2s", want)
		}
	}
}

// openStream opens the stream and returns the status and the lines of the body (closed when the test ends).
func openStream(t *testing.T, env *streamEnv, query string) (int, <-chan string) {
	t.Helper()
	resp, err := http.Get(env.server.URL + "/v1/realtime/stream" + query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if status := resp.StatusCode; status != http.StatusOK {
		return status, nil
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	return resp.StatusCode, lines(resp)
}

var quickSettings = StreamSettings{TicketTTL: time.Minute, Heartbeat: time.Hour, MaxAge: time.Hour}

func TestStreamDeliversEventsToItsOwnerOnly(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	status, events := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	waitFor(t, events, "event: ready")

	env.hub.Deliver(realtime.Envelope{UserIDs: []uuid.UUID{uuid.New()}, Name: "notification", Data: json.RawMessage(`{"kind":"other"}`)})
	env.hub.Deliver(realtime.Envelope{UserIDs: []uuid.UUID{env.user}, Name: "notification", Data: json.RawMessage(`{"kind":"invoice_sent"}`)})
	waitFor(t, events, "event: notification")
	waitFor(t, events, `"kind":"invoice_sent"`)
}

func TestStreamSkipsEventsOfTheOtherSideOfTheAccount(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	_, events := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleLandlord))
	waitFor(t, events, "event: ready")

	env.hub.Deliver(realtime.Envelope{UserIDs: []uuid.UUID{env.user}, Audience: "tenant", Name: "notification", Data: json.RawMessage(`{"kind":"for_tenant"}`)})
	env.hub.Deliver(realtime.Envelope{UserIDs: []uuid.UUID{env.user}, Audience: "landlord", Name: "notification", Data: json.RawMessage(`{"kind":"for_landlord"}`)})
	waitFor(t, events, `"kind":"for_landlord"`) // the tenant one was before it and never showed up
}

func TestTicketWorksOnce(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	ticket := env.ticketFor(authz.RoleTenant)
	if status, _ := openStream(t, env, "?ticket="+ticket); status != http.StatusOK {
		t.Fatalf("first use: %d", status)
	}
	if status, _ := openStream(t, env, "?ticket="+ticket); status != http.StatusUnauthorized {
		t.Fatalf("reused ticket: %d", status)
	}
	if status, _ := openStream(t, env, ""); status != http.StatusUnauthorized {
		t.Fatalf("no ticket: %d", status)
	}
	if status, _ := openStream(t, env, "?ticket=nope"); status != http.StatusUnauthorized {
		t.Fatalf("unknown ticket: %d", status)
	}
}

func TestBearerCallerNeedsNoTicket(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	request, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/realtime/stream", nil)
	request.Header.Set("Authorization", "Bearer any")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	waitFor(t, lines(resp), "event: ready")
}

func TestStreamExpiresAfterMaxAge(t *testing.T) {
	env := newStreamEnv(t, 5, StreamSettings{TicketTTL: time.Minute, Heartbeat: time.Hour, MaxAge: 100 * time.Millisecond})
	_, events := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant))
	waitFor(t, events, "event: expired")
	waitClosed(t, events, "stream not closed after expired")
}

func TestStreamPingsWhileIdle(t *testing.T) {
	env := newStreamEnv(t, 5, StreamSettings{TicketTTL: time.Minute, Heartbeat: 50 * time.Millisecond, MaxAge: time.Hour})
	_, events := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant))
	waitFor(t, events, ": ping")
}

func TestSixthStreamIsRefused(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	for range 5 {
		if status, _ := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant)); status != http.StatusOK {
			t.Fatalf("stream refused too early: %d", status)
		}
	}
	if status, _ := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant)); status != http.StatusTooManyRequests {
		t.Fatalf("sixth stream: %d", status)
	}
}

func TestShutdownEndsStreams(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	_, events := openStream(t, env, "?ticket="+env.ticketFor(authz.RoleTenant))
	waitFor(t, events, "event: ready")
	env.hub.Close()
	waitClosed(t, events, "stream still open after the hub closed")
}

func TestTicketResponse(t *testing.T) {
	env := newStreamEnv(t, 5, quickSettings)
	request, _ := http.NewRequest(http.MethodPost, env.server.URL+"/v1/realtime/ticket", nil)
	request.Header.Set("Authorization", "Bearer any")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Data struct {
			Ticket    string `json:"ticket"`
			ExpiresIn int    `json:"expires_in"`
			StreamURL string `json:"stream_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || body.Data.ExpiresIn != 60 || body.Data.StreamURL != "/v1/realtime/stream?ticket="+body.Data.Ticket {
		t.Fatalf("status %d, body %+v", resp.StatusCode, body.Data)
	}
}

// waitClosed drains what is left (the data line of the last event) and fails unless the stream then ends.
func waitClosed(t *testing.T, in <-chan string, message string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-in:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal(message)
		}
	}
}
