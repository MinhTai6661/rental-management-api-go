package httpadapter

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/httpx"
)

// Default rate limit of the public location routes, per client address.
const (
	LocationDefaultLimit  = 60
	LocationDefaultWindow = time.Minute
)

// MountLocations adds the location routes to the /v1 group. They are public (the sign-up form needs them before
// there is an account), so they are rate limited instead: limit requests per window for each client address.
func MountLocations(v1 *gin.RouterGroup, h *LocationHandler, limiter httpx.Limiter, limit int, window time.Duration) {
	g := v1.Group("", httpx.RateLimit(limiter, "locations", limit, window))
	g.GET("/provinces", h.ListProvinces)
	g.GET("/provinces/:code/wards", h.ListWards)
}
