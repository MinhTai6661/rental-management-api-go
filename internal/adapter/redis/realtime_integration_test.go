//go:build integration

package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/realtime"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/testdb"
)

var rds *testdb.Redis

func TestMain(m *testing.M) {
	var err error
	if rds, err = testdb.StartRedis(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	rds.Terminate()
	os.Exit(code)
}

func TestBroadcastReachesEveryListener(t *testing.T) {
	client := rds.Client(t)
	log := slog.New(slog.DiscardHandler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan realtime.Envelope, 4)
	for range 2 { // two "instances"
		listener := NewBroadcaster(client, "test:", log)
		go func() { _ = listener.Listen(ctx, func(env realtime.Envelope) { received <- env }) }()
	}
	sender := NewBroadcaster(client, "test:", log)
	user := uuid.New()
	sent := realtime.Envelope{UserIDs: []uuid.UUID{user}, Audience: "tenant", Name: "notification", Data: json.RawMessage(`{"kind":"invoice_sent"}`)}

	// The subscriptions open asynchronously: publish until both listeners have heard it.
	deadline := time.After(5 * time.Second)
	got := 0
	for got < 2 {
		if err := sender.Publish(ctx, sent); err != nil {
			t.Fatal(err)
		}
		select {
		case env := <-received:
			if len(env.UserIDs) != 1 || env.UserIDs[0] != user || env.Audience != "tenant" || env.Name != "notification" || string(env.Data) != `{"kind":"invoice_sent"}` {
				t.Fatalf("envelope changed on the way: %+v", env)
			}
			got++
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatalf("only %d listeners heard the event", got)
		}
	}
}

func TestListenStopsWithItsContext(t *testing.T) {
	client := rds.Client(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewBroadcaster(client, "test:", slog.New(slog.DiscardHandler)).Listen(ctx, func(realtime.Envelope) {})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return")
	}
}

func TestTicketWorksOnceAndExpires(t *testing.T) {
	client := rds.Client(t)
	ctx := context.Background()
	tickets := NewStreamTickets(client, "test:", time.Minute)
	token, err := tickets.Issue(ctx, "user-1", "session-1", "tenant")
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := client.Keys(ctx, "test:rt:ticket:*").Result()
	if len(keys) != 1 || keys[0] == "test:rt:ticket:"+token {
		t.Fatalf("only a hash of the ticket may be stored: %v", keys)
	}
	userID, sessionID, role, err := tickets.Redeem(ctx, token)
	if err != nil || userID != "user-1" || sessionID != "session-1" || role != "tenant" {
		t.Fatalf("redeem: %q %q %q %v", userID, sessionID, role, err)
	}
	if _, _, _, err := tickets.Redeem(ctx, token); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}
	if _, _, _, err := tickets.Redeem(ctx, "unknown"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("unknown ticket: %v", err)
	}

	short := NewStreamTickets(client, "test:", 100*time.Millisecond)
	token, _ = short.Issue(ctx, "user-2", "", "landlord")
	time.Sleep(300 * time.Millisecond)
	if _, _, _, err := short.Redeem(ctx, token); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("expired ticket: %v", err)
	}
}
