package contracthttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// InvitationHandler serves the offers of rooms by link: the landlord's side (make, list, copy the link, withdraw) and
// the invited person's (see, accept, decline, the inbox).
type InvitationHandler struct {
	uc      *usecase.Invitations
	answers *usecase.InvitationAnswers
	clock   usecase.Clock
	urls    FileURLs
	binder  *httpx.Binder
}

// NewInvitationHandler returns the handler. The clock tells which invitations have expired; urls builds the public
// address of a stored picture.
func NewInvitationHandler(uc *usecase.Invitations, answers *usecase.InvitationAnswers, clock usecase.Clock, urls FileURLs, binder *httpx.Binder) *InvitationHandler {
	return &InvitationHandler{uc: uc, answers: answers, clock: clock, urls: urls, binder: binder}
}

// List answers GET /v1/contract-invitations: the landlord's invitations, open ones first.
func (h *InvitationHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var status *domain.InvitationStatus
	raw, f := httpx.QueryOneOf(c, "status", invitationStatusNames()...)
	if f != nil {
		httpx.Fail(c, apperr.Invalid(*f))
		return
	}
	if raw != nil {
		s := domain.InvitationStatus(*raw)
		status = &s
	}
	items, total, err := h.uc.List(c.Request.Context(), landlord, status, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.List(c, invitationViewsOf(items, h.clock.Now()), httpx.TotalMeta(page, total))
}

func invitationStatusNames() []string {
	all := domain.InvitationStatuses()
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = string(s)
	}
	return out
}

// Create answers POST /v1/contract-invitations: the landlord offers a room; the link is shown here, once.
func (h *InvitationHandler) Create(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req createInvitationRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	issued, err := h.uc.Create(c.Request.Context(), landlord, in)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.Created(c, issuedInvitationViewOf(issued, h.clock.Now()))
}

// Get answers GET /v1/contract-invitations/{id}.
func (h *InvitationHandler) Get(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Get(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, invitationViewOf(d, h.clock.Now()))
}

// Link answers GET /v1/contract-invitations/{id}/link: the same link the landlord was given, while the offer waits.
func (h *InvitationHandler) Link(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	link, err := h.uc.Link(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, InvitationLinkView{Link: link.URL, ExpiresAt: link.ExpiresAt})
}

// Regenerate answers POST /v1/contract-invitations/{id}/regenerate: a new link; the old one stops working.
func (h *InvitationHandler) Regenerate(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	renewed, err := h.uc.Regenerate(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.OK(c, RenewedInvitationView{Invitation: invitationViewOf(renewed.Invitation, h.clock.Now()), Link: renewed.Link.URL})
}

// Revoke answers DELETE /v1/contract-invitations/{id}: the offer is withdrawn.
func (h *InvitationHandler) Revoke(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Revoke(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapContractError(err))
		return
	}
	httpx.NoContent(c)
}
