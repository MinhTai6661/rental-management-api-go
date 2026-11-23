package planhttp

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
)

func itoa(n int) string { return strconv.Itoa(n) }

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

// roleOf is the role the request acts in.
func roleOf(c *gin.Context) string {
	p, _ := httpx.PrincipalOf(c)
	return p.Role
}
