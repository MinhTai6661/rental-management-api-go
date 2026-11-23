// Package invoicehttp is the HTTP side of the invoice use cases: the landlord's bills of a contract (periods, preview,
// create, correct, send, settle, delete, share link), the tenant's view of the bills sent to them, the meter history
// of a room and the public page a share link opens.
package invoicehttp

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/invoice"
	usecase "rental-management/internal/usecase/invoice"
)

// Clock tells the time (the views tell whether a link still works).
type Clock interface{ Now() time.Time }

// InvoiceHandler serves the bill routes: it binds the request, calls the use case and writes the answer.
type InvoiceHandler struct {
	uc     *usecase.Invoices
	binder *httpx.Binder
	clock  Clock
	loc    *time.Location
}

// NewInvoiceHandler returns the handler. loc is the business time zone ("today" is the date there).
func NewInvoiceHandler(uc *usecase.Invoices, binder *httpx.Binder, clock Clock, loc *time.Location) *InvoiceHandler {
	if loc == nil {
		loc = time.FixedZone("ICT", 7*3600)
	}
	return &InvoiceHandler{uc: uc, binder: binder, clock: clock, loc: loc}
}

func (h *InvoiceHandler) today() time.Time { return domain.Today(h.clock.Now(), h.loc) }

func (h *InvoiceHandler) one(c *gin.Context, v domain.Viewer, status int, view usecase.View) {
	body := invoiceViewOf(view, v, h.clock.Now())
	if status == http.StatusCreated {
		httpx.Created(c, body)
		return
	}
	httpx.OK(c, body)
}

// List answers GET /v1/contracts/{id}/invoices: the bills of a contract the caller sees, newest first, a page at a time
// (?limit up to 50, ?offset).
func (h *InvoiceHandler) List(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	limit, offset, err := loadMore(c, usecase.MaxPageSize)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := h.uc.List(c.Request.Context(), viewer, id, limit, offset)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": invoiceViewsOf(page.Items, viewer, h.clock.Now()),
		"meta": loadMoreMeta{Limit: limit, Offset: offset, Total: page.Total, HasMore: page.HasMore},
	})
}

// Periods answers GET /v1/contracts/{id}/billing-periods: the started periods and the bill each has.
func (h *InvoiceHandler) Periods(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	rows, err := h.uc.Periods(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.OK(c, periodViewsOf(rows))
}

// Preview answers POST /v1/contracts/{id}/invoices/preview.
func (h *InvoiceHandler) Preview(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	var req previewRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	p, err := h.uc.Preview(c.Request.Context(), landlord, id, in)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.OK(c, previewViewOf(p))
}

// Create answers POST /v1/contracts/{id}/invoices: a draft, or with already_paid a bill created sent and paid.
func (h *InvoiceHandler) Create(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	var req createRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in, err := req.input(id, h.today())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	v, err := h.uc.Create(c.Request.Context(), landlord, in)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	h.one(c, domain.Viewer{UserID: landlord, AsLandlord: true}, http.StatusCreated, v)
}

// Get answers GET /v1/invoices/{id}.
func (h *InvoiceHandler) Get(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	v, err := h.uc.Get(c.Request.Context(), viewer, id)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	h.one(c, viewer, http.StatusOK, v)
}

// Update answers PUT /v1/invoices/{id}: a correction of an unpaid bill.
func (h *InvoiceHandler) Update(c *gin.Context) {
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
	v, err := h.uc.Update(c.Request.Context(), landlord, id, in)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	h.one(c, domain.Viewer{UserID: landlord, AsLandlord: true}, http.StatusOK, v)
}

// PreviewUpdate answers POST /v1/invoices/{id}/preview: what the corrected bill would look like.
func (h *InvoiceHandler) PreviewUpdate(c *gin.Context) {
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
	p, err := h.uc.PreviewUpdate(c.Request.Context(), landlord, id, in.BillInput)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.OK(c, previewViewOf(p))
}

// Delete answers DELETE /v1/invoices/{id}.
func (h *InvoiceHandler) Delete(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.NoContent(c)
}

// Send answers POST /v1/invoices/{id}/send: a draft goes to the person (or a sent bill goes again).
func (h *InvoiceHandler) Send(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	var req sendRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	v, err := h.uc.Send(c.Request.Context(), landlord, id, delivery(req.Delivery))
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	h.one(c, domain.Viewer{UserID: landlord, AsLandlord: true}, http.StatusOK, v)
}

// SetStatus answers PATCH /v1/invoices/{id}: a bill moves between unpaid (sent) and paid.
func (h *InvoiceHandler) SetStatus(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	var req statusRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	v, err := h.uc.SetStatus(c.Request.Context(), landlord, id, domain.Status(req.Status))
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	h.one(c, domain.Viewer{UserID: landlord, AsLandlord: true}, http.StatusOK, v)
}

func (h *InvoiceHandler) link(c *gin.Context, fresh bool) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	op := h.uc.ShareLink
	if fresh {
		op = h.uc.RegenerateShareLink
	}
	l, err := op(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.OK(c, LinkView{URL: l.URL, ExpiresAt: l.ExpiresAt})
}

// ShareLink answers POST /v1/invoices/{id}/share-link: the link that shows the bill without signing in, the same one
// every time until it runs out or is replaced.
func (h *InvoiceHandler) ShareLink(c *gin.Context) { h.link(c, false) }

// RegenerateShareLink answers POST /v1/invoices/{id}/share-link/regenerate: a new link, the old one stops working.
func (h *InvoiceHandler) RegenerateShareLink(c *gin.Context) { h.link(c, true) }

// RevokeShareLink answers DELETE /v1/invoices/{id}/share-link.
func (h *InvoiceHandler) RevokeShareLink(c *gin.Context) {
	landlord, id, ok := landlordAndID(c)
	if !ok {
		return
	}
	if err := h.uc.RevokeShareLink(c.Request.Context(), landlord, id); err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.NoContent(c)
}

// MeterHandler serves the meter history of a room.
type MeterHandler struct{ uc *usecase.Invoices }

// NewMeterHandler returns the handler.
func NewMeterHandler(uc *usecase.Invoices) *MeterHandler { return &MeterHandler{uc: uc} }

// List answers GET /v1/rooms/{id}/meter-readings (?charge_id, ?page, ?per_page): the readings of the room's meters,
// newest first.
func (h *MeterHandler) List(c *gin.Context) {
	landlord, roomID, ok := landlordAndID(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, 10)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	charge, field := httpx.QueryUUID(c, "charge_id")
	if field != nil {
		httpx.Fail(c, apperr.Invalid(*field))
		return
	}
	result, err := h.uc.MeterHistory(c.Request.Context(), landlord, roomID, charge, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.List(c, meterViewsOf(result.Items), httpx.TotalMeta(page, result.Total))
}

// PublicHandler serves the page a share link opens.
type PublicHandler struct{ uc *usecase.Public }

// NewPublicHandler returns the handler.
func NewPublicHandler(uc *usecase.Public) *PublicHandler { return &PublicHandler{uc: uc} }

// Show answers GET /v1/public/invoices/{token}. Nobody signs in, so the answer is never cached or indexed and carries
// no referrer; a wrong, expired or withdrawn link all answer the same 404.
func (h *PublicHandler) Show(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Referrer-Policy", "no-referrer")
	view, err := h.uc.View(c.Request.Context(), c.Param("token"))
	if err != nil {
		httpx.Fail(c, mapInvoiceError(err))
		return
	}
	httpx.OK(c, publicViewOf(view))
}
