package audithttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/audit"
	"rental-management/pkg/authz"
)

// Mount adds the audit routes to the /v1 group: the staff's view (staff role and permission `audit.view`), everybody's
// own activity, and the history of a room, a contract or an invoice, which only its landlord reads (the permission to
// change the thing, as the use case then checks that it is theirs).
func Mount(v1 *gin.RouterGroup, h *Handler, auth httpx.Authenticator) {
	signedIn := httpx.RequireAuth(auth)
	v1.GET("/admin/audit-logs", signedIn, httpx.RequireRole(authz.RoleSuperAdmin, authz.RoleAdmin), httpx.RequirePermission("audit.view"), h.List)
	v1.GET("/my/activity", signedIn, h.Mine)

	landlord := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord))
	landlord.GET("/rooms/:id/history", httpx.RequirePermission("rooms.update"), h.History(domain.SubjectRoom))
	landlord.GET("/contracts/:id/history", httpx.RequirePermission("contracts.update"), h.History(domain.SubjectContract))
	landlord.GET("/invoices/:id/history", httpx.RequirePermission("invoices.update"), h.History(domain.SubjectInvoice))
}
