package listinghttp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/listing"
	usecase "rental-management/internal/usecase/listing"
)

// ReviewHandler serves the admins' review of listings.
type ReviewHandler struct {
	uc     *usecase.Reviews
	urls   FileURLs
	binder *httpx.Binder
}

// NewReviewHandler returns the handler.
func NewReviewHandler(uc *usecase.Reviews, urls FileURLs, binder *httpx.Binder) *ReviewHandler {
	return &ReviewHandler{uc: uc, urls: urls, binder: binder}
}

// List answers GET /v1/admin/listings: the listings in a status (the waiting ones by default) with how many there
// are per status. A draft is the landlord's own business, so it is never listed.
func (h *ReviewHandler) List(c *gin.Context) {
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
	result, err := h.uc.List(c.Request.Context(), filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	writeList(c, result, page, h.urls, true)
}

// Get answers GET /v1/admin/listings/{id}: 404 for a draft.
func (h *ReviewHandler) Get(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.OK(c, listingDetailViewOf(d, h.urls, true))
}

// Approve answers POST /v1/admin/listings/{id}/approve: the listing goes on find rooms.
func (h *ReviewHandler) Approve(c *gin.Context) {
	h.decide(c, false, func(c *gin.Context, admin, id uuid.UUID, note *string) error {
		d, err := h.uc.Approve(c.Request.Context(), admin, id, note)
		return h.write(c, d, err)
	})
}

// Reject answers POST /v1/admin/listings/{id}/reject: the reason is required.
func (h *ReviewHandler) Reject(c *gin.Context) {
	h.decide(c, true, func(c *gin.Context, admin, id uuid.UUID, note *string) error {
		d, err := h.uc.Reject(c.Request.Context(), admin, id, *note)
		return h.write(c, d, err)
	})
}

// TakeDown answers POST /v1/admin/listings/{id}/unpublish: a live listing leaves find rooms; the reason is required
// and goes to the landlord.
func (h *ReviewHandler) TakeDown(c *gin.Context) {
	h.decide(c, true, func(c *gin.Context, admin, id uuid.UUID, note *string) error {
		d, err := h.uc.TakeDown(c.Request.Context(), admin, id, *note)
		return h.write(c, d, err)
	})
}

func (h *ReviewHandler) write(c *gin.Context, d domain.Detail, err error) error {
	if err != nil {
		return err
	}
	httpx.OK(c, listingDetailViewOf(d, h.urls, true))
	return nil
}

// decide reads the caller, the listing and the note of a decision, checks the note when it is required, and runs it.
func (h *ReviewHandler) decide(c *gin.Context, noteRequired bool, run func(c *gin.Context, admin, id uuid.UUID, note *string) error) {
	admin, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req reviewRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if noteRequired {
		if _, err := requiredNote(req.Note); err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	if err := run(c, admin, id, req.Note); err != nil {
		httpx.Fail(c, mapListingError(err))
	}
}

// Bulk answers POST /v1/admin/listings/bulk: approve or reject several listings; a rejection carries one reason for
// all of them.
func (h *ReviewHandler) Bulk(c *gin.Context) {
	admin, ok := userIDOf(c)
	if !ok {
		return
	}
	var req bulkRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	note := ""
	if req.Note != nil {
		note = *req.Note
	}
	if req.Action == string(usecase.ActionReject) {
		if _, err := requiredNote(req.Note); err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	result, err := h.uc.Bulk(c.Request.Context(), admin, usecase.Action(req.Action), req.IDs, note)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": bulkViewOf(c, result)})
}
