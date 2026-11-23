package identityhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/httpx"
)

// MountAuth adds the auth routes to the /v1 group. Everything is rate limited per client address; login also per
// address and email (in the handler). auth guards the routes that need a signed-in caller.
func MountAuth(v1 *gin.RouterGroup, h *AuthHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	limit := func(bucket string, n int) gin.HandlerFunc {
		return httpx.RateLimit(limiter, bucket, n, time.Minute)
	}
	group := v1.Group("/auth")
	group.POST("/register", limit("auth-register", 10), h.Register)
	group.POST("/login", limit("auth-login", 10), h.Login)
	group.POST("/refresh", limit("auth-refresh", 30), h.Refresh)
	group.POST("/email/verify", limit("auth-email-verify", 10), h.VerifyEmail)
	group.POST("/email/resend", limit("auth-email-resend", 5), h.ResendEmail)

	signedIn := group.Group("", httpx.RequireAuth(auth))
	signedIn.GET("/me", h.Me)
	signedIn.POST("/logout", h.Logout)
	signedIn.POST("/logout-all", h.LogoutAll)
	signedIn.POST("/switch-role", limit("auth-switch-role", 10), h.SwitchRole)
	signedIn.PUT("/password", limit("auth-password", 5), h.ChangePassword)
}
