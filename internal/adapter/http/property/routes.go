// Package propertyhttp is the HTTP side of the property use cases: buildings, rooms and their pictures, all behind
// the landlord role.
package propertyhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountProperty adds the property routes to the /v1 group. Everything here belongs to the landlord: the caller must
// act in the landlord role and hold the permission of the route, and every query is scoped to them. auth turns a
// bearer token into the caller; limiter counts the picture uploads.
func MountProperty(v1 *gin.RouterGroup, b *BuildingHandler, r *RoomHandler, m *MediaHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	owner := v1.Group("", httpx.RequireAuth(auth), httpx.RequireRole(authz.RoleLandlord))
	can := httpx.RequirePermission
	upload := httpx.RateLimit(limiter, "property-media-upload", 60, time.Minute)

	buildings := owner.Group("/buildings")
	buildings.GET("", can("buildings.view"), b.List)
	buildings.POST("", can("buildings.create"), b.Create)
	buildings.PUT("/order", can("buildings.update"), b.Reorder)
	buildings.POST("/bulk-delete", can("buildings.delete"), b.BulkDelete)
	buildings.GET("/:id", can("buildings.view"), b.Get)
	buildings.PUT("/:id", can("buildings.update"), b.Update)
	buildings.DELETE("/:id", can("buildings.delete"), b.Delete)
	buildings.POST("/:id/copy", can("buildings.create"), b.Copy)
	buildings.POST("/:id/rooms", can("rooms.create"), r.CreateRun)
	buildings.POST("/:id/media", upload, can("buildings.update"), m.Upload(false))
	buildings.PUT("/:id/media", can("buildings.update"), m.Arrange(false))
	buildings.PUT("/:id/media/:media_id/file", upload, can("buildings.update"), m.ReplaceFile(false))
	buildings.DELETE("/:id/media/:media_id", can("buildings.update"), m.Delete(false))

	rooms := owner.Group("/rooms")
	rooms.GET("", can("rooms.view"), r.List)
	rooms.POST("", can("rooms.create"), r.Create)
	rooms.PUT("/order", can("rooms.update"), r.Reorder)
	rooms.POST("/bulk-delete", can("rooms.delete"), r.BulkDelete)
	rooms.POST("/bulk-status", can("rooms.update"), r.BulkStatus)
	rooms.GET("/:id", can("rooms.view"), r.Get)
	rooms.PUT("/:id", can("rooms.update"), r.Update)
	rooms.DELETE("/:id", can("rooms.delete"), r.Delete)
	rooms.POST("/:id/media", upload, can("rooms.update"), m.Upload(true))
	rooms.PUT("/:id/media", can("rooms.update"), m.Arrange(true))
	rooms.PUT("/:id/media/:media_id/file", upload, can("rooms.update"), m.ReplaceFile(true))
	rooms.DELETE("/:id/media/:media_id", can("rooms.update"), m.Delete(true))

	// The pictures themselves are public (an <img> tag cannot send a bearer token, and the names are unguessable), and
	// the route is shared with the other contexts that store pictures, so cmd/rental-management mounts it
	// (MediaHandler.Serve for the keys of this context).
}
