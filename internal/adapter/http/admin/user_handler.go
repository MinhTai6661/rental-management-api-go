package adminhttp

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// UserHandler serves the admin's management of accounts and roles.
type UserHandler struct {
	uc     *usecase.Users
	binder *httpx.Binder
}

// NewUserHandler returns the handler.
func NewUserHandler(uc *usecase.Users, binder *httpx.Binder) *UserHandler {
	return &UserHandler{uc: uc, binder: binder}
}

// AccountView is an account as the admin sees it.
type AccountView struct {
	ID          string    `json:"id"`
	Email       *string   `json:"email"`
	FullName    string    `json:"full_name"`
	PhoneNumber *string   `json:"phone_number"`
	Status      string    `json:"status"`
	WardCode    *int32    `json:"ward_code"`
	Roles       []string  `json:"roles"`
	CreatedAt   time.Time `json:"created_at"`
}

func accountView(a domain.Account) AccountView {
	roles := a.Roles
	if roles == nil {
		roles = []string{}
	}
	return AccountView{
		ID: a.ID.String(), Email: a.Email, FullName: a.FullName, PhoneNumber: a.PhoneNumber, Status: a.Status,
		WardCode: a.WardCode, Roles: roles, CreatedAt: a.CreatedAt.UTC(),
	}
}

// List answers GET /v1/admin/users (?status, ?role_name, ?search, ?ward_code, ?province_code, ?sort_by, ?page, ?per_page).
func (h *UserHandler) List(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ward, f1 := httpx.QueryInt64(c, "ward_code", 1, 1<<31-1)
	province, f2 := httpx.QueryInt64(c, "province_code", 1, 1<<31-1)
	for _, f := range []*apperr.Field{f1, f2} {
		if f != nil {
			httpx.Fail(c, apperr.Invalid(*f))
			return
		}
	}
	q := usecase.UserQuery{
		Status: c.Query("status"), RoleName: c.Query("role_name"), Search: c.Query("search"), Sort: c.Query("sort_by"),
		WardCode: int32Of(ward), ProvinceCode: int32Of(province),
	}
	if q.WardCode != nil {
		q.ProvinceCode = nil // a ward says more than its province
	}
	items, total, err := h.uc.List(c.Request.Context(), actor, q, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	views := make([]AccountView, len(items))
	for i, a := range items {
		views[i] = accountView(a)
	}
	httpx.List(c, views, httpx.TotalMeta(page, total))
}

func int32Of(v *int64) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // bounded by QueryInt64
	return &n
}

// Roles answers GET /v1/admin/roles: the roles the caller may give (one page of them).
func (h *UserHandler) Roles(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, httpx.MaxPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	roles, total := h.uc.Roles(actor, page.PerPage, page.Offset())
	if roles == nil {
		roles = []string{}
	}
	httpx.List(c, roles, httpx.TotalMeta(page, total))
}

// Delete answers DELETE /v1/admin/users/{id}.
func (h *UserHandler) Delete(c *gin.Context) {
	actor, id, ok := actorAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), actor, id); err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.NoContent(c)
}

type bulkDeleteRequest struct {
	IDs []string `json:"ids" validate:"required,min=1,max=100,unique,dive,uuid"`
}

// BulkDelete answers POST /v1/admin/users/bulk-delete: it answers 200 even when some accounts were refused.
func (h *UserHandler) BulkDelete(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req bulkDeleteRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	ids, err := parseIDs(req.IDs)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.BulkDelete(c.Request.Context(), actor, ids)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, bulkViewOf(c, result))
}

func bulkViewOf(c *gin.Context, r domain.BulkResult) bulkView {
	view := bulkView{Done: make([]string, len(r.Done)), Failed: make([]failed, len(r.Failed))}
	for i, id := range r.Done {
		view.Done[i] = id.String()
	}
	for i, f := range r.Failed {
		view.Failed[i] = failed{ID: f.ID.String(), Name: f.Name, Message: messageOf(c, f.Reason)}
	}
	return view
}

// messageOf is the translated message of the API error a refusal maps to.
func messageOf(c *gin.Context, err error) string {
	if ae, ok := apperr.As(mapError(err)); ok {
		return httpx.Localize(c).T("errors."+ae.Code, ae.Params)
	}
	return err.Error()
}

type rolesRequest struct {
	Roles []string `json:"roles"`
	Note  string   `json:"note" validate:"omitempty,max=1000"`
}

// AssignRoles answers PUT /v1/admin/users/{id}/roles.
func (h *UserHandler) AssignRoles(c *gin.Context) {
	actor, id, ok := actorAndID(c)
	if !ok {
		return
	}
	var req rolesRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	acc, err := h.uc.AssignRoles(c.Request.Context(), actor, id, req.Roles, req.Note)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, accountView(acc))
}

// ImpactView is what taking the landlord role away touches.
type ImpactView struct {
	Rooms                int  `json:"rooms"`
	ActiveContracts      int  `json:"active_contracts"`
	PendingRegistrations int  `json:"pending_registrations"`
	UnpaidInvoices       int  `json:"unpaid_invoices"`
	CanRevoke            bool `json:"can_revoke"`
}

// Impact answers GET /v1/admin/users/{id}/landlord-impact.
func (h *UserHandler) Impact(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	impact, err := h.uc.Impact(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, ImpactView{
		Rooms: impact.Rooms, ActiveContracts: impact.ActiveContracts, PendingRegistrations: impact.PendingRegistrations,
		UnpaidInvoices: impact.UnpaidInvoices, CanRevoke: impact.CanRevoke(),
	})
}

type revokeRequest struct {
	Note string `json:"note" validate:"omitempty,max=1000"`
}

// RevokeLandlord answers POST /v1/admin/users/{id}/revoke-landlord.
func (h *UserHandler) RevokeLandlord(c *gin.Context) {
	actor, id, ok := actorAndID(c)
	if !ok {
		return
	}
	var req revokeRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.RevokeLandlord(c.Request.Context(), actor, id, req.Note); err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func actorAndID(c *gin.Context) (usecase.Actor, uuid.UUID, bool) {
	actor, ok := actorOf(c)
	if !ok {
		return usecase.Actor{}, uuid.Nil, false
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return usecase.Actor{}, uuid.Nil, false
	}
	return actor, id, true
}
