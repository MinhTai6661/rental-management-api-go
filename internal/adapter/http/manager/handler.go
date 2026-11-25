package managerhttp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/manager"
	usecase "rental-management/internal/usecase/manager"
)

// InvitationHandler serves the landlord's invitations to manage rooms (make, list, copy the link, new link, withdraw)
// and the invited person's side (see, accept, decline, the inbox).
type InvitationHandler struct {
	uc      *usecase.Invitations
	answers *usecase.Answers
	clock   usecase.Clock
	binder  *httpx.Binder
}

// NewInvitationHandler returns the handler. The clock tells which invitations have expired.
func NewInvitationHandler(uc *usecase.Invitations, answers *usecase.Answers, clock usecase.Clock, binder *httpx.Binder) *InvitationHandler {
	return &InvitationHandler{uc: uc, answers: answers, clock: clock, binder: binder}
}

// List answers GET /v1/my/manager-invitations: the landlord's invitations, open ones first.
func (h *InvitationHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var status *domain.InvitationStatus
	names := make([]string, 0, 4)
	for _, s := range domain.InvitationStatuses() {
		names = append(names, string(s))
	}
	raw, f := httpx.QueryOneOf(c, "status", names...)
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
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.List(c, invitationViewsOf(items, h.clock.Now()), httpx.TotalMeta(page, total))
}

// Create answers POST /v1/my/manager-invitations: the landlord invites someone to manage rooms; the link is shown here, once.
func (h *InvitationHandler) Create(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req createRequest
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
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.Created(c, issuedViewOf(issued, h.clock.Now()))
}

// Link answers GET /v1/my/manager-invitations/{id}/link: the same link the landlord was given, while it waits.
func (h *InvitationHandler) Link(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	link, err := h.uc.Link(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.OK(c, LinkView{Link: link.URL, ExpiresAt: link.ExpiresAt})
}

// Regenerate answers POST /v1/my/manager-invitations/{id}/regenerate: a new link; the old one stops working.
func (h *InvitationHandler) Regenerate(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	renewed, err := h.uc.Regenerate(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.OK(c, issuedViewOf(renewed, h.clock.Now()))
}

// Revoke answers DELETE /v1/my/manager-invitations/{id}: the invitation is withdrawn.
func (h *InvitationHandler) Revoke(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Revoke(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.NoContent(c)
}

func landlordAndID(c *gin.Context) (landlord, id uuid.UUID, ok bool) {
	landlord, ok = userIDOf(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return uuid.Nil, uuid.Nil, false
	}
	return landlord, id, true
}

// Preview answers POST /v1/manager-invitations/preview.
func (h *InvitationHandler) Preview(c *gin.Context) {
	user, ref, ok := h.answerRef(c)
	if !ok {
		return
	}
	offer, err := h.answers.Preview(c.Request.Context(), user, ref)
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.OK(c, offerViewOf(offer, h.clock.Now()))
}

// Accept answers POST /v1/manager-invitations/accept: the rooms are handed over, all of them or none.
func (h *InvitationHandler) Accept(c *gin.Context) {
	user, ref, ok := h.answerRef(c)
	if !ok {
		return
	}
	res, err := h.answers.Accept(c.Request.Context(), user, ref)
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	view := AcceptedView{Assignments: make([]AcceptedAssignmentView, len(res.Assignments)), Rooms: res.Rooms}
	for i, a := range res.Assignments {
		view.Assignments[i] = AcceptedAssignmentView{ID: a.ID, RoomID: a.RoomID}
	}
	httpx.Created(c, view)
}

// Decline answers POST /v1/manager-invitations/decline.
func (h *InvitationHandler) Decline(c *gin.Context) {
	user, ref, ok := h.answerRef(c)
	if !ok {
		return
	}
	if err := h.answers.Decline(c.Request.Context(), user, ref); err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.NoContent(c)
}

func (h *InvitationHandler) answerRef(c *gin.Context) (uuid.UUID, usecase.Ref, bool) {
	user, ok := userIDOf(c)
	if !ok {
		return uuid.Nil, usecase.Ref{}, false
	}
	var req answerRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return uuid.Nil, usecase.Ref{}, false
	}
	ref, err := req.ref()
	if err != nil {
		httpx.Fail(c, err)
		return uuid.Nil, usecase.Ref{}, false
	}
	return user, ref, true
}

// Inbox answers GET /v1/me/manager-invitations: the open invitations addressed to the caller's account.
func (h *InvitationHandler) Inbox(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	items, total, err := h.answers.Inbox(c.Request.Context(), user, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.List(c, offerViewsOf(items, h.clock.Now()), httpx.TotalMeta(page, total))
}

// AssignmentHandler serves the landlord's hand-overs and the manager's own rooms.
type AssignmentHandler struct {
	uc      *usecase.Assignments
	managed *usecase.Managed
	binder  *httpx.Binder
}

// NewAssignmentHandler returns the handler.
func NewAssignmentHandler(uc *usecase.Assignments, managed *usecase.Managed, binder *httpx.Binder) *AssignmentHandler {
	return &AssignmentHandler{uc: uc, managed: managed, binder: binder}
}

// groupedMeta is the meta of the grouped list: the page, and every room of the landlord that already has a manager.
type groupedMeta struct {
	httpx.Meta
	BusyRoomIDs []uuid.UUID `json:"busy_room_ids"`
}

// List answers GET /v1/my/room-managers. With view=groups every row is one manager (their rooms come from a call with
// manager_id); without it every row is one room handed over, optionally of one manager.
func (h *AssignmentHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	view, f := httpx.QueryOneOf(c, "view", "groups", "rooms")
	if f != nil {
		httpx.Fail(c, apperr.Invalid(*f))
		return
	}
	managerID, f := httpx.QueryUUID(c, "manager_id")
	if f != nil {
		httpx.Fail(c, apperr.Invalid(*f))
		return
	}
	ctx := c.Request.Context()
	if view != nil && *view == "groups" {
		groups, total, err := h.uc.Groups(ctx, landlord, page.PerPage, page.Offset())
		if err != nil {
			httpx.Fail(c, mapManagerError(err))
			return
		}
		busy, err := h.uc.BusyRoomIDs(ctx, landlord)
		if err != nil {
			httpx.Fail(c, mapManagerError(err))
			return
		}
		if busy == nil {
			busy = []uuid.UUID{}
		}
		c.JSON(http.StatusOK, gin.H{"data": groupViewsOf(groups), "meta": groupedMeta{Meta: httpx.TotalMeta(page, total), BusyRoomIDs: busy}})
		return
	}
	items, total, err := h.uc.List(ctx, landlord, managerID, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.List(c, assignmentViewsOf(items, true), httpx.TotalMeta(page, total))
}

// Update answers PUT /v1/my/room-managers/{id}: give or take capabilities, switch the notice to the landlord.
func (h *AssignmentHandler) Update(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	var req updateRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Update(c.Request.Context(), landlord, id, in)
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.OK(c, assignmentViewOf(d, true))
}

// Revoke answers DELETE /v1/my/room-managers/{id}: the room is taken back.
func (h *AssignmentHandler) Revoke(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Revoke(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.NoContent(c)
}

// Bulk answers POST /v1/my/room-managers/bulk: revoke or update many hand-overs at once.
func (h *AssignmentHandler) Bulk(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req bulkRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	var (
		res usecase.BulkResult
		err error
	)
	if req.Action == "revoke" {
		res, err = h.uc.BulkRevoke(ctx, landlord, req.IDs)
	} else {
		var in usecase.UpdateInput
		if in, err = (updateRequest{Grant: req.Grant, Revoke: req.Revoke, NotifyLandlord: req.NotifyLandlord}).input(); err != nil {
			httpx.Fail(c, err)
			return
		}
		res, err = h.uc.BulkUpdate(ctx, landlord, req.IDs, in)
	}
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	done := res.Done
	if done == nil {
		done = []uuid.UUID{}
	}
	httpx.OK(c, BulkView{Done: done, Failed: []any{}})
}

// Capabilities answers GET /v1/manager-capabilities: what a landlord can hand over (a short fixed catalogue).
func (h *AssignmentHandler) Capabilities(c *gin.Context) {
	httpx.OK(c, catalogueView())
}

// Managed answers GET /v1/my/managed-rooms: the rooms handed to the caller. With view=groups every row is one landlord
// (their rooms come from a call with landlord_id).
func (h *AssignmentHandler) Managed(c *gin.Context) {
	manager, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	view, f := httpx.QueryOneOf(c, "view", "groups", "rooms")
	if f != nil {
		httpx.Fail(c, apperr.Invalid(*f))
		return
	}
	landlordID, f := httpx.QueryUUID(c, "landlord_id")
	if f != nil {
		httpx.Fail(c, apperr.Invalid(*f))
		return
	}
	ctx := c.Request.Context()
	if view != nil && *view == "groups" {
		groups, total, err := h.managed.Groups(ctx, manager, page.PerPage, page.Offset())
		if err != nil {
			httpx.Fail(c, mapManagerError(err))
			return
		}
		out := make([]ManagedGroupView, len(groups))
		for i, g := range groups {
			out[i] = ManagedGroupView{LandlordID: g.LandlordID, LandlordName: g.LandlordName, Count: g.RoomCount, RoomIDs: g.RoomIDs}
		}
		httpx.List(c, out, httpx.TotalMeta(page, total))
		return
	}
	items, total, err := h.managed.Rooms(ctx, manager, landlordID, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapManagerError(err))
		return
	}
	httpx.List(c, assignmentViewsOf(items, false), httpx.TotalMeta(page, total))
}
