package adminhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// MountAdmin adds the admin routes to the /v1 group. The staff routes need the staff role and the permission of
// their area; the settings and the legal documents belong to the super admin alone.
func MountAdmin(v1 *gin.RouterGroup, users *UserHandler, apps *ApplicationHandler, settings *SettingsHandler, legal *LegalHandler,
	auth httpx.Authenticator, limiter httpx.Limiter) {
	signedIn := httpx.RequireAuth(auth)
	staff := httpx.RequireRole(authz.RoleSuperAdmin, authz.RoleAdmin)

	u := v1.Group("/admin", signedIn, staff)
	u.GET("/users", httpx.RequirePermission("users.view"), users.List)
	u.POST("/users/bulk-delete", httpx.RequirePermission("users.delete"), users.BulkDelete)
	u.DELETE("/users/:id", httpx.RequirePermission("users.delete"), users.Delete)
	u.PUT("/users/:id/roles", httpx.RequirePermission("users.assign_roles"), users.AssignRoles)
	u.GET("/roles", httpx.RequirePermission("users.assign_roles"), users.Roles)

	review := httpx.RequirePermission("landlord_applications.review")
	u.GET("/users/:id/landlord-impact", review, users.Impact)
	u.POST("/users/:id/revoke-landlord", review, users.RevokeLandlord)
	a := u.Group("/landlord-applications", review)
	a.GET("", apps.List)
	a.POST("/bulk", apps.Bulk)
	a.GET("/:id", apps.Get)
	a.POST("/:id/approve", apps.Approve)
	a.POST("/:id/reject", apps.Reject)

	owner := v1.Group("/admin", signedIn, httpx.RequireRole(authz.RoleSuperAdmin))
	owner.GET("/settings", httpx.RequirePermission("settings.manage"), settings.List)
	owner.PUT("/settings", httpx.RequirePermission("settings.manage"), settings.Update)
	owner.GET("/settings/history", httpx.RequirePermission("settings.manage"), settings.History)
	l := owner.Group("/legal/:kind", httpx.RequirePermission("legal.manage"))
	l.GET("", legal.Current)
	l.POST("", legal.Publish)
	l.GET("/versions", legal.Versions)
	l.GET("/:document_id", legal.Document)

	// The terms are public, so a visitor can read them before registering.
	v1.GET("/legal/:kind", httpx.RateLimit(limiter, "legal-show", 120, time.Minute), legal.Show)
}

// MountSelf adds the routes of the signed in person about their own account: the landlord application, the landlord
// profile, the terms they accept and the data subject's rights.
func MountSelf(v1 *gin.RouterGroup, apps *ApplicationHandler, profiles *ProfileHandler, legal *LegalHandler, personal *PersonalDataHandler,
	auth httpx.Authenticator, limiter httpx.Limiter) {
	signedIn := httpx.RequireAuth(auth)

	app := v1.Group("/my/landlord-application", signedIn)
	app.GET("", apps.Mine)
	app.POST("", httpx.RateLimitPerUser(limiter, "landlord-application-store", 10, time.Hour), apps.Submit)
	app.DELETE("", apps.Withdraw)

	p := v1.Group("/landlord/profile", signedIn, httpx.RequireRole(authz.RoleLandlord))
	p.GET("", profiles.Get)
	p.PUT("", profiles.Put)

	me := v1.Group("/profile", signedIn)
	me.GET("/legal-updates", legal.Updates)
	me.POST("/accept-terms", httpx.RateLimitPerUser(limiter, "profile-accept-terms", 20, time.Hour), legal.Accept)
	me.GET("/export", httpx.RateLimitPerUser(limiter, "profile-export", 5, time.Hour), personal.Export)
	me.GET("/erasure-check", personal.ErasureCheck)
	me.DELETE("", httpx.RateLimitPerUser(limiter, "profile-erase", 5, time.Hour), personal.Erase)
}
