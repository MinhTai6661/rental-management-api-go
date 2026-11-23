package propertyhttp

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// landlordIDOf is the caller of the request (RequireAuth ran before).
func landlordIDOf(c *gin.Context) (uuid.UUID, bool) {
	p, ok := httpx.PrincipalOf(c)
	if ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return id, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return uuid.Nil, false
}

func int32Ptr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // bounded by QueryInt64
	return &n
}

// bulkViewOf translates the refusals of a bulk operation into the language of the request.
func bulkViewOf(c *gin.Context, r usecase.BulkResult) BulkView {
	l := httpx.Localize(c)
	out := BulkView{Done: r.Done, Failed: make([]bulkFailureView, 0, len(r.Failed))}
	for _, f := range r.Failed {
		code, params := apperr.CodeServerInternal, map[string]string(nil)
		if e := propertyError(f.Err); e != nil {
			code, params = e.Code, e.Params
		}
		out.Failed = append(out.Failed, bulkFailureView{ID: f.ID, Name: f.Name, Code: code, Message: l.T("errors."+code, params)})
	}
	return out
}
