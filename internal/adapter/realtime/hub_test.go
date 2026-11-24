package realtime

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func envelope(audience string, users ...uuid.UUID) Envelope {
	return Envelope{UserIDs: users, Audience: audience, Name: "notification", Data: json.RawMessage(`{"kind":"invoice_sent"}`)}
}

func mustSubscribe(t *testing.T, hub *Hub, user uuid.UUID, role string) (<-chan Frame, func()) {
	t.Helper()
	events, cancel, err := hub.Subscribe(user, role)
	if err != nil {
		t.Fatal(err)
	}
	return events, cancel
}

func receive(t *testing.T, events <-chan Frame) Frame {
	t.Helper()
	select {
	case frame, ok := <-events:
		if !ok {
			t.Fatal("stream closed")
		}
		return frame
	case <-time.After(time.Second):
		t.Fatal("no frame")
		return Frame{}
	}
}

func expectNothing(t *testing.T, events <-chan Frame) {
	t.Helper()
	select {
	case frame := <-events:
		t.Fatalf("unexpected frame %+v", frame)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestDeliverReachesEveryStreamOfTheUser(t *testing.T) {
	hub := NewHub(5)
	user, other := uuid.New(), uuid.New()
	first, _ := mustSubscribe(t, hub, user, "tenant")
	second, _ := mustSubscribe(t, hub, user, "tenant")
	stranger, _ := mustSubscribe(t, hub, other, "tenant")

	hub.Deliver(envelope("", user))

	if got := receive(t, first); got.Name != "notification" || string(got.Data) != `{"kind":"invoice_sent"}` {
		t.Fatalf("frame %+v", got)
	}
	receive(t, second)
	expectNothing(t, stranger)
}

func TestAudienceFiltersByRole(t *testing.T) {
	hub := NewHub(5)
	user := uuid.New()
	asTenant, _ := mustSubscribe(t, hub, user, "tenant")
	asLandlord, _ := mustSubscribe(t, hub, user, "landlord")
	asAdmin, _ := mustSubscribe(t, hub, user, "admin")

	hub.Deliver(envelope("tenant", user))

	receive(t, asTenant)
	expectNothing(t, asLandlord)
	receive(t, asAdmin) // staff acts in no inbox side and gets everything addressed to them
}

func TestSubscribeCapsStreamsPerUser(t *testing.T) {
	hub := NewHub(2)
	user := uuid.New()
	_, cancel := mustSubscribe(t, hub, user, "tenant")
	mustSubscribe(t, hub, user, "tenant")
	if _, _, err := hub.Subscribe(user, "tenant"); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("third stream: %v", err)
	}
	cancel()
	if _, _, err := hub.Subscribe(user, "tenant"); err != nil {
		t.Fatalf("a freed slot must be usable: %v", err)
	}
}

func TestSlowConsumerIsDropped(t *testing.T) {
	hub := NewHub(5)
	user := uuid.New()
	events, _ := mustSubscribe(t, hub, user, "tenant")
	for range bufferedFrames + 1 {
		hub.Deliver(envelope("", user))
	}
	drained := 0
	for range events { // ends because the hub closed the channel
		drained++
	}
	if drained != bufferedFrames {
		t.Fatalf("drained %d frames, want %d", drained, bufferedFrames)
	}
	if _, _, err := hub.Subscribe(user, "tenant"); err != nil {
		t.Fatalf("the dropped stream must free its slot: %v", err)
	}
}

func TestCloseEndsEveryStreamAndRefusesNewOnes(t *testing.T) {
	hub := NewHub(5)
	events, cancel := mustSubscribe(t, hub, uuid.New(), "tenant")
	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	hub.Close()
	hub.Close()
	cancel() // after Close: must not panic
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not end the stream")
	}
	if _, _, err := hub.Subscribe(uuid.New(), "tenant"); !errors.Is(err, ErrClosed) {
		t.Fatalf("after Close: %v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	hub := NewHub(50)
	user := uuid.New()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, cancel, err := hub.Subscribe(user, "tenant")
			if err != nil {
				return
			}
			defer cancel()
			hub.Deliver(envelope("", user))
			select {
			case <-events:
			default:
			}
		}()
	}
	wg.Wait()
	hub.Close()
}
