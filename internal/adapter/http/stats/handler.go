// Package statshttp is the HTTP side of the statistics: a landlord's figures about their rooms, the platform's figures
// for the staff and the system's figures for a super admin. Every answer is `{"data": ...}` and holds only totals.
package statshttp

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/stats"
	usecase "rental-management/internal/usecase/stats"
)

// PropertyHandler serves /stats/property/*.
type PropertyHandler struct {
	uc  *usecase.Property
	now func() time.Time
}

// NewPropertyHandler returns the handler; now is the clock that decides "today" for the default range.
func NewPropertyHandler(uc *usecase.Property, now func() time.Time) *PropertyHandler {
	return &PropertyHandler{uc: uc, now: now}
}

// ask reads the query of the request (RequireAuth and the landlord role ran before).
func (h *PropertyHandler) ask(c *gin.Context, defaultLimit int) (usecase.Query, bool) {
	p, ok := httpx.PrincipalOf(c)
	id, err := uuid.Parse(p.UserID)
	if !ok || err != nil {
		httpx.Fail(c, apperr.Unauthenticated())
		return usecase.Query{}, false
	}
	q, err := queryOf(c, id, p.Role, domain.Day(h.now()), defaultLimit)
	if err != nil {
		httpx.Fail(c, err)
		return usecase.Query{}, false
	}
	return q, true
}

// serve is the shape every endpoint shares: read the query, work the figure out, shape it.
func serve[T any, V any](h *PropertyHandler, defaultLimit int, work func(*gin.Context, usecase.Query) (T, error), view func(T) V) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, ok := h.ask(c, defaultLimit)
		if !ok {
			return
		}
		out, err := work(c, q)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, view(out))
	}
}

// Dashboard answers GET /v1/stats/property/dashboard: overview, revenue, occupancy, the top rooms and the utilities in
// one answer (?from, ?to, ?building_id, ?sort, ?limit; the ranking is 10 long unless asked otherwise).
func (h *PropertyHandler) Dashboard() gin.HandlerFunc {
	return serve(h, dashboardLimit, func(c *gin.Context, q usecase.Query) (usecase.Dashboard, error) {
		return h.uc.Dashboard(c.Request.Context(), q)
	}, dashboardOf)
}

// Overview answers GET /v1/stats/property/overview.
func (h *PropertyHandler) Overview() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) (usecase.Overview, error) {
		return h.uc.Overview(c.Request.Context(), q)
	}, overviewOf)
}

// Revenue answers GET /v1/stats/property/revenue.
func (h *PropertyHandler) Revenue() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) (usecase.Revenue, error) {
		return h.uc.Revenue(c.Request.Context(), q)
	}, revenueOf)
}

// Occupancy answers GET /v1/stats/property/occupancy.
func (h *PropertyHandler) Occupancy() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) ([]usecase.OccupancyMonth, error) {
		return h.uc.Occupancy(c.Request.Context(), q)
	}, occupancyOf)
}

// Rooms answers GET /v1/stats/property/rooms (?sort=billed|collected|occupancy, ?limit up to 100).
func (h *PropertyHandler) Rooms() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) ([]usecase.RoomFigures, error) {
		return h.uc.Rooms(c.Request.Context(), q)
	}, roomsOf)
}

// Buildings answers GET /v1/stats/property/buildings (?limit up to 100; it takes no building filter).
func (h *PropertyHandler) Buildings() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) ([]usecase.BuildingFigures, error) {
		return h.uc.Buildings(c.Request.Context(), q)
	}, buildingsOf)
}

// Utilities answers GET /v1/stats/property/utilities.
func (h *PropertyHandler) Utilities() gin.HandlerFunc {
	return serve(h, rankingLimit, func(c *gin.Context, q usecase.Query) ([]usecase.UtilityMonth, error) {
		return h.uc.Utilities(c.Request.Context(), q)
	}, utilitiesOf)
}

// PlatformHandler serves /stats/admin/* and /stats/system/*.
type PlatformHandler struct {
	uc  *usecase.Platform
	now func() time.Time
}

// NewPlatformHandler returns the handler.
func NewPlatformHandler(uc *usecase.Platform, now func() time.Time) *PlatformHandler {
	return &PlatformHandler{uc: uc, now: now}
}

// Overview answers GET /v1/stats/admin/overview.
func (h *PlatformHandler) Overview(c *gin.Context) {
	out, err := h.uc.Overview(c.Request.Context())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, platformOverviewOf(out))
}

// Growth answers GET /v1/stats/admin/growth (?from, ?to; the last 12 months by default, 36 at most).
func (h *PlatformHandler) Growth(c *gin.Context) {
	var fields []apperr.Field
	rng, _ := rangeOf(c, domain.Day(h.now()), &fields)
	if len(fields) > 0 {
		httpx.Fail(c, apperr.Invalid(fields...))
		return
	}
	out, err := h.uc.Growth(c.Request.Context(), rng)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, growthOf(out))
}

// System answers GET /v1/stats/system/overview.
func (h *PlatformHandler) System(c *gin.Context) {
	out, err := h.uc.System(c.Request.Context())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, systemOf(out))
}
