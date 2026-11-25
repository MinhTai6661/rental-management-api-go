package contracthttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountContracts adds the contract routes to the /v1 group. Every route needs a signed in caller acting in the right
// role and holding the permission of the route; every query is scoped to the caller (somebody else's contract or
// invitation is a 404, never a 403). auth turns a bearer token into the caller; limiter counts the registrations and
// the invitations (per account, so one landlord never uses up another's allowance).
func MountContracts(v1 *gin.RouterGroup, h *ContractHandler, r *RentalHandler, inv *InvitationHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	both := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleTenant))
	landlord := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord))
	tenant := v1.Group("", signedIn, httpx.RequireRole(authz.RoleTenant))
	// A manager writes offers for the rooms handed to them (the use case checks the room); the landlord answers them.
	inviter := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleManager))
	register := httpx.RateLimit(limiter, "contract-register", 30, time.Hour)
	// Making an invitation and making a new link share one allowance.
	issue := httpx.RateLimitPerUser(limiter, "contract-invitation-create", 30, time.Hour)
	copyLink := httpx.RateLimitPerUser(limiter, "contract-invitation-link", 60, time.Minute)
	// Seeing, accepting and declining an offer share one allowance, so a link cannot be guessed at speed.
	answer := httpx.RateLimitPerUser(limiter, "contract-invitation-answer", 20, time.Minute)

	// A landlord sees the contracts on their rooms, a tenant their own.
	// A manager reads the contracts of the rooms handed to them (never the one they hold as tenant) and changes none.
	readers := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleManager, authz.RoleTenant))
	readers.GET("/contracts", can("contracts.view"), h.List)
	readers.GET("/contracts/:id", can("contracts.view"), h.Get)
	// A landlord removes any of their contracts; a tenant only withdraws a registration that still waits.
	both.DELETE("/contracts/:id", can("contracts.delete"), h.Delete)

	landlord.PUT("/contracts/order", can("contracts.update"), h.Reorder)
	landlord.POST("/contracts/bulk", can("contracts.update"), h.Bulk)
	landlord.PUT("/contracts/:id", can("contracts.update"), h.Update)
	landlord.PATCH("/contracts/:id", can("contracts.update"), h.Update)
	landlord.POST("/contracts/:id/terminate", can("contracts.update"), h.Terminate)
	landlord.POST("/contracts/:id/cancellation-request/approve", can("contracts.update"), h.ApproveCancellation)
	landlord.POST("/contracts/:id/cancellation-request/decline", can("contracts.update"), h.DeclineCancellation)

	// The landlord's offers of rooms by link.
	inviter.GET("/contract-invitations", can("contracts.create"), inv.List)
	inviter.POST("/contract-invitations", can("contracts.create"), issue, inv.Create)
	inviter.GET("/contract-invitations/:id", can("contracts.create"), inv.Get)
	inviter.GET("/contract-invitations/:id/link", can("contracts.create"), copyLink, inv.Link)
	inviter.POST("/contract-invitations/:id/regenerate", can("contracts.create"), issue, inv.Regenerate)
	inviter.DELETE("/contract-invitations/:id", can("contracts.create"), inv.Revoke)

	// The landlord answers the offers their managers wrote.
	landlord.POST("/contract-invitations/:id/approve", can("contracts.update"), inv.Approve)
	landlord.POST("/contract-invitations/:id/reject", can("contracts.update"), inv.Reject)

	// The invited person's side: the contract is made on accept, through the rules of every contract.
	tenant.POST("/contract-invitations/preview", can("contract_invitations.respond"), answer, inv.Preview)
	tenant.POST("/contract-invitations/accept", can("contract_invitations.respond"), answer, inv.Accept)
	tenant.POST("/contract-invitations/decline", can("contract_invitations.respond"), answer, inv.Decline)
	tenant.GET("/me/contract-invitations", can("contract_invitations.respond"), inv.Inbox)

	tenant.POST("/contracts/register", register, can("contracts.create"), h.Register)
	tenant.POST("/contracts/:id/cancellation-request", can("contracts.request_cancel"), h.RequestCancellation)
	tenant.DELETE("/contracts/:id/cancellation-request", can("contracts.request_cancel"), h.WithdrawCancellation)
	tenant.GET("/me/contracts", can("contracts.view"), r.List)
	tenant.GET("/me/contracts/:id", can("contracts.view"), r.Get)
}
