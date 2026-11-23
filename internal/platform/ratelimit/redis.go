package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Limiter whose counters live in Redis, so the limit holds across every instance of the API.
type Redis struct {
	client *redis.Client
	prefix string
	script *redis.Script
}

// NewRedis returns a limiter that stores its keys under prefix (for example "rental-management:").
func NewRedis(client *redis.Client, prefix string) *Redis {
	return &Redis{client: client, prefix: prefix, script: redis.NewScript(incrWithExpiry)}
}

// Allow counts the request with INCR, and starts the window with EXPIRE the first time the key is seen.
func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	full := r.prefix + "rl:" + key

	// One round trip: the script both counts and reads the time left, so a crash between the two calls
	// cannot leave a counter without an expiry.
	res, err := r.script.Run(ctx, r.client, []string{full}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("rate limit %q: %w", key, err)
	}
	count, ttlMS := res[0], res[1]
	if count > int64(limit) {
		return Result{Allowed: false, RetryAfter: time.Duration(ttlMS) * time.Millisecond}, nil
	}
	return Result{Allowed: true}, nil
}

// incrWithExpiry is the script: it returns {count, remaining ms}. The expiry is set only when the key is new.
const incrWithExpiry = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {count, ttl}
`
