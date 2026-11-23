package httpx

import (
	"context"
	"math"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/pkg/ratelimit"
)

// Limiter is what the RateLimit middleware needs; the ratelimit package implements it.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Result, error)
}

// RateLimit allows each client address limit requests per window on the routes it guards. bucket names the
// group of routes sharing the counter ("locations"). A client over the limit gets 429 with Retry-After; a limiter
// that fails (Redis down) answers 503 rather than letting everything through.
func RateLimit(l Limiter, bucket string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !allow(c, l, bucket+":"+c.ClientIP(), limit, window) {
			return
		}
		c.Next()
	}
}

// RateLimitPerUser is RateLimit counted per signed-in account instead of per address, so one landlord behind a shared
// connection cannot use up another one's allowance. Put it after RequireAuth: without a caller it answers 401.
func RateLimitPerUser(l Limiter, bucket string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalOf(c)
		if !ok {
			Fail(c, apperr.Unauthenticated())
			return
		}
		if !allow(c, l, bucket+":u:"+p.UserID, limit, window) {
			return
		}
		c.Next()
	}
}

// allow counts one request against key and answers the request itself (and returns false) when it must stop.
func allow(c *gin.Context, l Limiter, key string, limit int, window time.Duration) bool {
	res, err := l.Allow(c.Request.Context(), key, limit, window)
	if err != nil {
		Fail(c, apperr.Busy(err))
		return false
	}
	if !res.Allowed {
		Fail(c, apperr.RateLimited(int(math.Ceil(res.RetryAfter.Seconds()))))
		return false
	}
	return true
}
