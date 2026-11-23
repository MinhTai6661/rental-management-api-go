package propertyhttp

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	domain "rental-management/internal/domain/property"
	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// RoomHandler serves the room routes: it binds the request, calls the use case and writes the answer.
type RoomHandler struct {
	uc     *usecase.Rooms
	urls   FileURLs
	binder *httpx.Binder
}

// NewRoomHandler returns the handler. urls builds the public address of a stored picture.
func NewRoomHandler(uc *usecase.Rooms, urls FileURLs, binder *httpx.Binder) *RoomHandler {
	return &RoomHandler{uc: uc, urls: urls, binder: binder}
}

// List answers GET /v1/rooms.
func (h *RoomHandler) List(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	filter, err := roomFilter(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	items, total, err := h.uc.List(c.Request.Context(), owner, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.List(c, roomSummaryViewsOf(items, h.urls), httpx.TotalMeta(page, total))
}

// roomFilter reads the list filters of the rooms; every bad value is reported in one 422.
func roomFilter(c *gin.Context) (usecase.RoomFilter, error) {
	var fields []apperr.Field
	keep := func(f *apperr.Field) {
		if f != nil {
			fields = append(fields, *f)
		}
	}
	filter := usecase.RoomFilter{Name: c.Query("name")}
	status, f := httpx.QueryOneOf(c, "status", "available", "rented", "maintenance")
	keep(f)
	filter.Status = status
	building, f := httpx.QueryUUID(c, "building_id")
	keep(f)
	filter.BuildingID = building
	noBuilding, f := httpx.QueryBool(c, "no_building")
	keep(f)
	filter.NoBuilding = noBuilding != nil && *noBuilding
	if filter.NoBuilding && filter.BuildingID != nil {
		fields = append(fields, apperr.Field{Name: "no_building", Rule: apperr.RuleOneOf})
	}
	ward, f := httpx.QueryInt64(c, "ward_code", 1, 1<<31-1)
	keep(f)
	province, f := httpx.QueryInt64(c, "province_code", 1, 1<<31-1)
	keep(f)
	filter.WardCode, filter.ProvinceCode = int32Ptr(ward), int32Ptr(province)
	const maxMoney = 1_000_000_000_000
	filter.PriceFrom, f = httpx.QueryInt64(c, "price_from", 0, maxMoney)
	keep(f)
	filter.PriceTo, f = httpx.QueryInt64(c, "price_to", 0, maxMoney)
	keep(f)
	filter.AreaFrom, f = httpx.QueryFloat(c, "area_from", 0, 99999.99)
	keep(f)
	filter.AreaTo, f = httpx.QueryFloat(c, "area_to", 0, 99999.99)
	keep(f)
	// A range whose end is below its start can match nothing: say so on the start, with the end as the limit.
	if filter.PriceFrom != nil && filter.PriceTo != nil && *filter.PriceTo < *filter.PriceFrom {
		fields = append(fields, apperr.Field{Name: "price_from", Rule: apperr.RuleLTEField, Params: map[string]string{"other": strconv.FormatInt(*filter.PriceTo, 10)}})
	}
	if filter.AreaFrom != nil && filter.AreaTo != nil && *filter.AreaTo < *filter.AreaFrom {
		fields = append(fields, apperr.Field{Name: "area_from", Rule: apperr.RuleLTEField, Params: map[string]string{"other": strconv.FormatFloat(*filter.AreaTo, 'f', -1, 64)}})
	}
	sort, f := httpx.QueryOneOf(c, "sort", "display_order", "name", "-name", "price", "-price", "area", "-area", "created_at", "-created_at")
	keep(f)
	if sort != nil && *sort != "display_order" {
		filter.Sort = *sort
	}
	if len(fields) > 0 {
		return usecase.RoomFilter{}, apperr.Invalid(fields...)
	}
	return filter, nil
}

// Get answers GET /v1/rooms/{id}.
func (h *RoomHandler) Get(c *gin.Context) {
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
	httpx.OK(c, roomDetailViewOf(detail, h.urls))
}

// Create answers POST /v1/rooms.
func (h *RoomHandler) Create(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	var req roomRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.uc.Create(c.Request.Context(), owner, req.input())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.Created(c, roomDetailViewOf(detail, h.urls))
}

// Update answers PUT /v1/rooms/{id}.
func (h *RoomHandler) Update(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req roomRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.uc.Update(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.OK(c, roomDetailViewOf(detail, h.urls))
}

// Delete answers DELETE /v1/rooms/{id}.
func (h *RoomHandler) Delete(c *gin.Context) {
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

// BulkDelete answers POST /v1/rooms/bulk-delete.
func (h *RoomHandler) BulkDelete(c *gin.Context) {
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

// BulkStatus answers POST /v1/rooms/bulk-status.
func (h *RoomHandler) BulkStatus(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	var req bulkStatusRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.BulkSetStatus(c.Request.Context(), owner, req.IDs, domain.Status(req.Status))
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkViewOf(c, result)})
}

// Reorder answers PUT /v1/rooms/order.
func (h *RoomHandler) Reorder(c *gin.Context) {
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

// CreateRun answers POST /v1/buildings/{id}/rooms: a run of rooms in one building.
func (h *RoomHandler) CreateRun(c *gin.Context) {
	owner, ok := landlordIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req runRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.CreateRun(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	httpx.Created(c, runViewOf(result))
}
