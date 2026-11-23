//go:build integration

package ratelimit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"rental-management/internal/platform/testdb"
)

var rds *testdb.Redis

func TestMain(m *testing.M) {
	var err error
	rds, err = testdb.StartRedis(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	rds.Terminate()
	os.Exit(code)
}

func TestRedisLimitsAndExpires(t *testing.T) {
	ctx := context.Background()
	l := NewRedis(rds.Client(t), "test:")
	window := 400 * time.Millisecond

	for i := 1; i <= 2; i++ {
		if res, err := l.Allow(ctx, "k", 2, window); err != nil || !res.Allowed {
			t.Fatalf("request %d: %+v %v", i, res, err)
		}
	}
	res, err := l.Allow(ctx, "k", 2, window)
	if err != nil || res.Allowed || res.RetryAfter <= 0 || res.RetryAfter > window {
		t.Fatalf("third request must be refused with a wait: %+v %v", res, err)
	}
	if res, _ := l.Allow(ctx, "other", 2, window); !res.Allowed {
		t.Error("another key has its own counter")
	}

	time.Sleep(window + 100*time.Millisecond)
	if res, _ := l.Allow(ctx, "k", 2, window); !res.Allowed {
		t.Error("the window ended, the key must be allowed again")
	}
}

func TestRedisErrorIsReturned(t *testing.T) {
	client := rds.Client(t)
	l := NewRedis(client, "test:")
	_ = client.Close()
	if _, err := l.Allow(context.Background(), "k", 1, time.Second); err == nil {
		t.Error("a closed client must give an error, not an answer")
	}
}
