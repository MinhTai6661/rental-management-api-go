package notificationhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	"rental-management/pkg/authz"
)

// MountNotifications adds the inbox routes to the /v1 group. They need a signed in caller in any role and no
// permission: everybody has an inbox, and every query is scoped to the caller. They are not rate limited (the web polls
// the bell on a schedule and bulk is capped at 100 ids).
func MountNotifications(v1 *gin.RouterGroup, h *InboxHandler, auth httpx.Authenticator) {
	n := v1.Group("/notifications", httpx.RequireAuth(auth))
	n.GET("", h.List)
	n.GET("/latest", h.Latest)
	n.GET("/unread-count", h.UnreadCount)
	n.POST("/read-all", h.ReadAll)
	n.POST("/bulk", h.Bulk)
	n.POST("/:id/read", h.Read)
	n.DELETE("/:id", h.Delete)
	n.DELETE("", h.Clear)
}

// MountAnnouncements adds the announcement routes to the /v1 group: the platform's messages for admins and super
// admins (permission `announcements.send`) and a landlord's messages to their own people (landlord role, permission
// `announcements.send_to_tenants`). Both groups use one handler, which reads the sender from the caller, so a message
// that is not theirs is a 404. A landlord's creating and publishing are limited per hour; counting an audience per
// minute.
func MountAnnouncements(v1 *gin.RouterGroup, h *AnnouncementHandler, images *ImageHandler, stats *StatsHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	signedIn := httpx.RequireAuth(auth)
	reach := httpx.RateLimitPerUser(limiter, "announcements-reach", 120, time.Minute)
	store := httpx.RateLimitPerUser(limiter, "announcements-store", 30, time.Hour)
	upload := httpx.RateLimitPerUser(limiter, "announcements-images", 60, time.Hour)

	admin := v1.Group("/admin/announcements", signedIn, httpx.RequireRole(authz.RoleSuperAdmin, authz.RoleAdmin), httpx.RequirePermission("announcements.send"))
	landlord := v1.Group("/my/announcements", signedIn, httpx.RequireRole(authz.RoleLandlord), httpx.RequirePermission("announcements.send_to_tenants"))
	for _, g := range []struct {
		group *gin.RouterGroup
		limit gin.HandlerFunc
	}{{admin, passThrough}, {landlord, store}} {
		g.group.GET("", h.List)
		g.group.POST("", g.limit, h.Create)
		g.group.POST("/reach", reach, h.Reach)
		g.group.GET("/stats", stats.Overview)
		g.group.GET("/:id", h.Get)
		g.group.GET("/:id/stats", stats.Detail)
		g.group.POST("/:id/images", upload, images.Upload)
		g.group.PUT("/:id/images", images.Arrange)
		g.group.DELETE("/:id/images/:image_id", images.Delete)
		g.group.PUT("/:id", h.Update)
		g.group.POST("/:id/publish", g.limit, h.Publish)
		g.group.POST("/:id/unpublish", h.Unpublish)
		g.group.DELETE("/:id", h.Recall)
	}
	landlord.GET("/people", h.People)
}

// passThrough is the limit of a group that has none.
func passThrough(c *gin.Context) { c.Next() }

// MountPopups adds the popup routes to the /v1 group: which popups to show the signed in person now, and what they did
// with one. Recording is limited per person per minute.
func MountPopups(v1 *gin.RouterGroup, h *PopupHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	record := httpx.RateLimitPerUser(limiter, "popups-record", 120, time.Minute)
	p := v1.Group("/popups", httpx.RequireAuth(auth))
	p.GET("/active", h.Active)
	p.POST("/:id/seen", record, h.Record(domain.ActionSeen))
	p.POST("/:id/dismiss", record, h.Record(domain.ActionDismiss))
	p.POST("/:id/click", record, h.Record(domain.ActionClick))
}

// MountRealtime adds the realtime routes to the /v1 group: a ticket for a signed in caller (limited per person per
// minute) and the event stream, which signs in by that ticket (or by a bearer token) itself.
func MountRealtime(v1 *gin.RouterGroup, h *StreamHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	ticket := httpx.RateLimitPerUser(limiter, "realtime-ticket", 30, time.Minute)
	r := v1.Group("/realtime")
	r.POST("/ticket", httpx.RequireAuth(auth), ticket, h.Ticket)
	r.GET("/stream", httpx.AuthenticateIfPresent(auth), h.Stream)
}
