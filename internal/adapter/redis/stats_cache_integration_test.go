//go:build integration

package redisstore

import (
	"context"
	"testing"
	"time"
)

func TestStatsCacheKeepsAnAnswerUntilItExpires(t *testing.T) {
	cache, ctx := NewStatsCache(rds.Client(t), "test:"), context.Background()
	type answer struct {
		Billed int64
		Rate   *float64
	}
	zero := 0.0
	var got answer
	if hit, err := cache.Get(ctx, "stats:x", &got); err != nil || hit {
		t.Fatalf("nothing yet: hit=%v err=%v", hit, err)
	}
	if err := cache.Set(ctx, "stats:x", answer{Billed: 7, Rate: &zero}, 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if hit, err := cache.Get(ctx, "stats:x", &got); err != nil || !hit || got.Billed != 7 || got.Rate == nil || *got.Rate != 0 {
		t.Fatalf("a 0 stays a 0, not null: hit=%v err=%v %+v", hit, err, got)
	}
	time.Sleep(250 * time.Millisecond)
	if hit, _ := cache.Get(ctx, "stats:x", &got); hit {
		t.Error("it expired")
	}
}
