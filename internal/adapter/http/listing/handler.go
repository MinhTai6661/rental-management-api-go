// Package listinghttp is the HTTP side of the listing use cases: a landlord's own listings, the admins' review of
// them, and the search of find rooms for signed in renters and for guests.
package listinghttp

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/listing"
	usecase "rental-management/internal/usecase/listing"
)

// listMeta is the pagination of a list of listings plus how many there are in each status.
type listMeta struct {
	httpx.Meta
	Counts map[string]int `json:"counts"`
}

func writeList(c *gin.Context, page usecase.Page, req httpx.PageRequest, urls FileURLs, admin bool) {
	counts := map[string]int{}
	for s, n := range page.Counts {
		counts[string(s)] = n
	}
	c.JSON(http.StatusOK, gin.H{
		"data": listingViewsOf(page.Items, urls, admin),
		"meta": listMeta{Meta: httpx.TotalMeta(req, page.Total), Counts: counts},
	})
}

// ListingHandler serves a landlord's own listings.
type ListingHandler struct {
	uc     *usecase.Listings
	urls   FileURLs
	binder *httpx.Binder
}

// NewListingHandler returns the handler. urls builds the public address of a stored picture.
func NewListingHandler(uc *usecase.Listings, urls FileURLs, binder *httpx.Binder) *ListingHandler {
	return &ListingHandler{uc: uc, urls: urls, binder: binder}
}

// List answers GET /v1/my/listings: the landlord's listings with how many there are per status.
func (h *ListingHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
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
	result, err := h.uc.List(c.Request.Context(), landlord, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	writeList(c, result, page, h.urls, false)
}

// Get answers GET /v1/my/listings/{id}.
func (h *ListingHandler) Get(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	d, err := h.uc.Get(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.OK(c, listingDetailViewOf(d, h.urls, false))
}

// Create answers POST /v1/my/listings: a draft that offers one of the landlord's rooms.
func (h *ListingHandler) Create(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req createRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Create(c.Request.Context(), landlord, usecase.CreateInput{RoomID: *req.RoomID, Title: req.Title, Description: req.Description})
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.Created(c, listingViewOf(d, h.urls, false))
}

// Update answers PUT /v1/my/listings/{id}: a partial update of the headline and the text.
func (h *ListingHandler) Update(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
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
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.OK(c, listingViewOf(d, h.urls, false))
}

// Submit answers POST /v1/my/listings/{id}/submit: the listing goes to the admins.
func (h *ListingHandler) Submit(c *gin.Context) {
	h.act(c, h.uc.Submit)
}

// Unpublish answers POST /v1/my/listings/{id}/unpublish: a live listing leaves find rooms.
func (h *ListingHandler) Unpublish(c *gin.Context) {
	h.act(c, h.uc.Unpublish)
}

func (h *ListingHandler) act(c *gin.Context, do func(ctx context.Context, landlord, id uuid.UUID) (domain.Detail, error)) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	d, err := do(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.OK(c, listingViewOf(d, h.urls, false))
}

// Delete answers DELETE /v1/my/listings/{id}: a live listing must be taken down first (409).
func (h *ListingHandler) Delete(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.NoContent(c)
}

// landlordAndID reads the caller and the listing id of a landlord's action on one listing.
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
