// Package planhttp is the HTTP side of the plan use cases: the catalog a user reads, a user's own subscription, what
// an admin does with subscriptions and what a super admin does with the catalog.
package planhttp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/plan"
	usecase "rental-management/internal/usecase/plan"
)

// defaultPlansPerPage is the page size of the catalog lists: bounded data, so one page is normally all of it.
const defaultPlansPerPage = httpx.MaxPerPage

// PlanHandler serves the plan routes: it binds the request, calls the use case and writes the answer.
type PlanHandler struct {
	plans  *usecase.Plans
	subs   *usecase.Subscriptions
	binder *httpx.Binder
}

// NewPlanHandler returns the handler.
func NewPlanHandler(plans *usecase.Plans, subs *usecase.Subscriptions, binder *httpx.Binder) *PlanHandler {
	return &PlanHandler{plans: plans, subs: subs, binder: binder}
}

// Catalog answers GET /v1/plans: the plans on offer to the side the caller acts on (or to `audience`).
func (h *PlanHandler) Catalog(c *gin.Context) {
	page, err := httpx.ParsePage(c, defaultPlansPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	audience := domain.AudienceOfRole(roleOf(c))
	asked, field := httpx.QueryOneOf(c, "audience", string(domain.AudienceLandlord), string(domain.AudienceTenant))
	if field != nil {
		httpx.Fail(c, apperr.Invalid(*field))
		return
	}
	if asked != nil {
		audience, _ = domain.ParseAudience(*asked)
	}
	plans, total, err := h.plans.Available(c.Request.Context(), audience, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.List(c, planViewsOf(plans), httpx.TotalMeta(page, total))
}

// Mine answers GET /v1/my/subscription.
func (h *PlanHandler) Mine(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	h.writeMine(c, http.StatusOK, user)
}

func (h *PlanHandler) writeMine(c *gin.Context, status int, user uuid.UUID) {
	m, err := h.subs.Mine(c.Request.Context(), user, roleOf(c))
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	if status == http.StatusCreated {
		httpx.Created(c, mineViewOf(m))
		return
	}
	httpx.OK(c, mineViewOf(m))
}

// Subscribe answers POST /v1/my/subscription: a free plan takes effect at once, a paid one waits for an admin.
func (h *PlanHandler) Subscribe(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	var req subscribeRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if _, err := h.subs.Subscribe(c.Request.Context(), user, roleOf(c), *req.PlanID); err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	h.writeMine(c, http.StatusCreated, user)
}

// CancelMine answers DELETE /v1/my/subscription: the user goes back to the default plan.
func (h *PlanHandler) CancelMine(c *gin.Context) {
	user, ok := userIDOf(c)
	if !ok {
		return
	}
	if err := h.subs.Cancel(c.Request.Context(), user, roleOf(c)); err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.NoContent(c)
}

// ListSubscriptions answers GET /admin/subscriptions: the requests waiting for activation, or any status.
func (h *PlanHandler) ListSubscriptions(c *gin.Context) {
	page, err := httpx.ParsePage(c, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var fields []apperr.Field
	keep := func(f *apperr.Field) {
		if f != nil {
			fields = append(fields, *f)
		}
	}
	q := domain.SubscriptionQuery{Limit: page.PerPage, Offset: page.Offset()}
	status, f := httpx.QueryOneOf(c, "status", "pending", "active", "canceled", "expired")
	keep(f)
	pending := domain.StatusPending
	q.Status = &pending
	if status != nil {
		s, _ := domain.ParseStatus(*status)
		q.Status = &s
	}
	audience, f := httpx.QueryOneOf(c, "audience", "landlord", "tenant")
	keep(f)
	if audience != nil {
		a, _ := domain.ParseAudience(*audience)
		q.Audience = &a
	}
	user, f := httpx.QueryUUID(c, "user_id")
	keep(f)
	q.UserID = user
	if len(fields) > 0 {
		httpx.Fail(c, apperr.Invalid(fields...))
		return
	}
	items, total, err := h.subs.Search(c.Request.Context(), q)
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.List(c, subscriptionViewsOf(items), httpx.TotalMeta(page, total))
}

// Activate answers POST /admin/subscriptions/{id}/activate: the payment arrived, the request becomes the running plan.
func (h *PlanHandler) Activate(c *gin.Context) {
	actor, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.subs.Activate(c.Request.Context(), actor, id)
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.OK(c, subscriptionViewOf(d, true))
}

// Assign answers PUT /admin/users/{id}/subscription: the user is put on the plan right now.
func (h *PlanHandler) Assign(c *gin.Context) {
	actor, ok := userIDOf(c)
	if !ok {
		return
	}
	target, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req assignRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	end, err := req.endsAt()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	d, err := h.subs.Assign(c.Request.Context(), actor, target, *req.PlanID, end)
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.Created(c, subscriptionViewOf(d, true))
}

// Revoke answers DELETE /admin/users/{id}/subscription: every plan and request of the user ends.
func (h *PlanHandler) Revoke(c *gin.Context) {
	actor, ok := userIDOf(c)
	if !ok {
		return
	}
	target, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.subs.Revoke(c.Request.Context(), actor, target); err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.NoContent(c)
}

// ListPlans answers GET /admin/plans: the whole catalog, inactive plans included.
func (h *PlanHandler) ListPlans(c *gin.Context) {
	page, err := httpx.ParsePage(c, defaultPlansPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	plans, total, err := h.plans.List(c.Request.Context(), page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.List(c, planViewsOf(plans), httpx.TotalMeta(page, total))
}

// CreatePlan answers POST /admin/plans.
func (h *PlanHandler) CreatePlan(c *gin.Context) {
	actor, ok := userIDOf(c)
	if !ok {
		return
	}
	var req createPlanRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, err := h.plans.Create(c.Request.Context(), actor, req.input())
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.Created(c, planViewOf(p))
}

// UpdatePlan answers PUT and PATCH /admin/plans/{id}: a partial update; plans are never deleted, only switched off.
func (h *PlanHandler) UpdatePlan(c *gin.Context) {
	actor, ok := userIDOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var req updatePlanRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	patch, err := req.patch()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	p, err := h.plans.Update(c.Request.Context(), actor, id, patch)
	if err != nil {
		httpx.Fail(c, mapPlanError(err))
		return
	}
	httpx.OK(c, planViewOf(p))
}
