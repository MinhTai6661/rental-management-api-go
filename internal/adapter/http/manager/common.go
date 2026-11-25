package managerhttp

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/manager"
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

// capabilities turns the codes of a request field into capabilities; an unknown code names the field.
func capabilities(field string, codes []string) ([]domain.Capability, error) {
	out, err := domain.ParseCapabilities(codes)
	if err != nil {
		return nil, apperr.Invalid(apperr.Field{Name: field, Rule: apperr.RuleOneOf, Params: map[string]string{"values": joinCodes()}})
	}
	return out, nil
}

func joinCodes() string {
	s := ""
	for i, code := range capabilityCodes() {
		if i > 0 {
			s += ", "
		}
		s += code
	}
	return s
}
