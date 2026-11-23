// Package ratelimit counts requests per key in fixed windows. Two stores implement it: Memory (one process, no
// dependencies) and Redis (shared by every instance of the API).
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Result is the answer to one request.
type Result struct {
	// Allowed is false once the key has used up its requests in the current window.
	Allowed bool
	// RetryAfter is how long until the window ends; it is meaningful when Allowed is false.
	RetryAfter time.Duration
}

// Limiter counts one request for key against limit requests per window.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error)
}

// purgeEvery is how often Memory drops windows that have ended, so the map cannot grow without bound.
const purgeEvery = time.Minute

type counter struct {
	count   int
	resetAt time.Time
}

// Memory is a Limiter that keeps its counters in the process. It is exact for one instance and forgets everything
// on restart; use Redis when several instances serve the API.
type Memory struct {
	now func() time.Time

	mu        sync.Mutex
	counters  map[string]*counter
	lastPurge time.Time
}

// NewMemory returns an empty in-memory limiter. now is the clock; pass time.Now in production.
func NewMemory(now func() time.Time) *Memory {
	return &Memory{now: now, counters: map[string]*counter{}, lastPurge: now()}
}

// Allow counts the request.
func (m *Memory) Allow(_ context.Context, key string, limit int, window time.Duration) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if now.Sub(m.lastPurge) >= purgeEvery {
		for k, c := range m.counters {
			if !now.Before(c.resetAt) {
				delete(m.counters, k)
			}
		}
		m.lastPurge = now
	}

	c, ok := m.counters[key]
	if !ok || !now.Before(c.resetAt) {
		c = &counter{resetAt: now.Add(window)}
		m.counters[key] = c
	}
	c.count++
	if c.count > limit {
		return Result{Allowed: false, RetryAfter: c.resetAt.Sub(now)}, nil
	}
	return Result{Allowed: true}, nil
}
