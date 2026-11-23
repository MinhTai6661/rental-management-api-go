package property

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/platform/apperr"
	"rental-management/internal/platform/httpx"
)

// Request bodies. The validate tags are the rules of the API; errors are reported under the json names.

type chargeRequest struct {
	ID        *uuid.UUID `json:"id"`
	Name      string     `json:"name" validate:"required,max=100"`
	Kind      string     `json:"kind" validate:"required,oneof=service electricity water internet parking cleaning other"`
	AmountVND *int64     `json:"amount_vnd" validate:"required,min=0,max=1000000000000"`
	Unit      string     `json:"unit" validate:"required,oneof=per_period per_person per_kwh per_cubic_meter one_time"`
	Note      *string    `json:"note" validate:"omitempty,max=255"`
}

type buildingRequest struct {
	Name                string          `json:"name" validate:"required,max=50"`
	WardCode            *int32          `json:"ward_code"`
	AddressDetail       string          `json:"address_detail" validate:"required,max=500"`
	Description         *string         `json:"description" validate:"omitempty,max=2000"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
}

type copyBuildingRequest struct {
	Name         string `json:"name" validate:"required,max=50"`
	IncludeRooms bool   `json:"include_rooms"`
}

type roomRequest struct {
	BuildingID          *uuid.UUID      `json:"building_id"`
	Name                string          `json:"name" validate:"required,max=50"`
	WardCode            *int32          `json:"ward_code"`
	AddressDetail       *string         `json:"address_detail" validate:"omitempty,max=500"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Description         *string         `json:"description" validate:"omitempty,max=5000"`
	Status              *string         `json:"status" validate:"omitempty,oneof=available rented maintenance"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
	Inherit             *[]string       `json:"inherit" validate:"omitempty,dive,oneof=price area rental_period description charges media"`
}

type runRequest struct {
	NamePrefix          string          `json:"name_prefix" validate:"max=50"`
	From                *int32          `json:"from" validate:"required,min=0,max=999999"`
	To                  *int32          `json:"to" validate:"required,min=0,max=999999"`
	Padding             *int32          `json:"padding" validate:"omitempty,min=0,max=10"`
	Status              *string         `json:"status" validate:"omitempty,oneof=available maintenance"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Description         *string         `json:"description" validate:"omitempty,max=5000"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
}

type bulkStatusRequest struct {
	IDs    []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
	Status string      `json:"status" validate:"required,oneof=available rented maintenance"`
}

type idsRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
}

type orderRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required,min=1,max=1000,unique"`
}

func (r buildingRequest) input() BuildingInput {
	return BuildingInput{
		Name: r.Name, WardCode: r.WardCode, AddressDetail: r.AddressDetail, Description: r.Description,
		PriceVND: r.PriceVND, Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Charges: chargeInputs(r.Charges),
	}
}

// chargeInputs converts the charges of a request; nil stays nil (the list is not part of the request).
func chargeInputs(items []chargeRequest) []ChargeInput {
	if items == nil {
		return nil
	}
	out := make([]ChargeInput, len(items))
	for i, c := range items {
		out[i] = ChargeInput{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: *c.AmountVND, Unit: c.Unit, Note: c.Note}
	}
	return out
}

func (r roomRequest) input() RoomInput {
	in := RoomInput{
		BuildingID: r.BuildingID, Name: r.Name, WardCode: r.WardCode, AddressDetail: r.AddressDetail, PriceVND: r.PriceVND,
		Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Description: r.Description, Status: r.Status, Charges: chargeInputs(r.Charges),
	}
	if r.Inherit != nil {
		groups := make([]Group, len(*r.Inherit))
		for i, g := range *r.Inherit {
			groups[i] = Group(g)
		}
		in.Inherit = &groups
	}
	return in
}

func (r runRequest) input() RunInput {
	in := RunInput{
		NamePrefix: r.NamePrefix, From: int(*r.From), To: int(*r.To), Status: r.Status, PriceVND: r.PriceVND,
		Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Description: r.Description, Charges: chargeInputs(r.Charges),
	}
	if r.Padding != nil {
		in.Padding = int(*r.Padding)
	}
	return in
}

type handler struct {
	svc    *Service
	binder *httpx.Binder
}

// landlordID is the caller of the request (RequireAuth ran before).
func landlordID(c *gin.Context) (uuid.UUID, bool) {
	p, ok := httpx.PrincipalOf(c)
	if ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return id, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return uuid.Nil, false
}

func (h handler) listBuildings(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var fields []apperr.Field
	filter := BuildingFilter{Name: c.Query("name")}
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
	items, total, err := h.svc.ListBuildings(c.Request.Context(), owner, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.List(c, items, httpx.TotalMeta(page, total))
}

func int32Ptr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // bounded by QueryInt64
	return &n
}

func (h handler) getBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.svc.GetBuilding(c.Request.Context(), owner, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, detail)
}

func (h handler) createBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req buildingRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.svc.CreateBuilding(c.Request.Context(), owner, req.input())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, detail)
}

func (h handler) updateBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
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
	detail, err := h.svc.UpdateBuilding(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, detail)
}

func (h handler) deleteBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.DeleteBuilding(c.Request.Context(), owner, id); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) bulkDeleteBuildings(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req idsRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.svc.BulkDeleteBuildings(c.Request.Context(), owner, req.IDs)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkView(c, result)})
}

func (h handler) reorderBuildings(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req orderRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.ReorderBuildings(c.Request.Context(), owner, req.IDs); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) copyBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
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
	result, err := h.svc.CopyBuilding(c.Request.Context(), owner, id, req.Name, req.IncludeRooms)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, result)
}

type bulkFailureView struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

type bulkResultView struct {
	Done   []uuid.UUID       `json:"done"`
	Failed []bulkFailureView `json:"failed"`
}

// bulkView translates the refusals of a bulk operation into the language of the request.
func bulkView(c *gin.Context, r BulkResult) bulkResultView {
	l := httpx.Localize(c)
	out := bulkResultView{Done: r.Done, Failed: make([]bulkFailureView, 0, len(r.Failed))}
	for _, f := range r.Failed {
		out.Failed = append(out.Failed, bulkFailureView{ID: f.ID, Name: f.Name, Code: f.Code, Message: l.T("errors."+f.Code, f.Params)})
	}
	return out
}

func (h handler) listRooms(c *gin.Context) {
	owner, ok := landlordID(c)
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
	items, total, err := h.svc.ListRooms(c.Request.Context(), owner, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.List(c, items, httpx.TotalMeta(page, total))
}

// roomFilter reads the list filters of the rooms; every bad value is reported in one 422.
func roomFilter(c *gin.Context) (RoomFilter, error) {
	var fields []apperr.Field
	keep := func(f *apperr.Field) {
		if f != nil {
			fields = append(fields, *f)
		}
	}
	filter := RoomFilter{Name: c.Query("name")}
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
		return RoomFilter{}, apperr.Invalid(fields...)
	}
	return filter, nil
}

func (h handler) getRoom(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.svc.GetRoom(c.Request.Context(), owner, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, detail)
}

func (h handler) createRoom(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req roomRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	detail, err := h.svc.CreateRoom(c.Request.Context(), owner, req.input())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, detail)
}

func (h handler) updateRoom(c *gin.Context) {
	owner, ok := landlordID(c)
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
	detail, err := h.svc.UpdateRoom(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, detail)
}

func (h handler) deleteRoom(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.DeleteRoom(c.Request.Context(), owner, id); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) bulkDeleteRooms(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req idsRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.svc.BulkDeleteRooms(c.Request.Context(), owner, req.IDs)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkView(c, result)})
}

func (h handler) bulkRoomStatus(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req bulkStatusRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.svc.BulkSetRoomStatus(c.Request.Context(), owner, req.IDs, Status(req.Status))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkView(c, result)})
}

func (h handler) reorderRooms(c *gin.Context) {
	owner, ok := landlordID(c)
	if !ok {
		return
	}
	var req orderRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.ReorderRooms(c.Request.Context(), owner, req.IDs); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) createRoomsInBuilding(c *gin.Context) {
	owner, ok := landlordID(c)
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
	result, err := h.svc.CreateRoomsInBuilding(c.Request.Context(), owner, id, req.input())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, result)
}

// ---- pictures ----

type arrangeRequest struct {
	Items []arrangeItemRequest `json:"items" validate:"required,min=1,max=50,dive"`
}

type arrangeItemRequest struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind" validate:"required,oneof=photo cover"`
}

// readUpload reads the multipart picture of the request into an Upload.
func (h handler) readUpload(c *gin.Context) (Upload, error) {
	up, err := httpx.ReadUpload(c, "file", int64(h.svc.MaxUploadBytes()))
	if err != nil {
		return Upload{}, err
	}
	return Upload{FileName: up.FileName, Data: up.Data, Kind: up.Values["kind"]}, nil
}

// mediaOwner reads the owner of the pictures from the path: a room or a building.
func mediaOwner(c *gin.Context, room bool) (MediaOwner, error) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		return MediaOwner{}, err
	}
	if room {
		return RoomMedia(id), nil
	}
	return BuildingMedia(id), nil
}

func (h handler) uploadMedia(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordID(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		up, err := h.readUpload(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		view, err := h.svc.AddMedia(c.Request.Context(), owner, target, up)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.Created(c, view)
	}
}

func (h handler) replaceMediaFile(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordID(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		mediaID, err := httpx.ParseUUIDParam(c, "media_id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		up, err := h.readUpload(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		view, err := h.svc.ReplaceMediaFile(c.Request.Context(), owner, target, mediaID, up)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, view)
	}
}

func (h handler) arrangeMedia(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordID(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var req arrangeRequest
		if err := h.binder.JSON(c, &req); err != nil {
			httpx.Fail(c, err)
			return
		}
		items := make([]ArrangeItem, len(req.Items))
		for i, it := range req.Items {
			items[i] = ArrangeItem(it)
		}
		views, err := h.svc.ArrangeMedia(c.Request.Context(), owner, target, items)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, views)
	}
}

func (h handler) deleteMedia(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordID(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		mediaID, err := httpx.ParseUUIDParam(c, "media_id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if err := h.svc.DeleteMedia(c.Request.Context(), owner, target, mediaID); err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.NoContent(c)
	}
}

// serveMedia answers the public picture route. The URL holds an unguessable, never reused key, so the answer can be
// cached for ever and the key's file name is the validator.
func (h handler) serveMedia(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	body, info, err := h.svc.OpenMedia(c.Request.Context(), key)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer func() { _ = body.Close() }()

	etag := mediaETag(key)
	header := c.Writer.Header()
	header.Set("ETag", etag)
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
	header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.DataFromReader(http.StatusOK, info.Size, info.ContentType, body, nil)
}
