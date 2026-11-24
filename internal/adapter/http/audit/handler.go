// Package audithttp is the HTTP side of the audit trail: the staff's view of the whole trail, the activity of the
// signed in person, and the history of a room, a contract or an invoice for its landlord. Every list is a feed that
// says whether another page follows (the trail is never counted).
package audithttp

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/audit"
	usecase "rental-management/internal/usecase/audit"
)

// Page sizes: the staff read long pages, a person's own feed is shorter.
const (
	adminPerPage = 50
	minePerPage  = 20
)

// Handler serves the audit routes.
type Handler struct{ reader *usecase.Reader }

// NewHandler returns the handler.
func NewHandler(reader *usecase.Reader) *Handler { return &Handler{reader: reader} }

// List answers GET /v1/admin/audit-logs: the whole trail, newest first, filtered by ?actor_id, ?actor_role, ?action,
// ?target_user_id, ?room_id, ?contract_id, ?invoice_id, ?from, ?to (days, YYYY-MM-DD) and ?tier.
func (h *Handler) List(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, adminPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	filter, err := filterOf(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	feed, err := h.reader.Admin(c.Request.Context(), viewer, filter, usecase.Page{Page: page.Page, PerPage: page.PerPage})
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.List(c, entriesOf(feed.Items), httpx.FeedMeta(page, feed.HasMore))
}

// Mine answers GET /v1/my/activity: what the caller did and what was done to their account.
func (h *Handler) Mine(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, minePerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	items, more, err := h.reader.Mine(c.Request.Context(), viewer.UserID, usecase.Page{Page: page.Page, PerPage: page.PerPage})
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	out := make([]entryView, len(items))
	for i, it := range items {
		out[i] = entryOf(it.Record)
		n := it.Count
		out[i].Count = &n
	}
	httpx.List(c, out, httpx.FeedMeta(page, more))
}

// History answers GET /v1/{rooms,contracts,invoices}/:id/history for the landlord who owns the thing.
func (h *Handler) History(subject domain.Subject) gin.HandlerFunc {
	return func(c *gin.Context) {
		viewer, ok := viewerOf(c)
		if !ok {
			return
		}
		id, err := httpx.ParseUUIDParam(c, "id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		page, err := httpx.ParsePage(c, minePerPage)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		feed, err := h.reader.History(c.Request.Context(), viewer, subject, id, usecase.Page{Page: page.Page, PerPage: page.PerPage})
		if err != nil {
			httpx.Fail(c, mapError(err))
			return
		}
		httpx.List(c, entriesOf(feed.Items), httpx.FeedMeta(page, feed.HasMore))
	}
}

// viewerOf is the caller of the request and the role they act in (RequireAuth ran before).
func viewerOf(c *gin.Context) (usecase.Viewer, bool) {
	if p, ok := httpx.PrincipalOf(c); ok {
		if id, err := uuid.Parse(p.UserID); err == nil {
			return usecase.Viewer{UserID: id, Role: p.Role}, true
		}
	}
	httpx.Fail(c, apperr.Unauthenticated())
	return usecase.Viewer{}, false
}

// filterOf reads the filters of the staff's list; every bad value is reported at once.
func filterOf(c *gin.Context) (domain.Filter, error) {
	var (
		f      domain.Filter
		fields []apperr.Field
	)
	for _, q := range []struct {
		name string
		dst  **uuid.UUID
	}{{"actor_id", &f.ActorID}, {"target_user_id", &f.TargetUserID}, {"room_id", &f.RoomID}, {"contract_id", &f.ContractID}, {"invoice_id", &f.InvoiceID}} {
		id, field := httpx.QueryUUID(c, q.name)
		if field != nil {
			fields = append(fields, *field)
			continue
		}
		*q.dst = id
	}
	if role := c.Query("actor_role"); len(role) > domain.MaxActorRoleLen {
		fields = append(fields, apperr.Field{Name: "actor_role", Rule: apperr.RuleMaxString, Params: map[string]string{"max": itoa(domain.MaxActorRoleLen)}})
	} else {
		f.ActorRole = role
	}
	if raw := c.Query("action"); raw != "" {
		if a, ok := domain.ParseAction(raw); ok {
			f.Action = &a
		} else {
			fields = append(fields, apperr.Field{Name: "action", Rule: apperr.RuleOneOf})
		}
	}
	if tier, ok := domain.ParseTier(c.Query("tier")); ok {
		f.Tier = tier
	} else {
		fields = append(fields, apperr.Field{Name: "tier", Rule: apperr.RuleOneOf})
	}
	for _, q := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		raw := c.Query(q.name)
		if raw == "" {
			continue
		}
		day, err := time.ParseInLocation(time.DateOnly, raw, domain.Zone)
		if err != nil {
			fields = append(fields, apperr.Field{Name: q.name, Rule: apperr.RuleDate})
			continue
		}
		*q.dst = &day
	}
	if len(fields) > 0 {
		return domain.Filter{}, apperr.Invalid(fields...)
	}
	return f, nil
}
