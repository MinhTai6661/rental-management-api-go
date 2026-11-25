package invoicehttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountInvoices adds the invoice routes to the /v1 group. Every route but the public one needs a signed in caller
// acting in the right role and holding the permission of the route; every query is scoped to the caller (somebody
// else's contract or bill is a 404, never a 403). auth turns a bearer token into the caller; limiter counts the
// requests of the public page per address.
func MountInvoices(v1 *gin.RouterGroup, h *InvoiceHandler, m *MeterHandler, p *PublicHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	both := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleManager, authz.RoleTenant))
	// A manager works on the bills of the rooms handed to them (the use cases scope every query and check the rights).
	landlord := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleManager))

	// A landlord sees every bill of their contracts, a tenant what was sent to their account.
	both.GET("/contracts/:id/invoices", can("invoices.view"), h.List)
	both.GET("/invoices/:id", can("invoices.view"), h.Get)

	landlord.GET("/contracts/:id/billing-periods", can("invoices.view"), h.Periods)
	landlord.POST("/contracts/:id/invoices/preview", can("invoices.create"), h.Preview)
	landlord.POST("/contracts/:id/invoices", can("invoices.create"), h.Create)
	landlord.POST("/invoices/:id/send", can("invoices.create"), h.Send)
	landlord.POST("/invoices/:id/share-link", can("invoices.create"), h.ShareLink)
	landlord.POST("/invoices/:id/share-link/regenerate", can("invoices.create"), h.RegenerateShareLink)
	landlord.DELETE("/invoices/:id/share-link", can("invoices.create"), h.RevokeShareLink)
	landlord.PATCH("/invoices/:id", can("invoices.update"), h.SetStatus)
	landlord.PUT("/invoices/:id", can("invoices.update"), h.Update)
	landlord.POST("/invoices/:id/preview", can("invoices.update"), h.PreviewUpdate)
	landlord.DELETE("/invoices/:id", can("invoices.delete"), h.Delete)
	landlord.GET("/rooms/:id/meter-readings", can("rooms.view"), m.List)

	// Anybody with the link reads the bill, signed in or not, so it is limited per address.
	v1.GET("/public/invoices/:token", httpx.RateLimit(limiter, "public-invoice", 30, time.Minute), p.Show)
}
