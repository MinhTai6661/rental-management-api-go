package httpx

import (
	"context"
	"math"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/apperr"
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
		res, err := l.Allow(c.Request.Context(), bucket+":"+c.ClientIP(), limit, window)
		if err != nil {
			Fail(c, apperr.Busy(err))
			return
		}
		if !res.Allowed {
			Fail(c, apperr.RateLimited(int(math.Ceil(res.RetryAfter.Seconds()))))
			return
		}
		c.Next()
	}
}
