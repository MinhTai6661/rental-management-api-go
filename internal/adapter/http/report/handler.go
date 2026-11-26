package reporthttp

import (
	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/httpx"
	usecase "rental-management/internal/usecase/report"
)

const (
	defaultReportsPerPage = 10
	defaultRepliesPerPage = 20
	maxRepliesPerPage     = 50
)

// ReportHandler serves the reports: the author sends and follows them, the person a report goes to answers it.
type ReportHandler struct {
	uc     *usecase.Reports
	binder *httpx.Binder
}

// NewReportHandler returns the handler.
func NewReportHandler(uc *usecase.Reports, binder *httpx.Binder) *ReportHandler {
	return &ReportHandler{uc: uc, binder: binder}
}

// Mine answers GET /v1/reports: the reports the caller wrote, newest first.
func (h *ReportHandler) Mine(c *gin.Context) {
	h.list(c, h.uc.Mine)
}

// Inbox answers GET /v1/reports/inbox: the reports the caller has to answer (room reports of a landlord's rooms, feature
// reports for an admin).
func (h *ReportHandler) Inbox(c *gin.Context) {
	h.list(c, h.uc.Inbox)
}

func (h *ReportHandler) list(c *gin.Context, load listFunc) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, defaultReportsPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	filter, err := filterFromQuery(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	views, total, err := load(c.Request.Context(), viewer, filter, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.List(c, reportViewsOf(views), httpx.TotalMeta(page, total))
}

// Create answers POST /v1/reports.
func (h *ReportHandler) Create(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	in, err := createInputFromJSON(c, h.binder)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	created, err := h.uc.Create(c.Request.Context(), viewer, in)
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.Created(c, reportViewOf(created))
}

// Show answers GET /v1/reports/{id}: the author or the recipient (a report the caller may not see is a 404).
func (h *ReportHandler) Show(c *gin.Context) {
	viewer, id, ok := viewerAndID(c)
	if !ok {
		return
	}
	found, err := h.uc.Get(c.Request.Context(), viewer, id)
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.OK(c, reportViewOf(found))
}

// Replies answers GET /v1/reports/{id}/replies: the conversation, newest message first, a page at a time.
func (h *ReportHandler) Replies(c *gin.Context) {
	viewer, id, ok := viewerAndID(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, defaultRepliesPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page.PerPage = min(page.PerPage, maxRepliesPerPage)
	conversation, err := h.uc.Replies(c.Request.Context(), viewer, id, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.List(c, replyViewsOf(conversation.Report, conversation.Replies), httpx.TotalMeta(page, conversation.Total))
}

// Reply answers POST /v1/reports/{id}/replies: a message from the author or the recipient.
func (h *ReportHandler) Reply(c *gin.Context) {
	viewer, id, ok := viewerAndID(c)
	if !ok {
		return
	}
	body, err := replyBodyFromJSON(c, h.binder)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	replied, err := h.uc.Reply(c.Request.Context(), viewer, id, body)
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.Created(c, reportViewOf(replied.View))
}

// SetStatus answers PUT /v1/reports/{id}/status: only the recipient sets where the report stands.
func (h *ReportHandler) SetStatus(c *gin.Context) {
	viewer, id, ok := viewerAndID(c)
	if !ok {
		return
	}
	status, err := statusFromJSON(c, h.binder)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	updated, err := h.uc.SetStatus(c.Request.Context(), viewer, id, status)
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.OK(c, reportViewOf(updated))
}

// Delete answers DELETE /v1/reports/{id}: a tenant deletes a report they wrote (the recipient keeps it, read only).
func (h *ReportHandler) Delete(c *gin.Context) {
	viewer, id, ok := viewerAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), viewer, id); err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	httpx.NoContent(c)
}
