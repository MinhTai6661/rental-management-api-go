package adminhttp

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// LegalHandler serves the terms of use and the privacy policy: the public reading, the super admin's publishing and
// the signed in person's acceptance.
type LegalHandler struct {
	uc     *usecase.Legal
	binder *httpx.Binder
}

// NewLegalHandler returns the handler.
func NewLegalHandler(uc *usecase.Legal, binder *httpx.Binder) *LegalHandler {
	return &LegalHandler{uc: uc, binder: binder}
}

// TextView is a document in one language.
type TextView struct {
	Title string          `json:"title"`
	Body  json.RawMessage `json:"body"`
}

// SummaryView says what changed in both languages.
type SummaryView struct {
	VI string `json:"vi"`
	EN string `json:"en"`
}

// DocumentView is a published version of a legal document.
type DocumentView struct {
	ID            string              `json:"id"`
	Kind          string              `json:"kind"`
	Version       string              `json:"version"`
	PublishedAt   time.Time           `json:"published_at"`
	PublishedBy   *string             `json:"published_by,omitempty"`
	ChangeSummary SummaryView         `json:"change_summary"`
	Content       map[string]TextView `json:"content"`
}

func documentView(d domain.Document, withAuthor bool) DocumentView {
	v := DocumentView{
		ID: d.ID.String(), Kind: string(d.Kind), Version: d.Version, PublishedAt: d.PublishedAt.UTC(),
		ChangeSummary: SummaryView{VI: d.Summary.VI, EN: d.Summary.EN},
		Content: map[string]TextView{
			"vi": {Title: d.Content.VI.Title, Body: d.Content.VI.Body}, "en": {Title: d.Content.EN.Title, Body: d.Content.EN.Body},
		},
	}
	if withAuthor && d.PublishedByName != "" {
		v.PublishedBy = &d.PublishedByName
	}
	return v
}

func kindOf(c *gin.Context) (domain.LegalKind, bool) {
	kind, err := domain.ParseLegalKind(c.Param("kind"))
	if err != nil {
		httpx.Fail(c, mapError(err))
		return "", false
	}
	return kind, true
}

// Show answers GET /v1/legal/{kind}: the newest version, or null when none was published. Anybody may read it.
func (h *LegalHandler) Show(c *gin.Context) {
	kind, ok := kindOf(c)
	if !ok {
		return
	}
	doc, err := h.uc.Current(c.Request.Context(), kind)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	if doc == nil {
		httpx.OK(c, nil)
		return
	}
	httpx.OK(c, documentView(*doc, false))
}

// CurrentView is what the super admin sees of a document: the version in force and its content.
type CurrentView struct {
	CurrentVersion string        `json:"current_version"`
	Document       *DocumentView `json:"document"`
}

// Current answers GET /v1/admin/legal/{kind}.
func (h *LegalHandler) Current(c *gin.Context) {
	kind, ok := kindOf(c)
	if !ok {
		return
	}
	doc, err := h.uc.Current(c.Request.Context(), kind)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	version, err := h.uc.CurrentVersion(c.Request.Context())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	view := CurrentView{CurrentVersion: version}
	if doc != nil {
		d := documentView(*doc, true)
		view.Document = &d
	}
	httpx.OK(c, view)
}

type textRequest struct {
	Title string          `json:"title" validate:"required,max=200"`
	Body  json.RawMessage `json:"body"`
}

type summaryRequest struct {
	VI string `json:"vi" validate:"required,max=2000"`
	EN string `json:"en" validate:"required,max=2000"`
}

type publishRequest struct {
	Content struct {
		VI textRequest `json:"vi" validate:"required"`
		EN textRequest `json:"en" validate:"required"`
	} `json:"content" validate:"required"`
	ChangeSummary summaryRequest `json:"change_summary" validate:"required"`
}

// Publish answers POST /v1/admin/legal/{kind}: it adds a new version for everybody to accept.
func (h *LegalHandler) Publish(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	kind, ok := kindOf(c)
	if !ok {
		return
	}
	var req publishRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	version, err := h.uc.Publish(c.Request.Context(), actor, kind, domain.LegalInput{
		Content: domain.LegalContent{
			VI: domain.LegalText{Title: req.Content.VI.Title, Body: req.Content.VI.Body},
			EN: domain.LegalText{Title: req.Content.EN.Title, Body: req.Content.EN.Body},
		},
		Summary: domain.ChangeSummary{VI: req.ChangeSummary.VI, EN: req.ChangeSummary.EN},
	})
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.Created(c, gin.H{"version": version})
}

// Versions answers GET /v1/admin/legal/{kind}/versions (?page, ?per_page up to 50, default 10).
func (h *LegalHandler) Versions(c *gin.Context) {
	kind, ok := kindOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if page.PerPage > 50 {
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "per_page", Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": "50"}}))
		return
	}
	docs, total, err := h.uc.Versions(c.Request.Context(), kind, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	views := make([]DocumentView, len(docs))
	for i, d := range docs {
		views[i] = documentView(d, true)
	}
	httpx.List(c, views, httpx.TotalMeta(page, total))
}

// Document answers GET /v1/admin/legal/{kind}/{document_id}: 404 when the id belongs to the other document.
func (h *LegalHandler) Document(c *gin.Context) {
	kind, ok := kindOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "document_id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	doc, err := h.uc.Document(c.Request.Context(), kind, id)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, documentView(doc, true))
}

// Updates answers GET /v1/profile/legal-updates (?per_page up to 20, default 5): what changed since the caller last accepted.
func (h *LegalHandler) Updates(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	limit := 5
	if raw := c.Query("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "per_page", Rule: apperr.RuleInteger}))
			return
		case n < 1 || n > 20:
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "per_page", Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": "20"}}))
			return
		}
		limit = n
	}
	docs, err := h.uc.Updates(c.Request.Context(), actor.ID, limit)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	views := make([]DocumentView, len(docs))
	for i, d := range docs {
		views[i] = documentView(d, false)
	}
	httpx.OK(c, views)
}

// Accept answers POST /v1/profile/accept-terms: the caller accepts the version in force.
func (h *LegalHandler) Accept(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	version, err := h.uc.Accept(c.Request.Context(), actor)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, gin.H{"accepted_version": version})
}
