package location

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	"rental-management/internal/platform/apperr"
	"rental-management/internal/platform/db"
	"rental-management/internal/platform/httpx"
)

// cacheControl lets browsers and proxies keep an answer for a day: the divisions change a few times a year at most.
const cacheControl = "public, max-age=86400"

// reader is what the handlers need from the store.
type reader interface {
	Provinces(ctx context.Context) ([]Item, error)
	Wards(ctx context.Context, provinceCode int32) (items []Item, found bool, err error)
}

type handler struct{ store reader }

// listProvinces answers GET /v1/provinces.
func (h handler) listProvinces(c *gin.Context) {
	items, err := h.store.Provinces(c.Request.Context())
	if err != nil {
		httpx.Fail(c, wrap(err))
		return
	}
	c.Header("Cache-Control", cacheControl)
	httpx.OK(c, items)
}

// listWards answers GET /v1/provinces/{code}/wards.
func (h handler) listWards(c *gin.Context) {
	code, err := strconv.ParseInt(c.Param("code"), 10, 32)
	if err != nil {
		httpx.Fail(c, apperr.E(apperr.KindNotFound, apperr.CodeProvinceNotFound))
		return
	}
	items, found, err := h.store.Wards(c.Request.Context(), int32(code))
	if err != nil {
		httpx.Fail(c, wrap(err))
		return
	}
	if !found {
		httpx.Fail(c, apperr.E(apperr.KindNotFound, apperr.CodeProvinceNotFound))
		return
	}
	c.Header("Cache-Control", cacheControl)
	httpx.OK(c, items)
}

// wrap turns a store error into the API error: an overloaded database is a 503, anything else a 500.
func wrap(err error) *apperr.Error {
	if db.IsBusy(err) {
		return apperr.Busy(err)
	}
	return apperr.Internal(err)
}
