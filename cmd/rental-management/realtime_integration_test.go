//go:build integration

package main

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/config"
	notificationhttp "rental-management/internal/adapter/http/notification"
	"rental-management/internal/adapter/realtime"
	redisstore "rental-management/internal/adapter/redis"
	"rental-management/pkg/authz"
)

// This is the whole realtime path with the real adapters: a message sent through the use case is published to Redis,
// the listener hands it to the hub, and the stream opened with a ticket writes it.

type realtimeEnv struct {
	*bridgeEnv
	server  *httptest.Server
	tickets *redisstore.StreamTickets
}

func newRealtimeEnv(t *testing.T) *realtimeEnv {
	t.Helper()
	e := &realtimeEnv{bridgeEnv: newBridgeEnv(t)}
	client := rds.Client(t)
	log := slog.New(slog.DiscardHandler)
	hub := realtime.NewHub(5)
	broadcaster := redisstore.NewBroadcaster(client, "test:", log)
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	go func() {
		close(listening)
		_ = broadcaster.Listen(ctx, hub.Deliver)
	}()
	<-listening
	time.Sleep(200 * time.Millisecond) // the subscription opens asynchronously
	e.tickets = redisstore.NewStreamTickets(client, "test:", time.Minute)
	e.inbox = newNotifications(config.Config{}, log, e.pool, nil, realtimePusher{broadcast: broadcaster})

	engine := gin.New()
	handler := notificationhttp.NewStreamHandler(e.tickets, hub, notificationhttp.StreamSettings{TicketTTL: time.Minute, Heartbeat: time.Hour, MaxAge: time.Hour})
	notificationhttp.MountRealtime(engine.Group("/v1"), handler, fakeAuth{}, allowAll{})
	e.server = httptest.NewServer(engine)
	t.Cleanup(func() {
		hub.Close()
		e.server.Close()
		cancel()
	})
	return e
}

func (e *realtimeEnv) open(t *testing.T, role string, userIsLandlord bool) <-chan string {
	t.Helper()
	user := e.tenant
	if userIsLandlord {
		user = e.landlord
	}
	ticket, err := e.tickets.Issue(context.Background(), user.String(), "", role)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(e.server.URL + "/v1/realtime/stream?ticket=" + ticket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
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

// expectLine waits for a line containing want and returns the lines it passed on the way.
func expectLine(t *testing.T, in <-chan string, want string) []string {
	t.Helper()
	var skipped []string
	deadline := time.After(2 * time.Second)
	for {
		select {
		case line, open := <-in:
			if !open {
				t.Fatalf("stream ended before %q", want)
			}
			if strings.Contains(line, want) {
				return skipped
			}
			skipped = append(skipped, line)
		case <-deadline:
			t.Fatalf("no %q within 2s", want)
			return skipped
		}
	}
}

func TestSentMessageArrivesOnTheOwnersStream(t *testing.T) {
	e := newRealtimeEnv(t)
	tenantStream := e.open(t, authz.RoleTenant, false)
	landlordStream := e.open(t, authz.RoleLandlord, true)
	expectLine(t, tenantStream, "event: ready")
	expectLine(t, landlordStream, "event: ready")

	// A tenant-side message for the tenant; the landlord (a different person) must not see it.
	e.send(t, e.tenant, "invoice_sent")
	expectLine(t, tenantStream, "event: notification")
	expectLine(t, tenantStream, `"kind":"invoice_sent"`)

	// A landlord-side message for the account that is both: only the stream acting as landlord gets it.
	e.send(t, e.landlord, "payment_submitted")
	expectLine(t, landlordStream, `"kind":"payment_submitted"`)
}

func (e *realtimeEnv) send(t *testing.T, user uuid.UUID, kind string) {
	t.Helper()
	if err := e.inbox.deliveries.Send(context.Background(), messageFor(user, kind)); err != nil {
		t.Fatal(err)
	}
}

func TestStreamOfTheOtherRoleSkipsTheMessage(t *testing.T) {
	e := newRealtimeEnv(t)
	asTenant := e.open(t, authz.RoleTenant, true) // the landlord account acting as a tenant
	asLandlord := e.open(t, authz.RoleLandlord, true)
	expectLine(t, asTenant, "event: ready")
	expectLine(t, asLandlord, "event: ready")

	e.send(t, e.landlord, "payment_submitted") // landlord-side kind
	expectLine(t, asLandlord, `"kind":"payment_submitted"`)
	e.send(t, e.landlord, "invoice_sent") // tenant-side kind
	skipped := expectLine(t, asTenant, `"kind":"invoice_sent"`)
	for _, line := range skipped {
		if strings.Contains(line, "payment_submitted") {
			t.Fatalf("the tenant-mode stream got a landlord-side message: %q", line)
		}
	}
}
