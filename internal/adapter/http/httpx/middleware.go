package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/pkg/i18n"
)

const requestIDHeader = "X-Request-Id"

// validRequestID accepts the ids other tools send (uuid, nginx's hex...) and rejects anything odd.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID gives every request an id (the caller's X-Request-Id when it looks sane, else a new uuid),
// echoes it in the response and puts a logger carrying it in the context.
func RequestID(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !validRequestID.MatchString(id) {
			id = uuid.Must(uuid.NewV7()).String()
		}
		c.Header(requestIDHeader, id)
		c.Set(loggerKey, base.With("request_id", id))
		c.Next()
	}
}

// SecurityHeaders adds the headers that are safe for an API that only serves JSON.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// Locale picks the language of the request from Accept-Language for the messages of the response.
func Locale(catalog *i18n.Catalog) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(localizerKey, catalog.Localizer(i18n.Negotiate(c.GetHeader("Accept-Language"))))
		c.Next()
	}
}

// AccessLog logs one line per request, after it has been answered.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		Log(c).Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

// Recover turns a panic into a logged 500 so one bad request cannot take the process down.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { //nolint:errorlint // net/http documents comparing the value itself
				panic(rec)
			}
			Log(c).Error("panic", "panic", rec, "stack", string(debug.Stack()))
			Fail(c, apperr.Internal(errors.New("panic")))
		}()
		c.Next()
	}
}

// Timeout gives every request a deadline. Handlers must pass c.Request.Context() to whatever they call;
// when the deadline passes and nothing was written yet, the client gets the 503 "server.busy" error.
func Timeout(limit time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), limit)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && !c.Writer.Written() {
			Fail(c, apperr.Busy(ctx.Err()))
		}
	}
}
