package statshttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	"rental-management/pkg/authz"
)

// Mount adds the statistics routes to the /v1 group. A landlord reads the figures of their own rooms (permission
// `stats.view`; a manager's figures wait for the manager context), the staff read the platform (`stats.admin`) and
// only a super admin reads the system (`stats.system`). The roles do not overlap: an admin has no landlord figures.
func Mount(v1 *gin.RouterGroup, property *PropertyHandler, platform *PlatformHandler, auth httpx.Authenticator) {
	signedIn := httpx.RequireAuth(auth)

	own := v1.Group("/stats/property", signedIn, httpx.RequireRole(authz.RoleLandlord), httpx.RequirePermission("stats.view"))
	own.GET("/dashboard", property.Dashboard())
	own.GET("/overview", property.Overview())
	own.GET("/revenue", property.Revenue())
	own.GET("/occupancy", property.Occupancy())
	own.GET("/rooms", property.Rooms())
	own.GET("/buildings", property.Buildings())
	own.GET("/utilities", property.Utilities())

	staff := v1.Group("/stats/admin", signedIn, httpx.RequireRole(authz.RoleSuperAdmin, authz.RoleAdmin), httpx.RequirePermission("stats.admin"))
	staff.GET("/overview", platform.Overview)
	staff.GET("/growth", platform.Growth)

	v1.GET("/stats/system/overview", signedIn, httpx.RequireRole(authz.RoleSuperAdmin), httpx.RequirePermission("stats.system"), platform.System)
}
