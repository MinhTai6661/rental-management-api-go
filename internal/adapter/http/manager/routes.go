package managerhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountManagers adds the manager routes to the /v1 group. Every route needs a signed in caller acting in the right role
// and holding the permission of the route; every query is scoped to the caller (somebody else's invitation or hand-over
// is a 404, never a 403). limiter counts the invitations per account.
func MountManagers(v1 *gin.RouterGroup, inv *InvitationHandler, as *AssignmentHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	landlord := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord))
	tenant := v1.Group("", signedIn, httpx.RequireRole(authz.RoleTenant))
	manager := v1.Group("", signedIn, httpx.RequireRole(authz.RoleManager))
	// Making an invitation and making a new link share one allowance.
	issue := httpx.RateLimitPerUser(limiter, "manager-invitations-store", 30, time.Hour)
	copyLink := httpx.RateLimitPerUser(limiter, "manager-invitations-link", 60, time.Minute)
	// Seeing, accepting and declining an invitation share one allowance, so a link cannot be guessed at speed.
	answer := httpx.RateLimitPerUser(limiter, "manager-invitation-answer", 20, time.Minute)

	landlord.GET("/manager-capabilities", can("managers.manage"), as.Capabilities)
	landlord.GET("/my/manager-invitations", can("managers.manage"), inv.List)
	landlord.POST("/my/manager-invitations", can("managers.manage"), issue, inv.Create)
	landlord.GET("/my/manager-invitations/:id/link", can("managers.manage"), copyLink, inv.Link)
	landlord.POST("/my/manager-invitations/:id/regenerate", can("managers.manage"), issue, inv.Regenerate)
	landlord.DELETE("/my/manager-invitations/:id", can("managers.manage"), inv.Revoke)

	landlord.GET("/my/room-managers", can("managers.manage"), as.List)
	landlord.POST("/my/room-managers/bulk", can("managers.manage"), as.Bulk)
	landlord.PUT("/my/room-managers/:id", can("managers.manage"), as.Update)
	landlord.DELETE("/my/room-managers/:id", can("managers.manage"), as.Revoke)

	// The invited person's side: the rooms are handed over on accept.
	tenant.POST("/manager-invitations/preview", can("manager_invitations.respond"), answer, inv.Preview)
	tenant.POST("/manager-invitations/accept", can("manager_invitations.respond"), answer, inv.Accept)
	tenant.POST("/manager-invitations/decline", can("manager_invitations.respond"), answer, inv.Decline)
	tenant.GET("/me/manager-invitations", can("manager_invitations.respond"), inv.Inbox)

	// A manager only reads what was handed to them: they never give a room up.
	manager.GET("/my/managed-rooms", can("rooms.view"), as.Managed)
}
