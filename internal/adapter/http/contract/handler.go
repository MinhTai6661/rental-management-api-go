// Package contracthttp is the HTTP side of the contract use cases: the landlord's and the tenant's views of rental
// contracts, a tenant's registration and request to end a contract, and the renter's own list ("my rooms").
package contracthttp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// ContractHandler serves the contract routes: it binds the request, calls the use case and writes the answer.
type ContractHandler struct {
	uc     *usecase.Contracts
	urls   FileURLs
	binder *httpx.Binder
}

// NewContractHandler returns the handler. urls builds the public address of a stored picture.
func NewContractHandler(uc *usecase.Contracts, urls FileURLs, binder *httpx.Binder) *ContractHandler {
	return &ContractHandler{uc: uc, urls: urls, binder: binder}
}

// List answers GET /v1/contracts: the contracts on the landlord's rooms, or the tenant's own.
func (h *ContractHandler) List(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	filter, err := listFilter(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	items, total, err := h.uc.List(c.Request.Context(), viewer, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.List(c, contractViewsOf(items, viewer, h.urls), httpx.TotalMeta(page, total))
}

// listFilter reads the list filters; every bad value is reported in one 422.
func listFilter(c *gin.Context) (usecase.ListFilter, error) {
	var fields []apperr.Field
	keep := func(f *apperr.Field) {
		if f != nil {
			fields = append(fields, *f)
		}
	}
	filter := usecase.ListFilter{}
	status, f := httpx.QueryOneOf(c, "status", "pending", "active", "ended", "rejected")
	keep(f)
	if status != nil {
		s := domain.Status(*status)
		filter.Status = &s
	}
	cancellation, f := httpx.QueryOneOf(c, "cancellation_status", "requested", "approved", "declined")
	keep(f)
	if cancellation != nil {
		s := domain.CancellationStatus(*cancellation)
		filter.CancellationStatus = &s
	}
	room, f := httpx.QueryUUID(c, "room_id")
	keep(f)
	filter.RoomID = room
	building, f := httpx.QueryUUID(c, "building_id")
	keep(f)
	filter.BuildingID = building
	noBuilding, f := httpx.QueryBool(c, "no_building")
	keep(f)
	filter.NoBuilding = noBuilding != nil && *noBuilding
	unpaid, f := httpx.QueryBool(c, "has_unpaid_invoice")
	keep(f)
	filter.HasUnpaidInvoice = unpaid != nil && *unpaid
	if filter.NoBuilding && filter.BuildingID != nil {
		fields = append(fields, apperr.Field{Name: "no_building", Rule: apperr.RuleOneOf})
	}
	filter.Search = c.Query("search")
	if len([]rune(filter.Search)) > 255 {
		fields = append(fields, apperr.Field{Name: "search", Rule: apperr.RuleMaxString, Params: map[string]string{"max": "255"}})
	}
	sort, f := httpx.QueryOneOf(c, "sort", "display_order", "created_at", "-created_at", "starts_on", "-starts_on", "ends_on", "-ends_on")
	keep(f)
	if sort != nil && *sort != "display_order" {
		filter.Sort = *sort
	}
	if len(fields) > 0 {
		return usecase.ListFilter{}, apperr.Invalid(fields...)
	}
	return filter, nil
}

// Get answers GET /v1/contracts/{id}.
func (h *ContractHandler) Get(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Get(c.Request.Context(), viewer, id)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, contractViewOf(d, viewer, h.urls))
}

// Register answers POST /v1/contracts/register: a tenant asks to rent a room.
func (h *ContractHandler) Register(c *gin.Context) {
	tenant, ok := userIDOf(c)
	if !ok {
		return
	}
	var req registerRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Register(c.Request.Context(), tenant, in)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.Created(c, contractViewOf(d, domain.Viewer{UserID: tenant}, h.urls))
}

// Update answers PUT and PATCH /v1/contracts/{id}: a partial update by the landlord.
func (h *ContractHandler) Update(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req updateRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Update(c.Request.Context(), landlord, id, in)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, contractViewOf(d, domain.Viewer{UserID: landlord, AsLandlord: true}, h.urls))
}

// Delete answers DELETE /v1/contracts/{id}.
func (h *ContractHandler) Delete(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Delete(c.Request.Context(), viewer, id); err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.NoContent(c)
}

// Reorder answers PUT /v1/contracts/order.
func (h *ContractHandler) Reorder(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req orderRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Reorder(c.Request.Context(), landlord, req.IDs); err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.NoContent(c)
}

// Bulk answers POST /v1/contracts/bulk: approve or reject several waiting registrations.
func (h *ContractHandler) Bulk(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req bulkRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.BulkAnswer(c.Request.Context(), landlord, req.IDs, usecase.Answer(req.Action))
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkViewOf(c, result)})
}

// Terminate answers POST /v1/contracts/{id}/terminate: the landlord calls the contract off.
func (h *ContractHandler) Terminate(c *gin.Context) {
	landlord, id, ok := h.landlordAndID(c)
	if !ok {
		return
	}
	var req terminateRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	h.answer(c, landlord, true)(h.uc.Terminate(c.Request.Context(), landlord, id, req.Reason))
}

// RequestCancellation answers POST /v1/contracts/{id}/cancellation-request: the tenant asks to end the contract.
func (h *ContractHandler) RequestCancellation(c *gin.Context) {
	tenant, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req cancellationRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	h.answer(c, tenant, false)(h.uc.RequestCancellation(c.Request.Context(), tenant, id, req.Reason))
}

// WithdrawCancellation answers DELETE /v1/contracts/{id}/cancellation-request.
func (h *ContractHandler) WithdrawCancellation(c *gin.Context) {
	tenant, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	h.answer(c, tenant, false)(h.uc.WithdrawCancellation(c.Request.Context(), tenant, id))
}

// ApproveCancellation answers POST /v1/contracts/{id}/cancellation-request/approve.
func (h *ContractHandler) ApproveCancellation(c *gin.Context) {
	landlord, id, ok := h.landlordAndID(c)
	if !ok {
		return
	}
	h.answer(c, landlord, true)(h.uc.ApproveCancellation(c.Request.Context(), landlord, id))
}

// DeclineCancellation answers POST /v1/contracts/{id}/cancellation-request/decline.
func (h *ContractHandler) DeclineCancellation(c *gin.Context) {
	landlord, id, ok := h.landlordAndID(c)
	if !ok {
		return
	}
	var req declineRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	h.answer(c, landlord, true)(h.uc.DeclineCancellation(c.Request.Context(), landlord, id, req.Note))
}
