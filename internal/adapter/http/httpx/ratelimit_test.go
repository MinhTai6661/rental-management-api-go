package httpx

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/authz"
	"rental-management/pkg/ratelimit"
)

// fakeLimiter answers every request with the same canned result.
type fakeLimiter struct {
	res ratelimit.Result
	err error
	key string
}

func (f *fakeLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (ratelimit.Result, error) {
	f.key = key
	return f.res, f.err
}

func rateLimited(t *testing.T, l Limiter) *gin.Engine {
	t.Helper()
	return testEngine(t, func(r *gin.Engine) {
		r.GET("/x", RateLimit(l, "bucket", 5, time.Minute), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	})
}

func TestRateLimitAllowsAndKeysByBucketAndClient(t *testing.T) {
	l := &fakeLimiter{res: ratelimit.Result{Allowed: true}}
	if rec := call(rateLimited(t, l), http.MethodGet, "/x", nil); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if l.key != "bucket:192.0.2.1" {
		t.Errorf("key %q", l.key)
	}
}

func TestRateLimitRefusesWithRoundedUpRetryAfter(t *testing.T) {
	l := &fakeLimiter{res: ratelimit.Result{Allowed: false, RetryAfter: 1200 * time.Millisecond}}
	rec := call(rateLimited(t, l), http.MethodGet, "/x", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// A limiter that cannot answer (Redis down) must refuse the request, never let it through.
func TestRateLimitFailsClosedWhenTheLimiterFails(t *testing.T) {
	l := &fakeLimiter{err: errors.New("redis: connection refused")}
	rec := call(rateLimited(t, l), http.MethodGet, "/x", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

// fixedCaller accepts every bearer token as the same caller.
type fixedCaller struct{ userID string }

func (s fixedCaller) Authenticate(context.Context, string) (authz.Principal, error) {
	return authz.Principal{UserID: s.userID, Role: authz.RoleLandlord}, nil
}

func perUser(t *testing.T, l Limiter) *gin.Engine {
	t.Helper()
	return testEngine(t, func(r *gin.Engine) {
		r.GET("/x", RequireAuth(fixedCaller{userID: "u-1"}), RateLimitPerUser(l, "bucket", 5, time.Minute),
			func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		// No RequireAuth in front: there is no caller to count.
		r.GET("/anon", RateLimitPerUser(l, "bucket", 5, time.Minute), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	})
}

var bearer = map[string]string{"Authorization": "Bearer t"}

func TestRateLimitPerUserKeysByBucketAndAccount(t *testing.T) {
	l := &fakeLimiter{res: ratelimit.Result{Allowed: true}}
	if rec := call(perUser(t, l), http.MethodGet, "/x", bearer); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if l.key != "bucket:u:u-1" {
		t.Errorf("key %q", l.key)
	}
}

func TestRateLimitPerUserRefusesWithRetryAfter(t *testing.T) {
	l := &fakeLimiter{res: ratelimit.Result{Allowed: false, RetryAfter: 3 * time.Second}}
	rec := call(perUser(t, l), http.MethodGet, "/x", bearer)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "3" {
		t.Errorf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestRateLimitPerUserFailsClosedAndNeedsACaller(t *testing.T) {
	failing := &fakeLimiter{err: errors.New("redis down")}
	if rec := call(perUser(t, failing), http.MethodGet, "/x", bearer); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("limiter error: status %d, want 503", rec.Code)
	}
	ok := &fakeLimiter{res: ratelimit.Result{Allowed: true}}
	if rec := call(perUser(t, ok), http.MethodGet, "/anon", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no caller: status %d, want 401", rec.Code)
	}
	if ok.key != "" {
		t.Errorf("limiter used without a caller: %q", ok.key)
	}
}
