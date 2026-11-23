package httpx

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/apperr"
	"rental-management/pkg/authz"
)

const principalKey = "httpx.principal"

// Authenticator turns a bearer token into the caller of the request. The identity module implements it.
type Authenticator interface {
	Authenticate(ctx context.Context, bearer string) (authz.Principal, error)
}

// RequireAuth rejects requests without a valid access token (401, or 403 for a disabled account) and stores the
// Principal for the handlers that follow.
func RequireAuth(auth Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			Fail(c, apperr.Unauthenticated())
			return
		}
		principal, err := auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			Fail(c, err)
			return
		}
		c.Set(principalKey, principal)
		c.Next()
	}
}

// RequirePermission lets the request through only when the principal's role has the permission
// (including "<resource>.manage"). Put it after RequireAuth.
func RequirePermission(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalOf(c)
		if !ok {
			Fail(c, apperr.Unauthenticated())
			return
		}
		if !p.Can(name) {
			Fail(c, apperr.Forbidden())
			return
		}
		c.Next()
	}
}

// RequireRole lets the request through only when it acts in one of the roles. Put it after RequireAuth.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalOf(c)
		if !ok {
			Fail(c, apperr.Unauthenticated())
			return
		}
		if !p.HasRole(roles...) {
			Fail(c, apperr.Forbidden())
			return
		}
		c.Next()
	}
}

// PrincipalOf returns the authenticated caller; ok is false when RequireAuth did not run.
func PrincipalOf(c *gin.Context) (authz.Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return authz.Principal{}, false
	}
	p, ok := v.(authz.Principal)
	return p, ok
}

// bearerToken extracts the token of an "Authorization: Bearer <token>" header ("" when there is none).
func bearerToken(header string) string {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
