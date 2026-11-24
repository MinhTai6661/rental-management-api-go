package adminhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// ProfileHandler serves the landlord's own business profile.
type ProfileHandler struct {
	uc     *usecase.Profiles
	binder *httpx.Binder
}

// NewProfileHandler returns the handler.
func NewProfileHandler(uc *usecase.Profiles, binder *httpx.Binder) *ProfileHandler {
	return &ProfileHandler{uc: uc, binder: binder}
}

// ProfileView is the business profile of a landlord.
type ProfileView struct {
	BusinessName  string    `json:"business_name"`
	Phone         *string   `json:"phone"`
	WardCode      *int32    `json:"ward_code"`
	AddressDetail *string   `json:"address_detail"`
	Description   *string   `json:"description"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func profileView(p domain.Profile) ProfileView {
	return ProfileView{
		BusinessName: p.BusinessName, Phone: p.Phone, WardCode: p.WardCode, AddressDetail: p.AddressDetail,
		Description: p.Description, UpdatedAt: p.UpdatedAt.UTC(),
	}
}

// Get answers GET /v1/landlord/profile: the profile, or null when the landlord wrote none yet.
func (h *ProfileHandler) Get(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	p, err := h.uc.Get(c.Request.Context(), actor)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	if p == nil {
		httpx.OK(c, nil)
		return
	}
	httpx.OK(c, profileView(*p))
}

type profileRequest struct {
	BusinessName  string  `json:"business_name" validate:"required,max=150"`
	Phone         *string `json:"phone" validate:"omitempty,max=20"`
	WardCode      *int32  `json:"ward_code"`
	AddressDetail *string `json:"address_detail" validate:"omitempty,max=500"`
	Description   *string `json:"description" validate:"omitempty,max=2000"`
}

// Put answers PUT /v1/landlord/profile.
func (h *ProfileHandler) Put(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req profileRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, err := h.uc.Put(c.Request.Context(), actor, domain.ProfileInput{
		BusinessName: req.BusinessName, Phone: req.Phone, WardCode: req.WardCode, AddressDetail: req.AddressDetail, Description: req.Description,
	})
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, profileView(p))
}
