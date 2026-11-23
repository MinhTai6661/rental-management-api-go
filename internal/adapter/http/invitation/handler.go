// Package invitationhttp is the HTTP side of the invitation use cases that are shared by every kind of invitation:
// the landlord's lookup of the account to invite and the public link that stops invitation emails.
package invitationhttp

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/invitation"
	usecase "rental-management/internal/usecase/invitation"
)

// InviteeHandler serves the lookup of an invitee and the opt-out link.
type InviteeHandler struct {
	uc     *usecase.Invitees
	binder *httpx.Binder
}

// NewInviteeHandler returns the handler.
func NewInviteeHandler(uc *usecase.Invitees, binder *httpx.Binder) *InviteeHandler {
	return &InviteeHandler{uc: uc, binder: binder}
}

// LookupRequest is the body of POST /invitee-lookup.
type LookupRequest struct {
	By    string `json:"by" validate:"required"`
	Value string `json:"value" validate:"required,max=190"`
}

// InviteeView is what a landlord learns about an account they can invite: enough to recognize it, nothing else (no
// phone, no address, only a masked email).
type InviteeView struct {
	ID          string  `json:"id"`
	FullName    string  `json:"full_name"`
	EmailMasked *string `json:"email_masked"`
}

// LookupView is the answer of the lookup. Anybody who cannot be invited reads exactly like nobody.
type LookupView struct {
	Found bool         `json:"found"`
	User  *InviteeView `json:"user"`
}

// Lookup answers POST /v1/invitee-lookup.
func (h *InviteeHandler) Lookup(c *gin.Context) {
	p, ok := httpx.PrincipalOf(c)
	if !ok {
		httpx.Fail(c, apperr.Unauthenticated())
		return
	}
	landlord, err := uuid.Parse(p.UserID)
	if err != nil {
		httpx.Fail(c, apperr.Unauthenticated())
		return
	}
	var req LookupRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	by, ok := domain.ParseLookupBy(req.By)
	if !ok {
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "by", Rule: apperr.RuleOneOf}))
		return
	}
	person, err := h.uc.Lookup(c.Request.Context(), landlord, by, req.Value)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if person == nil {
		httpx.OK(c, LookupView{})
		return
	}
	httpx.OK(c, LookupView{Found: true, User: &InviteeView{
		ID: person.ID.String(), FullName: person.FullName, EmailMasked: domain.MaskEmail(person.Email),
	}})
}

// OptOutRequest is the body of POST /invitation-emails/opt-out.
type OptOutRequest struct {
	Email string `json:"email" validate:"required,email,max=190"`
	Token string `json:"token" validate:"required,max=128"`
}

// OptOut answers POST /v1/invitation-emails/opt-out: the link of an invitation email stops further ones.
func (h *InviteeHandler) OptOut(c *gin.Context) {
	var req OptOutRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.OptOut(c.Request.Context(), req.Email, req.Token); err != nil {
		if errors.Is(err, domain.ErrOptOutInvalid) {
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "token", Rule: apperr.RuleLinkInvalid}))
			return
		}
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}
