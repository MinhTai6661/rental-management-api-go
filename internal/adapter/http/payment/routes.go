package paymenthttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountPayments adds the payment routes to the /v1 group. Every route but the proof pictures needs a signed in caller
// acting in the right role and holding the permission of the route; every query is scoped to the caller (somebody
// else's bill or payment is a 404, never a 403). The payments use the permissions of the bills (a tenant who may view
// bills may pay them, a landlord who may update bills reviews the payments), as in the Laravel API. auth turns a bearer
// token into the caller; limiter counts the submissions per account and the proof requests per address.
func MountPayments(v1 *gin.RouterGroup, h *PaymentHandler, m *MethodHandler, p *ProofHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	can := httpx.RequirePermission
	signedIn := httpx.RequireAuth(auth)
	both := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord, authz.RoleTenant))
	tenant := v1.Group("", signedIn, httpx.RequireRole(authz.RoleTenant))
	landlord := v1.Group("", signedIn, httpx.RequireRole(authz.RoleLandlord))
	submit := httpx.RateLimitPerUser(limiter, "payment-submit", 30, time.Hour)

	both.GET("/invoices/:id/payments", can("invoices.view"), h.OfInvoice)
	tenant.POST("/invoices/:id/payments", can("invoices.view"), submit, h.Submit)
	tenant.GET("/invoices/:id/payment-instructions", can("invoices.view"), h.Instructions)
	tenant.DELETE("/payments/:id", can("invoices.view"), h.Withdraw)

	landlord.GET("/payments", can("invoices.update"), h.List)
	landlord.POST("/payments/:id/confirm", can("invoices.update"), h.Confirm)
	landlord.POST("/payments/:id/reject", can("invoices.update"), h.Reject)
	landlord.POST("/invoices/:id/record-payment", can("invoices.update"), h.Record)

	landlord.GET("/my/payment-methods", can("payment_methods.manage"), m.List)
	landlord.PUT("/my/payment-methods/:method", can("payment_methods.manage"), m.Save)
	landlord.DELETE("/my/payment-methods/:method", can("payment_methods.manage"), m.Remove)

	// The signature of the link is the permission, so there is no sign in; the address is limited instead.
	v1.GET("/payments/:id/proofs/:file", httpx.RateLimit(limiter, "payment-proof", 120, time.Minute), p.Show)
}
