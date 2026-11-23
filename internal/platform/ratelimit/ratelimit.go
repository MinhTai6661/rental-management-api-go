// Package ratelimit limits how often a client may call a route. It wraps redis_rate (the GCRA algorithm on Redis),
// so the limit holds across every instance of the API.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

// Result is the answer to one request.
type Result struct {
	// Allowed is false once the key has used up its requests.
	Allowed bool
	// RetryAfter is how long until the next request is permitted; it is meaningful when Allowed is false.
	RetryAfter time.Duration
}

// Limiter counts one request for key against limit requests per window.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error)
}

// Redis is a Limiter backed by redis_rate. A client may burst up to limit requests, after which requests are
// admitted at the steady rate of limit per window.
type Redis struct {
	limiter *redis_rate.Limiter
	prefix  string
}

// NewRedis returns a limiter that stores its keys under prefix (for example "rental-management:"). redis_rate adds
// its own "rate:" in front of every key.
func NewRedis(client *redis.Client, prefix string) *Redis {
	return &Redis{limiter: redis_rate.NewLimiter(client), prefix: prefix}
}

// Allow counts the request. An error (Redis down) is returned as is: callers must refuse the request, not allow it.
func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	res, err := r.limiter.Allow(ctx, r.prefix+"rl:"+key, redis_rate.Limit{Rate: limit, Burst: limit, Period: window})
	if err != nil {
		return Result{}, fmt.Errorf("rate limit %q: %w", key, err)
	}
	if res.Allowed == 0 {
		return Result{Allowed: false, RetryAfter: res.RetryAfter}, nil
	}
	return Result{Allowed: true}, nil
}
