package contracthttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// refOf reads the caller and the invitation a request names; it answers the request itself when either is bad.
func (h *InvitationHandler) refOf(c *gin.Context) (usecase.InvitationRef, bool) {
	var req answerRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return usecase.InvitationRef{}, false
	}
	ref, err := req.ref()
	if err != nil {
		httpx.Fail(c, err)
		return usecase.InvitationRef{}, false
	}
	return ref, true
}

// Preview answers POST /v1/contract-invitations/preview: the offer, before the person answers it.
func (h *InvitationHandler) Preview(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	ref, ok := h.refOf(c)
	if !ok {
		return
	}
	offer, err := h.answers.Preview(c.Request.Context(), user, ref)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, invitationOfferViewOf(offer, h.clock.Now(), h.urls))
}

// Accept answers POST /v1/contract-invitations/accept: the caller accepts the offer and gets their contract.
func (h *InvitationHandler) Accept(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	ref, ok := h.refOf(c)
	if !ok {
		return
	}
	d, err := h.answers.Accept(c.Request.Context(), user, ref)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.Created(c, contractViewOf(d, domain.Viewer{UserID: user}, h.urls))
}

// Decline answers POST /v1/contract-invitations/decline.
func (h *InvitationHandler) Decline(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	ref, ok := h.refOf(c)
	if !ok {
		return
	}
	if err := h.answers.Decline(c.Request.Context(), user, ref); err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.NoContent(c)
}

// Inbox answers GET /v1/me/contract-invitations: the open offers addressed to the caller's account.
func (h *InvitationHandler) Inbox(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	items, total, err := h.answers.Inbox(c.Request.Context(), user, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.List(c, invitationOfferViewsOf(items, h.clock.Now(), h.urls), httpx.TotalMeta(page, total))
}
