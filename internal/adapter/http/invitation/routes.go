package invitationhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountInvitees adds the routes every kind of invitation shares to the /v1 group:
//   - POST /invitee-lookup: a landlord (permission `contracts.create`) finds the account to invite by its exact email
//     or id, 30 a landlord per hour;
//   - POST /invitation-emails/opt-out: public, the link of an invitation email, limited per client address.
func MountInvitees(v1 *gin.RouterGroup, h *InviteeHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	v1.POST("/invitee-lookup", httpx.RequireAuth(auth), httpx.RequireRole(authz.RoleLandlord),
		httpx.RequirePermission("contracts.create"), httpx.RateLimitPerUser(limiter, "invitee-lookup", 30, time.Hour), h.Lookup)
	v1.POST("/invitation-emails/opt-out", httpx.RateLimit(limiter, "invitation-opt-out", 10, time.Minute), h.OptOut)
}
