// Package notificationhttp is the HTTP side of the notification use cases: the inbox of the signed in person (list,
// the bell, unread count, mark read, delete). Every route is scoped to the caller: somebody else's message is a 404,
// and the role the caller acts in decides which side of the inbox they see.
package notificationhttp

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	usecase "rental-management/internal/usecase/notification"
)

// InboxHandler serves the inbox routes: it binds the request, calls the use case and writes the answer.
type InboxHandler struct {
	uc     *usecase.Inbox
	binder *httpx.Binder
}

// NewInboxHandler returns the handler.
func NewInboxHandler(uc *usecase.Inbox, binder *httpx.Binder) *InboxHandler {
	return &InboxHandler{uc: uc, binder: binder}
}

// List answers GET /v1/notifications (?unread, ?kind, ?page, ?per_page): a feed, newest first, that says whether more
// follow and how many are unread.
func (h *InboxHandler) List(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	unread, field := httpx.QueryBool(c, "unread")
	if field != nil {
		httpx.Fail(c, apperr.Invalid(*field))
		return
	}
	var kind *domain.Kind
	if raw := c.Query("kind"); raw != "" {
		k, err := domain.ParseKind(raw)
		if err != nil {
			httpx.Fail(c, mapError(err))
			return
		}
		kind = &k
	}
	result, err := h.uc.List(c.Request.Context(), viewer, unread != nil && *unread, kind, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": viewsOf(result.Items),
		"meta": feedMeta{Meta: httpx.FeedMeta(page, result.HasMore), UnreadCount: result.UnreadCount},
	})
}

// Latest answers GET /v1/notifications/latest (?limit, 1 to 20, default 8): the newest messages for the bell. It is not
// paged and only counts what is unread.
func (h *InboxHandler) Latest(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "limit", Rule: apperr.RuleInteger}))
			return
		}
		limit = n
	}
	result, err := h.uc.Latest(c.Request.Context(), viewer, limit)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": viewsOf(result.Items), "meta": latestMeta{UnreadCount: result.UnreadCount}})
}

// UnreadCount answers GET /v1/notifications/unread-count.
func (h *InboxHandler) UnreadCount(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	n, err := h.uc.UnreadCount(c.Request.Context(), viewer)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"unread_count": n})
}

// ReadAll answers POST /v1/notifications/read-all: it marks the inbox of the role the caller acts in as read.
func (h *InboxHandler) ReadAll(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	n, err := h.uc.MarkAllRead(c.Request.Context(), viewer)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"updated": n})
}

// Bulk answers POST /v1/notifications/bulk {action: read|delete, ids}: ids that are not the caller's are ignored.
func (h *InboxHandler) Bulk(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	var req bulkRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	action, ids, err := req.parse()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	n, err := h.uc.Bulk(c.Request.Context(), viewer, action, ids)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"done": n})
}

// Read answers POST /v1/notifications/{id}/read.
func (h *InboxHandler) Read(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	n, err := h.uc.MarkRead(c.Request.Context(), viewer, id)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, viewOf(n))
}

// Delete answers DELETE /v1/notifications/{id}.
func (h *InboxHandler) Delete(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.uc.Delete(c.Request.Context(), viewer, id); err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.NoContent(c)
}

// Clear answers DELETE /v1/notifications?scope=read|all: it clears the caller's inbox of every role.
func (h *InboxHandler) Clear(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	scope, valid := domain.ParseDeleteScope(c.Query("scope"))
	if !valid {
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "scope", Rule: apperr.RuleOneOf}))
		return
	}
	n, err := h.uc.DeleteMany(c.Request.Context(), viewer, scope)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"deleted": n})
}
