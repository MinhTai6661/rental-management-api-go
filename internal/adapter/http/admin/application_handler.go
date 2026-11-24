package adminhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// ApplicationHandler serves the landlord applications: the tenant's own and the admin's review.
type ApplicationHandler struct {
	uc     *usecase.Applications
	binder *httpx.Binder
}

// NewApplicationHandler returns the handler.
func NewApplicationHandler(uc *usecase.Applications, binder *httpx.Binder) *ApplicationHandler {
	return &ApplicationHandler{uc: uc, binder: binder}
}

// ApplicantView is who applied.
type ApplicantView struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Email *string `json:"email"`
}

// ApplicationView is a landlord application.
type ApplicationView struct {
	ID            string        `json:"id"`
	Applicant     ApplicantView `json:"applicant"`
	BusinessName  string        `json:"business_name"`
	Phone         string        `json:"phone"`
	WardCode      *int32        `json:"ward_code"`
	AddressDetail *string       `json:"address_detail"`
	Note          *string       `json:"note"`
	Status        string        `json:"status"`
	ReviewedByID  *string       `json:"reviewed_by_id"`
	ReviewedAt    *time.Time    `json:"reviewed_at"`
	ReviewNote    *string       `json:"review_note"`
	CreatedAt     time.Time     `json:"created_at"`
}

func applicationView(a domain.Application) ApplicationView {
	v := ApplicationView{
		ID: a.ID.String(), Applicant: ApplicantView{ID: a.UserID.String(), Name: a.ApplicantName, Email: a.ApplicantEmail},
		BusinessName: a.BusinessName, Phone: a.Phone, WardCode: a.WardCode, AddressDetail: a.AddressDetail, Note: a.Note,
		Status: string(a.Status), ReviewedAt: a.ReviewedAt, ReviewNote: a.ReviewNote, CreatedAt: a.CreatedAt.UTC(),
	}
	if a.ReviewedByID != nil {
		s := a.ReviewedByID.String()
		v.ReviewedByID = &s
	}
	return v
}

type applicationRequest struct {
	BusinessName  string  `json:"business_name" validate:"required,max=150"`
	Phone         string  `json:"phone" validate:"required,max=20"`
	WardCode      *int32  `json:"ward_code"`
	AddressDetail *string `json:"address_detail" validate:"omitempty,max=500"`
	Note          *string `json:"note" validate:"omitempty,max=1000"`
}

// Submit answers POST /v1/my/landlord-application.
func (h *ApplicationHandler) Submit(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req applicationRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	app, err := h.uc.Submit(c.Request.Context(), actor, domain.ApplicationInput{
		BusinessName: req.BusinessName, Phone: req.Phone, WardCode: req.WardCode, AddressDetail: req.AddressDetail, Note: req.Note,
	})
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.Created(c, applicationView(app))
}

// Withdraw answers DELETE /v1/my/landlord-application.
func (h *ApplicationHandler) Withdraw(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	if err := h.uc.Withdraw(c.Request.Context(), actor); err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.NoContent(c)
}

// Mine answers GET /v1/my/landlord-application: the newest application of the caller, or null.
func (h *ApplicationHandler) Mine(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	app, err := h.uc.Mine(c.Request.Context(), actor)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	if app == nil {
		httpx.OK(c, nil)
		return
	}
	httpx.OK(c, applicationView(*app))
}

// listMeta is the meta of the list of applications: the page and how many there are in each status.
type listMeta struct {
	httpx.Meta
	Counts map[string]int `json:"counts"`
}

// List answers GET /v1/admin/landlord-applications (?status, ?search, ?sort_by, ?page, ?per_page).
func (h *ApplicationHandler) List(c *gin.Context) {
	page, err := httpx.ParsePage(c, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.uc.List(c.Request.Context(), usecase.ListInput{
		Status: c.Query("status"), Search: c.Query("search"), Sort: c.Query("sort_by"),
	}, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	views := make([]ApplicationView, len(res.Items))
	for i, a := range res.Items {
		views[i] = applicationView(a)
	}
	counts := map[string]int{string(domain.ApplicationPending): 0, string(domain.ApplicationApproved): 0, string(domain.ApplicationRejected): 0}
	for st, n := range res.Counts {
		counts[string(st)] = n
	}
	c.JSON(200, gin.H{"data": views, "meta": listMeta{Meta: httpx.TotalMeta(page, res.Total), Counts: counts}})
}

// DetailView is an application with the earlier ones of the same person.
type DetailView struct {
	ApplicationView
	History []ApplicationView `json:"history"`
}

// Get answers GET /v1/admin/landlord-applications/{id}.
func (h *ApplicationHandler) Get(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	view := DetailView{ApplicationView: applicationView(d.Application), History: make([]ApplicationView, len(d.History))}
	for i, a := range d.History {
		view.History[i] = applicationView(a)
	}
	httpx.OK(c, view)
}

type reviewRequest struct {
	Note string `json:"note" validate:"omitempty,max=1000"`
}

// Approve answers POST /v1/admin/landlord-applications/{id}/approve.
func (h *ApplicationHandler) Approve(c *gin.Context) { h.review(c, false) }

// Reject answers POST /v1/admin/landlord-applications/{id}/reject (the reason is required).
func (h *ApplicationHandler) Reject(c *gin.Context) { h.review(c, true) }

func (h *ApplicationHandler) review(c *gin.Context, reject bool) {
	actor, id, ok := actorAndID(c)
	if !ok {
		return
	}
	var req reviewRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	var err error
	if reject {
		err = h.uc.Reject(c.Request.Context(), actor, id, req.Note)
	} else {
		err = h.uc.Approve(c.Request.Context(), actor, id, req.Note)
	}
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.NoContent(c)
}

type applicationBulkRequest struct {
	Action string   `json:"action" validate:"required,oneof=approve reject"`
	IDs    []string `json:"ids" validate:"required,min=1,max=100,unique,dive,uuid"`
	Note   string   `json:"note" validate:"omitempty,max=1000"`
}

// Bulk answers POST /v1/admin/landlord-applications/bulk {action, ids, note}.
func (h *ApplicationHandler) Bulk(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req applicationBulkRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	ids, err := parseIDs(req.IDs)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.Bulk(c.Request.Context(), actor, req.Action == "reject", ids, req.Note)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, bulkViewOf(c, result))
}
