package contracthttp

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
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

// viewerOf is the caller as a viewer of contracts: a landlord sees the contracts on their rooms, a tenant their own.
func viewerOf(c *gin.Context) (domain.Viewer, bool) {
	id, ok := userIDOf(c)
	if !ok {
		return domain.Viewer{}, false
	}
	p, _ := httpx.PrincipalOf(c)
	return domain.Viewer{UserID: id, AsLandlord: p.Role == authz.RoleLandlord, AsManager: p.Role == authz.RoleManager}, true
}

// bulkViewOf translates the refusals of a bulk answer into the language of the request.
func bulkViewOf(c *gin.Context, r usecase.BulkResult) BulkView {
	l := httpx.Localize(c)
	out := BulkView{Done: r.Done, Failed: make([]FailureView, 0, len(r.Failed))}
	for _, f := range r.Failed {
		code, params := apperr.CodeServerInternal, map[string]string(nil)
		if e := contractError(f.Err); e != nil {
			code, params = e.Code, e.Params
		}
		out.Failed = append(out.Failed, FailureView{ID: f.ID, Name: f.Name, Code: code, Message: l.T("errors."+code, params)})
	}
	return out
}

// landlordAndID reads the caller and the contract id of a landlord's action on one contract.
func (h *ContractHandler) landlordAndID(c *gin.Context) (landlord, id uuid.UUID, ok bool) {
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

// answer returns the function that writes the outcome of an action that returns the contract: the contract as its
// actor sees it, or the mapped error.
func (h *ContractHandler) answer(c *gin.Context, actor uuid.UUID, asLandlord bool) func(domain.Detail, error) {
	return func(d domain.Detail, err error) {
		if err != nil {
			httpx.Fail(c, mapContractError(err))
			return
		}
		httpx.OK(c, contractViewOf(d, domain.Viewer{UserID: actor, AsLandlord: asLandlord}, h.urls))
	}
}
