//go:build integration

package ratelimit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"rental-management/pkg/testdb"
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

func TestRedisLimitsAndRecovers(t *testing.T) {
	ctx := context.Background()
	l := NewRedis(rds.Client(t), "test:")
	// 5 per 500ms: a burst of 5, then one more request every 100ms.
	const limit, window = 5, 500 * time.Millisecond

	for i := 1; i <= limit; i++ {
		if res, err := l.Allow(ctx, "k", limit, window); err != nil || !res.Allowed {
			t.Fatalf("request %d: %+v %v", i, res, err)
		}
	}
	res, err := l.Allow(ctx, "k", limit, window)
	if err != nil || res.Allowed || res.RetryAfter <= 0 || res.RetryAfter > window {
		t.Fatalf("the request over the limit must be refused with a wait: %+v %v", res, err)
	}
	if res, _ := l.Allow(ctx, "other", limit, window); !res.Allowed {
		t.Error("another key has its own budget")
	}

	time.Sleep(res.RetryAfter + 50*time.Millisecond)
	if res, _ := l.Allow(ctx, "k", limit, window); !res.Allowed {
		t.Error("after the wait the key must be allowed again")
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
