package contracthttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// RentalHandler serves the renter's own contracts ("my rooms").
type RentalHandler struct {
	uc   *usecase.Rentals
	urls FileURLs
}

// NewRentalHandler returns the handler.
func NewRentalHandler(uc *usecase.Rentals, urls FileURLs) *RentalHandler {
	return &RentalHandler{uc: uc, urls: urls}
}

// rentalMeta is the pagination of the renter's list plus how many contracts there are in each tab and how many running
// ones are in each state of money (for the filter chips).
type rentalMeta struct {
	httpx.Meta
	Counts        map[string]int `json:"counts"`
	PaymentCounts paymentCounts  `json:"payment_counts"`
}

type paymentCounts struct {
	All     int `json:"all"`
	Due     int `json:"due"`
	Review  int `json:"review"`
	Settled int `json:"settled"`
}

// List answers GET /v1/me/contracts?group=active|pending|history&payment=due|review|settled.
func (h *RentalHandler) List(c *gin.Context) {
	tenant, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	group, field := httpx.QueryOneOf(c, "group", usecase.GroupActive, usecase.GroupPending, usecase.GroupHistory)
	if field != nil {
		httpx.Fail(c, apperr.Invalid(*field))
		return
	}
	name := ""
	if group != nil {
		name = *group
	}
	payment, field := httpx.QueryOneOf(c, "payment", string(domain.PaymentDue), string(domain.PaymentReview), string(domain.PaymentSettled))
	if field != nil {
		httpx.Fail(c, apperr.Invalid(*field))
		return
	}
	var state *domain.PaymentState
	if payment != nil {
		s := domain.PaymentState(*payment)
		state = &s
	}
	result, err := h.uc.ListByPayment(c.Request.Context(), tenant, name, state, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	c.JSON(200, gin.H{
		"data": rentalViewsOf(result.Items, h.urls),
		"meta": rentalMeta{
			Meta: httpx.TotalMeta(page, result.Total), Counts: result.Counts,
			PaymentCounts: paymentCounts{
				All: result.PaymentCounts.All, Due: result.PaymentCounts.Due, Review: result.PaymentCounts.Review, Settled: result.PaymentCounts.Settled,
			},
		},
	})
}

// Get answers GET /v1/me/contracts/{id}: one of the renter's own contracts, with the room's costs and pictures.
func (h *RentalHandler) Get(c *gin.Context) {
	tenant, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Get(c.Request.Context(), tenant, id)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, rentalDetailViewOf(d, h.urls))
}
