package main

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/i18n"
)

// readyTimeout bounds how long /readyz waits for each dependency.
const readyTimeout = 2 * time.Second

// routerDeps is everything the router needs.
type routerDeps struct {
	Log            *slog.Logger
	Catalog        *i18n.Catalog
	RequestTimeout time.Duration
	// CORSOrigins are the web origins allowed to call the API from a browser.
	CORSOrigins []string
	// Modules mount their routes on the /v1 group.
	Modules []func(v1 *gin.RouterGroup)
	// Ready are the dependency checks behind /readyz (database, Redis...).
	Ready []func(context.Context) error
}

// newRouter builds the HTTP engine: the middleware chain, the health endpoints, the /v1 group with the modules'
// routes, and the JSON 404/405 answers.
func newRouter(d routerDeps) *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	// No proxy is trusted for now, so a client cannot choose its own address with X-Forwarded-For
	// (the rate limits key on it). Behind a reverse proxy this becomes a setting.
	_ = r.SetTrustedProxies(nil)

	// Order matters: CORS answers preflights before anything else, the locale must be known before
	// anything can fail, and the access log wraps recovery so panics are logged with their final status.
	r.Use(
		httpx.RequestID(d.Log),
		httpx.CORS(d.CORSOrigins),
		httpx.SecurityHeaders(),
		httpx.Locale(d.Catalog),
		httpx.AccessLog(),
		httpx.Recover(),
		httpx.Timeout(d.RequestTimeout),
	)

	r.NoRoute(func(c *gin.Context) { httpx.Fail(c, apperr.E(apperr.KindNotFound, apperr.CodeRouteNotFound)) })
	r.NoMethod(methodNotAllowed)

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", ready(d.Ready))

	v1 := r.Group("/v1")
	for _, mount := range d.Modules {
		mount(v1)
	}
	return r
}

// ready answers 200 when every dependency responds, otherwise a 503.
func ready(checks []func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()
		for _, check := range checks {
			if err := check(ctx); err != nil {
				httpx.Fail(c, apperr.Busy(err))
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// methodNotAllowed answers 405 with the methods the route does support in the Allow header.
func methodNotAllowed(c *gin.Context) {
	allowed := strings.Split(c.Writer.Header().Get("Allow"), ", ")
	slices.Sort(allowed)
	httpx.Fail(c, apperr.MethodNotAllowed(strings.Join(allowed, ", ")))
}
