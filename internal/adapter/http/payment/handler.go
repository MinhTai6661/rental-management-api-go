// Package paymenthttp is the HTTP side of the payment use cases: how a tenant pays a bill (with proof pictures), how a
// landlord confirms, rejects or writes down payments and sets up the payment methods they accept, the payment
// instructions of a bill, and the signed links that open a proof picture.
package paymenthttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/payment"
	usecase "rental-management/internal/usecase/payment"
)

// Limits are the limits of the proof pictures of a payment.
type Limits struct {
	// MaxFiles is how many pictures a payment may carry and MaxBytes how large each may be.
	MaxFiles int
	MaxBytes int64
}

// maxNoteBytes is room for a note of 500 characters of any script.
const maxNoteBytes = 2048

// PaymentHandler serves the payment routes: it binds the request, calls the use case and writes the answer.
type PaymentHandler struct {
	uc     *usecase.Payments
	binder *httpx.Binder
	limits Limits
}

// NewPaymentHandler returns the handler.
func NewPaymentHandler(uc *usecase.Payments, binder *httpx.Binder, limits Limits) *PaymentHandler {
	return &PaymentHandler{uc: uc, binder: binder, limits: limits}
}

// OfInvoice answers GET /v1/invoices/{id}/payments (?page, ?per_page): the payments of a bill, oldest first, for the
// landlord of its room and the tenant it was sent to.
func (h *PaymentHandler) OfInvoice(c *gin.Context) {
	viewer, ok := viewerOf(c)
	if !ok {
		return
	}
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := httpx.ParsePage(c, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.OfInvoice(c.Request.Context(), viewer, id, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.List(c, paymentViewsOf(result.Items), httpx.TotalMeta(page, result.Total))
}

// List answers GET /v1/payments (?status, ?page, ?per_page): the payments on the bills of the landlord's rooms, newest
// first.
func (h *PaymentHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c, httpx.DefaultPerPage)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var status *domain.Status
	if raw := c.Query("status"); raw != "" {
		s, valid := domain.ParseStatus(raw)
		if !valid {
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "status", Rule: apperr.RuleOneOf}))
			return
		}
		status = &s
	}
	result, err := h.uc.ForLandlord(c.Request.Context(), landlord, status, page.PerPage, page.Offset())
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.List(c, paymentViewsOf(result.Items), httpx.TotalMeta(page, result.Total))
}

// Submit answers POST /v1/invoices/{id}/payments: the tenant of a sent bill pays all or part of it. The body is
// multipart (amount_vnd, note, payment_method and up to PAYMENT_PROOF_MAX_FILES pictures in proofs[]) or, with no
// pictures, JSON.
func (h *PaymentHandler) Submit(c *gin.Context) {
	payer, invoiceID, ok := callerAndID(c)
	if !ok {
		return
	}
	in := usecase.SubmitInput{InvoiceID: invoiceID, PayerID: payer}
	var err error
	if strings.HasPrefix(c.ContentType(), "multipart/") {
		err = h.readMultipart(c, &in)
	} else {
		err = h.readJSON(c, &in)
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	view, err := h.uc.Submit(c.Request.Context(), in)
	if errors.Is(err, domain.ErrUnknownMethod) {
		// A method no driver has is a wrong value of the request here (and a missing resource on the settings routes).
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "payment_method", Rule: apperr.RuleOneOf}))
		return
	}
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.Created(c, paymentViewOf(view))
}

func (h *PaymentHandler) readJSON(c *gin.Context, in *usecase.SubmitInput) error {
	var req submitRequest
	if err := h.binder.JSON(c, &req); err != nil {
		return err
	}
	amount, err := req.amount()
	if err != nil {
		return err
	}
	in.Method, in.AmountVND, in.Note = req.PaymentMethod, amount, req.Note
	return nil
}

func (h *PaymentHandler) readMultipart(c *gin.Context, in *usecase.SubmitInput) error {
	up, err := httpx.ReadUploads(c, []string{"proofs", "proofs[]"}, h.limits.MaxFiles, h.limits.MaxBytes, maxNoteBytes)
	if err != nil {
		return err
	}
	if in.AmountVND, err = parseAmount(up.Values); err != nil {
		return err
	}
	in.Method = strings.TrimSpace(up.Values["payment_method"])
	if note, has := up.Values["note"]; has && strings.TrimSpace(note) != "" {
		in.Note = &note
	}
	for _, f := range up.Files {
		in.Proofs = append(in.Proofs, f.Data)
	}
	return nil
}

// Confirm answers POST /v1/payments/{id}/confirm: the landlord says the money arrived.
func (h *PaymentHandler) Confirm(c *gin.Context) {
	landlord, id, ok := callerAndID(c)
	if !ok {
		return
	}
	view, err := h.uc.Confirm(c.Request.Context(), landlord, id)
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.OK(c, paymentViewOf(view))
}

// Reject answers POST /v1/payments/{id}/reject {reason}: the landlord does not accept the payment.
func (h *PaymentHandler) Reject(c *gin.Context) {
	landlord, id, ok := callerAndID(c)
	if !ok {
		return
	}
	var req rejectRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	view, err := h.uc.Reject(c.Request.Context(), landlord, id, req.Reason)
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.OK(c, paymentViewOf(view))
}

// Withdraw answers DELETE /v1/payments/{id}: the tenant takes back a payment the landlord has not looked at yet.
func (h *PaymentHandler) Withdraw(c *gin.Context) {
	payer, id, ok := callerAndID(c)
	if !ok {
		return
	}
	if err := h.uc.Withdraw(c.Request.Context(), payer, id); err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.NoContent(c)
}

// Record answers POST /v1/invoices/{id}/record-payment {amount_vnd, note, paid_on}: the landlord writes down money that
// came in outside the app (a tenant with no account, cash).
func (h *PaymentHandler) Record(c *gin.Context) {
	landlord, id, ok := callerAndID(c)
	if !ok {
		return
	}
	var req recordRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	amount, err := req.amount()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	paid, err := req.paidOn()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	view, err := h.uc.Record(c.Request.Context(), usecase.RecordInput{InvoiceID: id, LandlordID: landlord, AmountVND: amount, Note: req.Note, PaidOn: paid})
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.Created(c, paymentViewOf(view))
}

// Instructions answers GET /v1/invoices/{id}/payment-instructions (?amount_vnd): how the tenant pays what is still due,
// one entry for each payment method the landlord accepts (empty when nothing is due). amount_vnd is what is about to be
// paid, for the QR code.
func (h *PaymentHandler) Instructions(c *gin.Context) {
	tenant, id, ok := callerAndID(c)
	if !ok {
		return
	}
	var amount *int64
	if raw := c.Query("amount_vnd"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			httpx.Fail(c, apperr.Invalid(apperr.Field{Name: domain.FieldAmount, Rule: apperr.RuleInteger}))
			return
		}
		amount = &n
	}
	list, err := h.uc.Instructions(c.Request.Context(), tenant, id, amount)
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.OK(c, instructionsViewsOf(list))
}

// ProofHandler serves the proof pictures behind their signed links.
type ProofHandler struct{ uc *usecase.Payments }

// NewProofHandler returns the handler.
func NewProofHandler(uc *usecase.Payments) *ProofHandler { return &ProofHandler{uc: uc} }

// Show answers GET /v1/payments/{id}/proofs/{file} (?expires, ?signature): one proof picture ("<index>.webp") or its
// small copy ("<index>_thumb.webp"). Nobody signs in: the signature of the link is the permission, and it expires. A
// wrong, tampered or expired link, and a file that is not there, all answer the same 404. The bytes are the picture as it
// was stored (WebP when the client sent WebP, otherwise JPEG), whatever the name says.
func (h *ProofHandler) Show(c *gin.Context) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, apperr.NotFound())
		return
	}
	expires, err := strconv.ParseInt(c.Query("expires"), 10, 64)
	if err != nil {
		httpx.Fail(c, apperr.NotFound())
		return
	}
	body, info, err := h.uc.OpenProof(c.Request.Context(), id, c.Param("file"), expires, c.Query("signature"))
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	defer func() { _ = body.Close() }()
	header := c.Writer.Header()
	header.Set("Cache-Control", "private, max-age=3600")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	c.DataFromReader(http.StatusOK, info.Size, info.ContentType, body, nil)
}

// MethodHandler serves the payment methods of the landlord.
type MethodHandler struct {
	uc     *usecase.Methods
	binder *httpx.Binder
}

// NewMethodHandler returns the handler.
func NewMethodHandler(uc *usecase.Methods, binder *httpx.Binder) *MethodHandler {
	return &MethodHandler{uc: uc, binder: binder}
}

// List answers GET /v1/my/payment-methods: every method the platform offers with what the landlord saved. The list is
// fixed and short (one entry per method), so it is not paged.
func (h *MethodHandler) List(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	list, err := h.uc.List(c.Request.Context(), landlord)
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	out := make([]MethodView, len(list))
	for i, m := range list {
		out[i] = methodViewOf(m)
	}
	httpx.OK(c, out)
}

// Save answers PUT /v1/my/payment-methods/{method} {is_active, config}: it sets up one method.
func (h *MethodHandler) Save(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	var req methodRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	saved, err := h.uc.Save(c.Request.Context(), landlord, c.Param("method"), domain.Config(req.Config), req.IsActive)
	if err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.OK(c, methodViewOf(saved))
}

// Remove answers DELETE /v1/my/payment-methods/{method}.
func (h *MethodHandler) Remove(c *gin.Context) {
	landlord, ok := userIDOf(c)
	if !ok {
		return
	}
	if err := h.uc.Remove(c.Request.Context(), landlord, c.Param("method")); err != nil {
		httpx.Fail(c, mapPaymentError(err))
		return
	}
	httpx.NoContent(c)
}
