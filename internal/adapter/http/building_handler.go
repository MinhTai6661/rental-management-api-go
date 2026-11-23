package httpadapter

import (
	"net/http"

	"github.com/gin-gonic/gin"

	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// BuildingHandler serves the building routes: it binds the request, calls the use case and writes the answer.
type BuildingHandler struct {
	uc     *usecase.Buildings
	urls   FileURLs
	binder *httpx.Binder
}

// NewBuildingHandler returns the handler. urls builds the public address of a stored picture.
func NewBuildingHandler(uc *usecase.Buildings, urls FileURLs, binder *httpx.Binder) *BuildingHandler {
	return &BuildingHandler{uc: uc, urls: urls, binder: binder}
}

// List answers GET /v1/buildings.
func (h *BuildingHandler) List(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var fields []apperr.Field
	filter := usecase.BuildingFilter{Name: c.Query("name")}
	var field *apperr.Field
	var ward, province *int64
	if ward, field = httpx.QueryInt64(c, "ward_code", 1, 1<<31-1); field != nil {
		fields = append(fields, *field)
	}
	if province, field = httpx.QueryInt64(c, "province_code", 1, 1<<31-1); field != nil {
		fields = append(fields, *field)
	}
	sort, field := httpx.QueryOneOf(c, "sort", "name", "-name", "created_at", "-created_at", "display_order")
	if field != nil {
		fields = append(fields, *field)
	}
	if len(fields) > 0 {
		httpx.Fail(c, apperr.Invalid(fields...))
		return
	}
	filter.WardCode, filter.ProvinceCode = int32Ptr(ward), int32Ptr(province)
	if sort != nil && *sort != "display_order" {
		filter.Sort = *sort
	}
	items, total, err := h.uc.List(c.Request.Context(), owner, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.List(c, buildingSummaryViewsOf(items, h.urls), httpx.TotalMeta(page, total))
}

// Get answers GET /v1/buildings/{id}.
func (h *BuildingHandler) Get(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.uc.Get(c.Request.Context(), owner, id)
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.OK(c, buildingDetailViewOf(detail, h.urls))
}

// Create answers POST /v1/buildings.
func (h *BuildingHandler) Create(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	var req buildingRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.uc.Create(c.Request.Context(), owner, req.input())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.Created(c, buildingDetailViewOf(detail, h.urls))
}

// Update answers PUT /v1/buildings/{id}.
func (h *BuildingHandler) Update(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req buildingRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.uc.Update(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.OK(c, buildingDetailViewOf(detail, h.urls))
}

// Delete answers DELETE /v1/buildings/{id}.
func (h *BuildingHandler) Delete(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Delete(c.Request.Context(), owner, id); err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.NoContent(c)
}

// BulkDelete answers POST /v1/buildings/bulk-delete.
func (h *BuildingHandler) BulkDelete(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	var req idsRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.BulkDelete(c.Request.Context(), owner, req.IDs)
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkViewOf(c, result)})
}

// Reorder answers PUT /v1/buildings/order.
func (h *BuildingHandler) Reorder(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	var req orderRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Reorder(c.Request.Context(), owner, req.IDs); err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.NoContent(c)
}

// Copy answers POST /v1/buildings/{id}/copy.
func (h *BuildingHandler) Copy(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req copyBuildingRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.Copy(c.Request.Context(), owner, id, req.Name, req.IncludeRooms)
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.Created(c, CopyView{Building: buildingDetailViewOf(result.Building, h.urls), RoomsCopied: result.RoomsCopied})
}
