package property

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/platform/authz"
	"rental-management/internal/platform/httpx"
)

// RouteOptions configures the routes of the module.
type RouteOptions struct {
	// Auth turns a bearer token into the caller (the identity service).
	Auth httpx.Authenticator
	// Limiter counts requests for the rate limits of the routes (the picture uploads).
	Limiter httpx.Limiter
}

// Mount returns the function that adds the routes to the /v1 group. Everything here belongs to the landlord: the
// caller must act in the landlord role and hold the permission of the route, and every query is scoped to them.
func Mount(svc *Service, opts RouteOptions) func(v1 *gin.RouterGroup) {
	h := handler{svc: svc, binder: httpx.NewBinder()}
	return func(v1 *gin.RouterGroup) {
		owner := v1.Group("", httpx.RequireAuth(opts.Auth), httpx.RequireRole(authz.RoleLandlord))
		can := httpx.RequirePermission
		upload := httpx.RateLimit(opts.Limiter, "property-media-upload", 60, time.Minute)

		buildings := owner.Group("/buildings")
		buildings.GET("", can("buildings.view"), h.listBuildings)
		buildings.POST("", can("buildings.create"), h.createBuilding)
		buildings.PUT("/order", can("buildings.update"), h.reorderBuildings)
		buildings.POST("/bulk-delete", can("buildings.delete"), h.bulkDeleteBuildings)
		buildings.GET("/:id", can("buildings.view"), h.getBuilding)
		buildings.PUT("/:id", can("buildings.update"), h.updateBuilding)
		buildings.DELETE("/:id", can("buildings.delete"), h.deleteBuilding)
		buildings.POST("/:id/copy", can("buildings.create"), h.copyBuilding)
		buildings.POST("/:id/rooms", can("rooms.create"), h.createRoomsInBuilding)
		buildings.POST("/:id/media", upload, can("buildings.update"), h.uploadMedia(false))
		buildings.PUT("/:id/media", can("buildings.update"), h.arrangeMedia(false))
		buildings.PUT("/:id/media/:media_id/file", upload, can("buildings.update"), h.replaceMediaFile(false))
		buildings.DELETE("/:id/media/:media_id", can("buildings.update"), h.deleteMedia(false))

		rooms := owner.Group("/rooms")
		rooms.GET("", can("rooms.view"), h.listRooms)
		rooms.POST("", can("rooms.create"), h.createRoom)
		rooms.PUT("/order", can("rooms.update"), h.reorderRooms)
		rooms.POST("/bulk-delete", can("rooms.delete"), h.bulkDeleteRooms)
		rooms.POST("/bulk-status", can("rooms.update"), h.bulkRoomStatus)
		rooms.GET("/:id", can("rooms.view"), h.getRoom)
		rooms.PUT("/:id", can("rooms.update"), h.updateRoom)
		rooms.DELETE("/:id", can("rooms.delete"), h.deleteRoom)
		rooms.POST("/:id/media", upload, can("rooms.update"), h.uploadMedia(true))
		rooms.PUT("/:id/media", can("rooms.update"), h.arrangeMedia(true))
		rooms.PUT("/:id/media/:media_id/file", upload, can("rooms.update"), h.replaceMediaFile(true))
		rooms.DELETE("/:id/media/:media_id", can("rooms.update"), h.deleteMedia(true))

		// The pictures themselves are public: an <img> tag cannot send a bearer token, and the names are unguessable.
		v1.GET("/media/*key", h.serveMedia)
		v1.HEAD("/media/*key", h.serveMedia)
	}
}
