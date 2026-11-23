package listinghttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	usecase "rental-management/internal/usecase/listing"
)

// publicCacheControl lets browsers and shared caches keep the public answers for a minute: the same for every
// visitor, and a room that was just rented may show for that long (as in the Laravel API's own cache).
const publicCacheControl = "public, max-age=60"

// OfferHandler serves the search of find rooms: signed in renters get the rooms with their address, guests the public
// side only.
type OfferHandler struct {
	uc   *usecase.Offers
	urls FileURLs
}

// NewOfferHandler returns the handler.
func NewOfferHandler(uc *usecase.Offers, urls FileURLs) *OfferHandler {
	return &OfferHandler{uc: uc, urls: urls}
}

func (h *OfferHandler) search(c *gin.Context) (usecase.OfferPage, httpx.PageRequest, bool) {
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return usecase.OfferPage{}, page, false
	}
	filter, err := offerFilter(c)
	if err != nil {
		httpx.Fail(c, err)
		return usecase.OfferPage{}, page, false
	}
	result, err := h.uc.Search(c.Request.Context(), filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return usecase.OfferPage{}, page, false
	}
	return result, page, true
}

// List answers GET /v1/vacant-rooms: the rooms on find rooms, for a signed in renter.
func (h *OfferHandler) List(c *gin.Context) {
	result, page, ok := h.search(c)
	if !ok {
		return
	}
	out := make([]OfferView, len(result.Items))
	for i, o := range result.Items {
		out[i] = offerViewOf(o, h.urls)
	}
	httpx.List(c, out, httpx.TotalMeta(page, result.Total))
}

// Get answers GET /v1/vacant-rooms/{id}.
func (h *OfferHandler) Get(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Detail(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	httpx.OK(c, OfferDetailView{OfferView: offerViewOf(d.Offer, h.urls), Charges: chargeViewsOf(d.Charges), Pictures: pictureViewsOf(d.Pictures, h.urls)})
}

// Landlord answers GET /v1/landlords/{id}: a landlord as a renter may see them.
func (h *OfferHandler) Landlord(c *gin.Context) {
	h.landlord(c, "/v1/vacant-rooms", false)
}

// PublicList answers GET /v1/public/rooms: the same rooms, public side only, for anybody.
func (h *OfferHandler) PublicList(c *gin.Context) {
	result, page, ok := h.search(c)
	if !ok {
		return
	}
	out := make([]PublicOfferView, len(result.Items))
	for i, o := range result.Items {
		out[i] = publicOfferViewOf(o, h.urls)
	}
	c.Header("Cache-Control", publicCacheControl)
	httpx.List(c, out, httpx.TotalMeta(page, result.Total))
}

// PublicGet answers GET /v1/public/rooms/{id}.
func (h *OfferHandler) PublicGet(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Detail(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	c.Header("Cache-Control", publicCacheControl)
	httpx.OK(c, PublicOfferDetailView{PublicOfferView: publicOfferViewOf(d.Offer, h.urls), Charges: chargeViewsOf(d.Charges), Pictures: pictureViewsOf(d.Pictures, h.urls)})
}

// PublicLandlord answers GET /v1/public/landlords/{id}.
func (h *OfferHandler) PublicLandlord(c *gin.Context) {
	h.landlord(c, "/v1/public/rooms", true)
}

func (h *OfferHandler) landlord(c *gin.Context, roomsPath string, cacheable bool) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	card, err := h.uc.Landlord(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapListingError(err))
		return
	}
	if cacheable {
		c.Header("Cache-Control", publicCacheControl)
	}
	httpx.OK(c, landlordViewOf(card, roomsPath))
}
