package planhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountPlans adds the plan routes to the /v1 group. Every route needs a signed in caller. A user reads the catalog and
// manages their own plan whatever role they act in; admins (permission subscriptions.manage) activate requests and
// hand plans out; only a super admin edits the catalog. auth turns a bearer token into the caller; limiter counts the
// plan requests (a paid one notifies the admins).
func MountPlans(v1 *gin.RouterGroup, h *PlanHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	request := httpx.RateLimit(limiter, "subscription-request", 20, time.Hour)

	signed := v1.Group("", signedIn)
	signed.GET("/plans", h.Catalog)
	signed.GET("/my/subscription", h.Mine)
	signed.POST("/my/subscription", request, h.Subscribe)
	signed.DELETE("/my/subscription", h.CancelMine)

	admin := v1.Group("/admin", signedIn)
	admin.GET("/subscriptions", can("subscriptions.manage"), h.ListSubscriptions)
	admin.POST("/subscriptions/:id/activate", can("subscriptions.manage"), h.Activate)
	admin.PUT("/users/:id/subscription", can("subscriptions.manage"), h.Assign)
	admin.DELETE("/users/:id/subscription", can("subscriptions.manage"), h.Revoke)

	catalog := v1.Group("/admin", signedIn, httpx.RequireRole(authz.RoleSuperAdmin))
	catalog.GET("/plans", can("subscriptions.manage"), h.ListPlans)
	catalog.POST("/plans", can("subscriptions.manage"), h.CreatePlan)
	catalog.PUT("/plans/:id", can("subscriptions.manage"), h.UpdatePlan)
	catalog.PATCH("/plans/:id", can("subscriptions.manage"), h.UpdatePlan)
}
