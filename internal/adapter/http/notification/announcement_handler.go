package notificationhttp

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	usecase "rental-management/internal/usecase/notification"
)

// ict is the business time zone of the days a client filters by.
var ict = time.FixedZone("ICT", 7*3600)

// AnnouncementHandler serves the announcement routes of the platform's admins (/admin/announcements) and of landlords
// (/my/announcements). The sender is the caller and the role they act in; a message that is not theirs is a 404.
type AnnouncementHandler struct {
	uc     *usecase.Announcements
	urls   FileURLs
	binder *httpx.Binder
}

// NewAnnouncementHandler returns the handler.
func NewAnnouncementHandler(uc *usecase.Announcements, urls FileURLs, binder *httpx.Binder) *AnnouncementHandler {
	return &AnnouncementHandler{uc: uc, urls: urls, binder: binder}
}

// senderOf is the caller as a sender: the person and the role they act in.
func senderOf(ginCtx *gin.Context) (domain.Sender, bool) {
	v, ok := viewerOf(ginCtx)
	if !ok {
		return domain.Sender{}, false
	}
	return domain.Sender{UserID: v.UserID, Role: v.Role}, true
}

// List answers GET /v1/{admin,my}/announcements (?status, ?from, ?to, ?type, ?channel, page).
func (handler *AnnouncementHandler) List(ginCtx *gin.Context) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(ginCtx, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	filters, err := filtersOf(ginCtx)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	rows, total, err := handler.uc.List(ginCtx.Request.Context(), sender, filters, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.List(ginCtx, rowViewsOf(rows), httpx.TotalMeta(page, total))
}

// filtersOf reads the filters of the list.
func filtersOf(ginCtx *gin.Context) (usecase.Filters, error) {
	var f usecase.Filters
	var fields []apperr.Field
	if raw := ginCtx.Query("status"); raw != "" {
		if s, ok := domain.ParseStatus(raw); ok {
			f.Status = &s
		} else {
			fields = append(fields, apperr.Field{Name: "status", Rule: apperr.RuleOneOf})
		}
	}
	if raw := ginCtx.Query("type"); raw != "" {
		if t, ok := domain.ParseType(raw); ok {
			f.Type = &t
		} else {
			fields = append(fields, apperr.Field{Name: "type", Rule: apperr.RuleOneOf})
		}
	}
	if raw := ginCtx.Query("channel"); raw != "" {
		if ch, ok := domain.ParseChannel(raw); ok {
			f.Channel = &ch
		} else {
			fields = append(fields, apperr.Field{Name: "channel", Rule: apperr.RuleOneOf})
		}
	}
	for _, detail := range []struct {
		name string
		dst  **time.Time
		end  bool
	}{{"from", &f.From, false}, {"to", &f.To, true}} {
		raw := ginCtx.Query(detail.name)
		if raw == "" {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", raw, ict)
		if err != nil {
			fields = append(fields, apperr.Field{Name: detail.name, Rule: apperr.RuleDate})
			continue
		}
		if detail.end {
			day = day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		}
		*detail.dst = &day
	}
	if len(fields) > 0 {
		return f, apperr.Invalid(fields...)
	}
	return f, nil
}

// Get answers GET /v1/{admin,my}/announcements/{id}.
func (handler *AnnouncementHandler) Get(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	detail, err := handler.uc.Get(ginCtx.Request.Context(), sender, id)
	handler.answer(ginCtx, http.StatusOK, detail, err)
}

// Create answers POST /v1/{admin,my}/announcements: it writes the message and, unless "publish" is false, puts it out.
func (handler *AnnouncementHandler) Create(ginCtx *gin.Context) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return
	}
	requestBody, ok := handler.read(ginCtx, true)
	if !ok {
		return
	}
	detail, err := handler.uc.Create(ginCtx.Request.Context(), sender, requestBody.content, requestBody.publish)
	handler.answer(ginCtx, http.StatusCreated, detail, err)
}

// Update answers PUT /v1/{admin,my}/announcements/{id}: a full replacement; "publish" puts a draft out and "reshow"
// shows the popup again to the people who closed it.
func (handler *AnnouncementHandler) Update(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	requestBody, ok := handler.read(ginCtx, false)
	if !ok {
		return
	}
	detail, err := handler.uc.Update(ginCtx.Request.Context(), sender, id, requestBody.content, requestBody.publish, requestBody.reshow)
	handler.answer(ginCtx, http.StatusOK, detail, err)
}

// Publish answers POST /v1/{admin,my}/announcements/{id}/publish.
func (handler *AnnouncementHandler) Publish(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	detail, err := handler.uc.Publish(ginCtx.Request.Context(), sender, id)
	handler.answer(ginCtx, http.StatusOK, detail, err)
}

// Unpublish answers POST /v1/{admin,my}/announcements/{id}/unpublish: back to a draft.
func (handler *AnnouncementHandler) Unpublish(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	detail, err := handler.uc.Unpublish(ginCtx.Request.Context(), sender, id)
	handler.answer(ginCtx, http.StatusOK, detail, err)
}

// Recall answers DELETE /v1/{admin,my}/announcements/{id}: the message leaves every inbox; 409 when already recalled.
func (handler *AnnouncementHandler) Recall(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	if err := handler.uc.Recall(ginCtx.Request.Context(), sender, id); err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.NoContent(ginCtx)
}

// Reach answers POST /v1/{admin,my}/announcements/reach: how many people the audience reaches now.
func (handler *AnnouncementHandler) Reach(ginCtx *gin.Context) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return
	}
	var requestBody reachRequest
	if err := handler.binder.JSON(ginCtx, &requestBody); err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	scope, criteria, people, err := requestBody.parse()
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	n, err := handler.uc.Reach(ginCtx.Request.Context(), sender, usecase.ReachInput{Scope: scope, Criteria: criteria, PeopleIDs: people})
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.OK(ginCtx, gin.H{"recipient_count": n})
}

// People answers GET /v1/my/announcements/people (?search, page): the running tenants a landlord can write to.
func (handler *AnnouncementHandler) People(ginCtx *gin.Context) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(ginCtx, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	people, total, err := handler.uc.People(ginCtx.Request.Context(), sender, strings.TrimSpace(ginCtx.Query("search")), page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	views := make([]personView, 0, len(people))
	for _, pageRequest := range people {
		v := personViewOf(pageRequest)
		v.Kind = "tenant"
		views = append(views, v)
	}
	httpx.List(ginCtx, views, httpx.TotalMeta(page, total))
}

// target reads the caller and the id of the message in the path.
func target(ginCtx *gin.Context) (domain.Sender, uuid.UUID, bool) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return domain.Sender{}, uuid.UUID{}, false
	}
	id, err := httpx.ParseUUIDParam(ginCtx, "id")
	if err != nil {
		httpx.Fail(ginCtx, err)
		return domain.Sender{}, uuid.UUID{}, false
	}
	return sender, id, true
}

// read binds and parses the body of a create or update.
func (handler *AnnouncementHandler) read(ginCtx *gin.Context, defaultPublish bool) (parsed, bool) {
	var requestBody announcementRequest
	if err := handler.binder.JSON(ginCtx, &requestBody); err != nil {
		httpx.Fail(ginCtx, err)
		return parsed{}, false
	}
	pageRequest, err := requestBody.parse(defaultPublish)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return parsed{}, false
	}
	return pageRequest, true
}

func (handler *AnnouncementHandler) answer(ginCtx *gin.Context, status int, detail domain.AnnouncementDetail, err error) {
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	if status == http.StatusCreated {
		httpx.Created(ginCtx, detailViewOf(detail, handler.urls))
		return
	}
	httpx.OK(ginCtx, detailViewOf(detail, handler.urls))
}
