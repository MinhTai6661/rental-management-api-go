// Package locationhttp is the HTTP side of the location use cases: the public province and ward routes.
package locationhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
)

// Default rate limit of the public location routes, per client address.
const (
	DefaultLimit  = 60
	DefaultWindow = time.Minute
)

// MountLocations adds the location routes to the /v1 group. They are public (the sign-up form needs them before
// there is an account), so they are rate limited instead: limit requests per window for each client address.
func MountLocations(v1 *gin.RouterGroup, h *LocationHandler, limiter httpx.Limiter, limit int, window time.Duration) {
	g := v1.Group("", httpx.RateLimit(limiter, "locations", limit, window))
	g.GET("/provinces", h.ListProvinces)
	g.GET("/provinces/:code/wards", h.ListWards)
}
