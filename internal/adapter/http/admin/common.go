package adminhttp

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	usecase "rental-management/internal/usecase/admin"
)

// actorOf is the caller of the request and the role they act in (RequireAuth ran before).
func actorOf(c *gin.Context) (usecase.Actor, bool) {
	p, ok := httpx.PrincipalOf(c)
	if ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return usecase.Actor{ID: id, Role: p.Role}, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return usecase.Actor{}, false
}

// parseIDs turns the ids of a bulk body into uuids (the validator already checked them).
func parseIDs(raw []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleUUID})
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// failed is one refused item of a bulk answer.
type failed struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// bulkView is the answer of a bulk request.
type bulkView struct {
	Done   []string `json:"done"`
	Failed []failed `json:"failed"`
}
