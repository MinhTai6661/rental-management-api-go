package reporthttp

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/report"
)

// viewerOf is the caller of the request and the role it acts in (RequireAuth ran before).
func viewerOf(c *gin.Context) (domain.Viewer, bool) {
	if p, ok := httpx.PrincipalOf(c); ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return domain.Viewer{UserID: id, Role: p.Role}, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return domain.Viewer{}, false
}

// viewerAndID is the caller and the id in the path.
func viewerAndID(c *gin.Context) (domain.Viewer, uuid.UUID, bool) {
	viewer, ok := viewerOf(c)
	if !ok {
		return domain.Viewer{}, uuid.Nil, false
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return domain.Viewer{}, uuid.Nil, false
	}
	return viewer, id, true
}
