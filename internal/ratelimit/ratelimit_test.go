package ratelimit

import (
	"context"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestMemoryLimitsPerKeyAndResetsAfterTheWindow(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	m := NewMemory(clock.now)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		res, err := m.Allow(ctx, "a", 3, time.Minute)
		if err != nil || !res.Allowed {
			t.Fatalf("request %d should pass: %+v %v", i, res, err)
		}
		clock.t = clock.t.Add(5 * time.Second)
	}
	res, _ := m.Allow(ctx, "a", 3, time.Minute)
	if res.Allowed {
		t.Fatal("4th request in the window must be refused")
	}
	// The window started 15s ago, so 45s are left.
	if res.RetryAfter != 45*time.Second {
		t.Errorf("RetryAfter = %s, want 45s", res.RetryAfter)
	}

	// Another key has its own counter.
	if res, _ := m.Allow(ctx, "b", 3, time.Minute); !res.Allowed {
		t.Error("a different key must not be limited")
	}

	clock.t = clock.t.Add(time.Minute)
	if res, _ := m.Allow(ctx, "a", 3, time.Minute); !res.Allowed {
		t.Error("the window ended, the key must be allowed again")
	}
}

func TestMemoryPurgesEndedWindows(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	m := NewMemory(clock.now)
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		_, _ = m.Allow(ctx, key, 1, 10*time.Second)
	}
	clock.t = clock.t.Add(2 * purgeEvery)
	_, _ = m.Allow(ctx, "d", 1, 10*time.Second)
	if len(m.counters) != 1 {
		t.Errorf("counters left = %d, want only the new key", len(m.counters))
	}
}
