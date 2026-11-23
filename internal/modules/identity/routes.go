package identity

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/platform/httpx"
)

// RouteOptions configures the routes of the module.
type RouteOptions struct {
	// Limiter counts requests for the rate limits of the routes.
	Limiter httpx.Limiter
}

// Mount returns the function that adds the auth routes to the /v1 group. Everything is rate limited per client
// address; login also per address and email.
func Mount(svc *Service, opts RouteOptions) func(v1 *gin.RouterGroup) {
	h := handler{svc: svc, binder: httpx.NewBinder(), limiter: opts.Limiter}
	limit := func(bucket string, n int) gin.HandlerFunc {
		return httpx.RateLimit(opts.Limiter, bucket, n, time.Minute)
	}
	return func(v1 *gin.RouterGroup) {
		auth := v1.Group("/auth")
		auth.POST("/register", limit("auth-register", 10), h.register)
		auth.POST("/login", limit("auth-login", 10), h.login)
		auth.POST("/refresh", limit("auth-refresh", 30), h.refresh)
		auth.POST("/email/verify", limit("auth-email-verify", 10), h.verifyEmail)
		auth.POST("/email/resend", limit("auth-email-resend", 5), h.resendEmail)

		signedIn := auth.Group("", httpx.RequireAuth(svc))
		signedIn.GET("/me", h.me)
		signedIn.POST("/logout", h.logout)
		signedIn.POST("/logout-all", h.logoutAll)
		signedIn.POST("/switch-role", limit("auth-switch-role", 10), h.switchRole)
		signedIn.PUT("/password", limit("auth-password", 5), h.changePassword)
	}
}
