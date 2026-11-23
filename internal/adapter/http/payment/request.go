package paymenthttp

import (
	"strconv"
	"strings"
	"time"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/payment"
)

// The bodies of the payment requests. The validate tags are the rules of the API; errors are reported under the json
// names.

// submitRequest is a payment sent as JSON (no proof pictures). The amount is amount_vnd; amount is accepted as the same
// thing, for clients written against the Laravel API.
type submitRequest struct {
	PaymentMethod string  `json:"payment_method" validate:"omitempty,max=30"`
	AmountVND     *int64  `json:"amount_vnd"`
	Amount        *int64  `json:"amount"`
	Note          *string `json:"note" validate:"omitempty,max=500"`
}

func (r submitRequest) amount() (int64, error) {
	switch {
	case r.AmountVND != nil:
		return *r.AmountVND, nil
	case r.Amount != nil:
		return *r.Amount, nil
	}
	return 0, apperr.Invalid(apperr.Field{Name: domain.FieldAmount, Rule: apperr.RuleRequired})
}

// parseAmount reads the amount of a multipart payment: a whole number of VND.
func parseAmount(values map[string]string) (int64, error) {
	raw, ok := values["amount_vnd"]
	if !ok {
		raw, ok = values["amount"]
	}
	raw = strings.TrimSpace(raw)
	if !ok || raw == "" {
		return 0, apperr.Invalid(apperr.Field{Name: domain.FieldAmount, Rule: apperr.RuleRequired})
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, apperr.Invalid(apperr.Field{Name: domain.FieldAmount, Rule: apperr.RuleInteger})
	}
	return n, nil
}

type rejectRequest struct {
	Reason string `json:"reason" validate:"required,max=500"`
}

type recordRequest struct {
	AmountVND *int64  `json:"amount_vnd"`
	Amount    *int64  `json:"amount"`
	Note      *string `json:"note" validate:"omitempty,max=500"`
	PaidOn    *string `json:"paid_on"`
}

func (r recordRequest) amount() (int64, error) {
	return submitRequest{AmountVND: r.AmountVND, Amount: r.Amount}.amount()
}

// paidOn reads the day the money came in (nil: now).
func (r recordRequest) paidOn() (*time.Time, error) {
	if r.PaidOn == nil || *r.PaidOn == "" {
		return nil, nil
	}
	d, err := time.Parse(time.DateOnly, *r.PaidOn)
	if err != nil {
		return nil, apperr.Invalid(apperr.Field{Name: "paid_on", Rule: apperr.RuleDate})
	}
	return &d, nil
}

type methodRequest struct {
	IsActive *bool             `json:"is_active"`
	Config   map[string]string `json:"config" validate:"required"`
}
