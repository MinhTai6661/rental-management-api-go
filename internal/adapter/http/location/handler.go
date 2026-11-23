package locationhttp

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/location"
	usecase "rental-management/internal/usecase/location"
)

// locationCacheControl lets browsers and proxies keep an answer for a day: the divisions change a few times a
// year at most.
const locationCacheControl = "public, max-age=86400"

// LocationHandler serves the public province and ward routes.
type LocationHandler struct{ uc *usecase.Locations }

// NewLocationHandler returns the handler.
func NewLocationHandler(uc *usecase.Locations) *LocationHandler { return &LocationHandler{uc: uc} }

// ListProvinces answers GET /v1/provinces.
func (h *LocationHandler) ListProvinces(c *gin.Context) {
	items, err := h.uc.Provinces(c.Request.Context())
	if err != nil {
		httpx.Fail(c, mapLocationError(err))
		return
	}
	c.Header("Cache-Control", locationCacheControl)
	httpx.OK(c, divisionViewsOf(items))
}

// ListWards answers GET /v1/provinces/{code}/wards. A code that is not a number is a province that does not exist.
func (h *LocationHandler) ListWards(c *gin.Context) {
	code, err := strconv.ParseInt(c.Param("code"), 10, 32)
	if err != nil {
		httpx.Fail(c, mapLocationError(domain.ErrProvinceNotFound))
		return
	}
	items, err := h.uc.Wards(c.Request.Context(), int32(code))
	if err != nil {
		httpx.Fail(c, mapLocationError(err))
		return
	}
	c.Header("Cache-Control", locationCacheControl)
	httpx.OK(c, divisionViewsOf(items))
}
