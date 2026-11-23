package paymenthttp

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/payment"
	"rental-management/pkg/authz"
)

// userIDOf is the caller of the request (RequireAuth ran before).
func userIDOf(c *gin.Context) (uuid.UUID, bool) {
	p, ok := httpx.PrincipalOf(c)
	if ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return id, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return uuid.Nil, false
}

// viewerOf is the caller as a viewer of payments: a landlord sees the payments on their rooms' bills, a tenant their own.
func viewerOf(c *gin.Context) (domain.Viewer, bool) {
	id, ok := userIDOf(c)
	if !ok {
		return domain.Viewer{}, false
	}
	p, _ := httpx.PrincipalOf(c)
	return domain.Viewer{UserID: id, AsLandlord: p.HasRole(authz.RoleLandlord)}, true
}

// callerAndID reads the caller and the id in the path.
func callerAndID(c *gin.Context) (caller, id uuid.UUID, ok bool) {
	caller, ok = userIDOf(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return uuid.Nil, uuid.Nil, false
	}
	return caller, id, true
}

func itoa(n int) string { return strconv.Itoa(n) }
