package reporthttp

import (
	"context"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/report"
)

// listFunc is a use case that lists reports for the caller.
type listFunc func(ctx context.Context, viewer domain.Viewer, f domain.Filter, limit, offset int) ([]domain.View, int, error)

// MountReports adds the report routes to the /v1 group. Every route needs a signed in caller holding the permission of
// the route; what the caller may do with a given report is decided by the report rules (a report the caller may not see
// is a 404, one they may see but not change a 403).
func MountReports(v1 *gin.RouterGroup, h *ReportHandler, auth httpx.Authenticator) {
	can := httpx.RequirePermission
	signedIn := v1.Group("", httpx.RequireAuth(auth))

	signedIn.GET("/reports", can("reports.view"), h.Mine)
	signedIn.GET("/reports/inbox", can("reports.update"), h.Inbox)
	signedIn.POST("/reports", can("reports.create"), h.Create)
	signedIn.GET("/reports/:id", can("reports.view"), h.Show)
	signedIn.DELETE("/reports/:id", can("reports.view"), h.Delete)
	signedIn.GET("/reports/:id/replies", can("reports.view"), h.Replies)
	signedIn.POST("/reports/:id/replies", can("reports.view"), h.Reply)
	signedIn.PUT("/reports/:id/status", can("reports.update"), h.SetStatus)
}

// MountReportSocket adds the conversation socket route to the /v1 group. The caller is the one-time ticket of the
// realtime API (a browser cannot send an Authorization header on a WebSocket) or a bearer token; what the caller may do in
// the report is decided by the report rules.
func MountReportSocket(v1 *gin.RouterGroup, h *SocketHandler, auth httpx.Authenticator) {
	v1.GET("/reports/:id/ws", httpx.AuthenticateIfPresent(auth), h.Connect)
}
