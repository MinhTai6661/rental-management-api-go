package invoicehttp

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/invoice"
	"rental-management/pkg/authz"
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

// viewerOf is the caller as a viewer of bills by the role of the request: a landlord sees the bills of their contracts,
// a manager those of the rooms handed to them, a tenant the ones sent to them.
func viewerOf(c *gin.Context) (domain.Viewer, bool) {
	id, ok := userIDOf(c)
	if !ok {
		return domain.Viewer{}, false
	}
	p, _ := httpx.PrincipalOf(c)
	return domain.Viewer{UserID: id, AsLandlord: p.Role == authz.RoleLandlord, AsManager: p.Role == authz.RoleManager}, true
}

// landlordAndID reads the caller and the id in the path.
func landlordAndID(c *gin.Context) (landlord, id uuid.UUID, ok bool) {
	landlord, ok = userIDOf(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return uuid.Nil, uuid.Nil, false
	}
	return landlord, id, true
}

// loadMoreMeta is the "meta" of a list that is read a few at a time ("load more"): where the page starts and whether
// another one follows.
type loadMoreMeta struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
}

// loadMore reads ?limit and ?offset of a "load more" list: limit defaults to 20 and may not pass max, offset to 0.
func loadMore(c *gin.Context, max int) (limit, offset int, err error) {
	limit, offset = 20, 0
	var fields []apperr.Field
	read := func(name string, lower, upper int) (int, bool) {
		raw := c.Query(name)
		if raw == "" {
			return 0, false
		}
		n, convErr := strconv.Atoi(raw)
		switch {
		case convErr != nil:
			fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleInteger})
		case n < lower:
			fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleMinNumber, Params: map[string]string{"min": itoa(lower)}})
		case upper > 0 && n > upper:
			fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": itoa(upper)}})
		default:
			return n, true
		}
		return 0, false
	}
	if n, ok := read("limit", 1, max); ok {
		limit = n
	}
	if n, ok := read("offset", 0, 0); ok {
		offset = n
	}
	if len(fields) > 0 {
		return 0, 0, apperr.Invalid(fields...)
	}
	return limit, offset, nil
}

// writerViewer is the caller as the viewer of the bill they just wrote or changed (a landlord, or a manager acting).
func writerViewer(c *gin.Context, id uuid.UUID) domain.Viewer {
	p, _ := httpx.PrincipalOf(c)
	return domain.Viewer{UserID: id, AsLandlord: p.Role != authz.RoleManager, AsManager: p.Role == authz.RoleManager}
}
