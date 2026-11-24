package notificationhttp

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	usecase "rental-management/internal/usecase/notification"
)

// viewerOf is the caller of the request and the role they act in (RequireAuth ran before). The role decides which side
// of the inbox they see.
func viewerOf(c *gin.Context) (usecase.Viewer, bool) {
	p, ok := httpx.PrincipalOf(c)
	if ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return usecase.Viewer{UserID: id, Role: p.Role}, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return usecase.Viewer{}, false
}

func itoa(n int) string { return strconv.Itoa(n) }

// FileURLs turns the storage key of a picture into the public address the client loads it from.
type FileURLs interface {
	URL(key string) string
}

// sessionOf is the login session of the caller (nil when the token carries none or it is not a uuid).
func sessionOf(ginCtx *gin.Context) *uuid.UUID {
	p, ok := httpx.PrincipalOf(ginCtx)
	if !ok {
		return nil
	}
	id, err := uuid.Parse(p.SessionID)
	if err != nil {
		return nil
	}
	return &id
}

// parseUUID reads a uuid from a body field.
func parseUUID(text string) (uuid.UUID, error) { return uuid.Parse(text) }
