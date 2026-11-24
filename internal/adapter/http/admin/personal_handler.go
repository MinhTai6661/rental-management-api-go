package adminhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// PersonalDataHandler serves the data subject's rights: a copy of their data and the erasure of their account.
type PersonalDataHandler struct {
	uc     *usecase.PersonalData
	binder *httpx.Binder
}

// NewPersonalDataHandler returns the handler.
func NewPersonalDataHandler(uc *usecase.PersonalData, binder *httpx.Binder) *PersonalDataHandler {
	return &PersonalDataHandler{uc: uc, binder: binder}
}

// exportView is the downloaded copy of a person's data.
type exportView struct {
	Account         domain.ExportAccount    `json:"account"`
	LandlordProfile *ProfileView            `json:"landlord_profile"`
	Rooms           []domain.ExportRoom     `json:"rooms"`
	RenterContracts []domain.ExportContract `json:"contracts_as_renter"`
	Payments        []domain.ExportPayment  `json:"payments"`
	ExportedAt      time.Time               `json:"exported_at"`
}

// Export answers GET /v1/profile/export: a JSON file with the data kept about the caller.
func (h *PersonalDataHandler) Export(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	data, err := h.uc.Export(c.Request.Context(), actor)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	view := exportView{
		Account: data.Account, Rooms: nonNil(data.Rooms), RenterContracts: nonNil(data.Contracts), Payments: nonNil(data.Payments),
		ExportedAt: time.Now().UTC(),
	}
	if data.Landlord != nil {
		p := profileView(*data.Landlord)
		view.LandlordProfile = &p
	}
	c.Header("Content-Disposition", `attachment; filename="personal-data.json"`)
	httpx.OK(c, view)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ErasureCheck answers GET /v1/profile/erasure-check: why the account cannot be erased yet (an empty list: it can).
func (h *PersonalDataHandler) ErasureCheck(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	blockers, err := h.uc.ErasureCheck(c.Request.Context(), actor)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"blockers": blockers})
}

type eraseRequest struct {
	Password string `json:"password" validate:"required,max=128"`
	Confirm  bool   `json:"confirm" validate:"accepted"`
}

// Erase answers DELETE /v1/profile {password, confirm}.
func (h *PersonalDataHandler) Erase(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req eraseRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Erase(c.Request.Context(), actor, req.Password); err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.NoContent(c)
}
