package listinghttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// publicLimit is how many requests per minute one client address may make to the public routes.
const publicLimit = 60

// MountListings adds the listing routes to the /v1 group: a landlord's own listings (landlord role, permission
// `my_listings.manage`; somebody else's listing is a 404) and the admins' review (permission `listings.review`: the
// admin and super admin roles hold it). auth turns a bearer token into the caller; limiter counts the creations and
// submissions.
func MountListings(v1 *gin.RouterGroup, h *ListingHandler, r *ReviewHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	landlord := v1.Group("/my/listings", signedIn, httpx.RequireRole(authz.RoleLandlord), can("my_listings.manage"))
	store := httpx.RateLimit(limiter, "listings-store", 30, time.Hour)
	submit := httpx.RateLimit(limiter, "listings-submit", 30, time.Hour)

	landlord.GET("", h.List)
	landlord.POST("", store, h.Create)
	landlord.GET("/:id", h.Get)
	landlord.PUT("/:id", h.Update)
	landlord.PATCH("/:id", h.Update)
	landlord.POST("/:id/submit", submit, h.Submit)
	landlord.POST("/:id/unpublish", h.Unpublish)
	landlord.DELETE("/:id", h.Delete)

	admin := v1.Group("/admin/listings", signedIn, can("listings.review"))
	admin.GET("", r.List)
	admin.POST("/bulk", r.Bulk)
	admin.GET("/:id", r.Get)
	admin.POST("/:id/approve", r.Approve)
	admin.POST("/:id/reject", r.Reject)
	admin.POST("/:id/unpublish", r.TakeDown)
}

// MountSearch adds the routes that show the rooms on find rooms: for a signed in renter (tenant role, permission
// `rooms.view`) the rooms with their address and the landlords; for anybody, no account, the public side only,
// limited per client address.
func MountSearch(v1 *gin.RouterGroup, h *OfferHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	tenant := v1.Group("", httpx.RequireAuth(auth), httpx.RequireRole(authz.RoleTenant), can("rooms.view"))
	tenant.GET("/vacant-rooms", h.List)
	tenant.GET("/vacant-rooms/:id", h.Get)
	tenant.GET("/landlords/:id", h.Landlord)

	public := v1.Group("/public", httpx.RateLimit(limiter, "public-browse", publicLimit, time.Minute))
	public.GET("/rooms", h.PublicList)
	public.GET("/rooms/:id", h.PublicGet)
	public.GET("/landlords/:id", h.PublicLandlord)
}
