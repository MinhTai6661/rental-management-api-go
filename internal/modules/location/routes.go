package location

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/httpx"
)

// Default rate limit of the public location routes, per client address.
const (
	DefaultLimit  = 60
	DefaultWindow = time.Minute
)

// Options configures the module's routes.
type Options struct {
	// Limiter counts requests; Limit requests per Window are allowed for each client address.
	Limiter httpx.Limiter
	Limit   int
	Window  time.Duration
}

// Mount returns the function that adds the module's routes to the /v1 group. The routes are public (the sign-up
// form needs them before there is an account), so they are rate limited instead.
func Mount(store *Store, opts Options) func(v1 *gin.RouterGroup) {
	return mount(store, opts)
}

func mount(store reader, opts Options) func(v1 *gin.RouterGroup) {
	h := handler{store: store}
	return func(v1 *gin.RouterGroup) {
		g := v1.Group("", httpx.RateLimit(opts.Limiter, "locations", opts.Limit, opts.Window))
		g.GET("/provinces", h.listProvinces)
		g.GET("/provinces/:code/wards", h.listWards)
	}
}
