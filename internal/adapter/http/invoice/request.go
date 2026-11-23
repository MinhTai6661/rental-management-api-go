package invoicehttp

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/invoice"
	usecase "rental-management/internal/usecase/invoice"
)

// The bodies of the invoice requests. The validate tags are the rules of the API; errors are reported under the json
// names.

type readingRequest struct {
	ChargeID      uuid.UUID `json:"charge_id" validate:"required"`
	ReadingValue  *float64  `json:"reading_value" validate:"required,min=0,max=1000000000"`
	PreviousValue *float64  `json:"previous_value" validate:"omitempty,min=0,max=1000000000"`
	UnitPriceVND  *int64    `json:"unit_price_vnd" validate:"omitempty,min=0,max=1000000000000"`
}

type extraRequest struct {
	Description string `json:"description" validate:"required,max=150"`
	AmountVND   *int64 `json:"amount_vnd" validate:"required,min=0,max=1000000000000"`
}

// The bill fields (rent, readings, extra lines) are the same in a preview, a new bill and a correction; they are
// repeated in each request rather than embedded so that the errors name the field by its plain path ("readings.0.charge_id").

type previewRequest struct {
	PeriodStart      *string          `json:"period_start"`
	RentUnitPriceVND *int64           `json:"rent_unit_price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	Readings         []readingRequest `json:"readings" validate:"omitempty,max=20,unique=ChargeID,dive"`
	ExtraItems       []extraRequest   `json:"extra_items" validate:"omitempty,max=10,dive"`
}

type createRequest struct {
	PeriodStart  *string `json:"period_start"`
	DueOn        *string `json:"due_on"`
	Note         *string `json:"note" validate:"omitempty,max=500"`
	Name         *string `json:"name" validate:"omitempty,max=80"`
	PaymentLabel *string `json:"payment_label" validate:"omitempty,max=14"`
	Delivery     *string `json:"delivery" validate:"omitempty,oneof=app link"`
	AlreadyPaid  bool    `json:"already_paid"`

	RentUnitPriceVND *int64           `json:"rent_unit_price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	Readings         []readingRequest `json:"readings" validate:"omitempty,max=20,unique=ChargeID,dive"`
	ExtraItems       []extraRequest   `json:"extra_items" validate:"omitempty,max=10,dive"`
}

type updateRequest struct {
	DueOn        *string          `json:"due_on"`
	Note         optional[string] `json:"note"`
	Name         *string          `json:"name" validate:"omitempty,max=80"`
	PaymentLabel *string          `json:"payment_label" validate:"omitempty,max=14"`
	ResendEmail  bool             `json:"resend_email"`

	RentUnitPriceVND *int64           `json:"rent_unit_price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	Readings         []readingRequest `json:"readings" validate:"omitempty,max=20,unique=ChargeID,dive"`
	ExtraItems       []extraRequest   `json:"extra_items" validate:"omitempty,max=10,dive"`
}

type sendRequest struct {
	Delivery *string `json:"delivery" validate:"omitempty,oneof=app link"`
}

type statusRequest struct {
	Status string `json:"status" validate:"required,oneof=sent paid"`
}

// optional is a field of a partial update that tells apart "absent" (leave it) from null (clear it).
type optional[T any] struct {
	set   bool
	value *T
}

// UnmarshalJSON records that the field was present, and its value (nil for null).
func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.value = &v
	return nil
}

// fieldCheck collects the failed rules of a request that cannot use validate tags.
type fieldCheck struct{ fields []apperr.Field }

func (c *fieldCheck) add(name, rule string, params map[string]string) {
	c.fields = append(c.fields, apperr.Field{Name: name, Rule: rule, Params: params})
}

func (c *fieldCheck) err() error {
	if len(c.fields) == 0 {
		return nil
	}
	return apperr.Invalid(c.fields...)
}

// date reads a calendar date (nil stays nil); a bad one is reported on the field.
func (c *fieldCheck) date(name string, raw *string) *time.Time {
	if raw == nil || *raw == "" {
		return nil
	}
	d, err := domain.ParseDate(*raw)
	if err != nil {
		c.add(name, apperr.RuleDate, nil)
		return nil
	}
	return &d
}

var labelChars = regexp.MustCompile(`^[A-Za-z0-9 ._/-]+$`)

// label checks the words of a transfer text: plain characters only (the length is a validate tag).
func (c *fieldCheck) label(name string, v *string) {
	if v != nil && *v != "" && !labelChars.MatchString(*v) {
		c.add(name, apperr.RulePaymentLabelChars, nil)
	}
}

// delivery reads the delivery a request asked for.
func delivery(raw *string) *domain.Delivery {
	if raw == nil || *raw == "" {
		return nil
	}
	d, ok := domain.ParseDelivery(*raw)
	if !ok {
		return nil
	}
	return &d
}

// billOf turns the bill fields of a request into the use case's input.
func billOf(rent *int64, readings []readingRequest, extras []extraRequest) usecase.BillInput {
	in := usecase.BillInput{RentUnitPriceVND: rent}
	for _, r := range readings {
		in.Readings = append(in.Readings, domain.ReadingInput{
			ChargeID: r.ChargeID, Value: *r.ReadingValue, Previous: r.PreviousValue, UnitPriceVND: r.UnitPriceVND,
		})
	}
	for _, e := range extras {
		in.Extras = append(in.Extras, domain.Extra{Description: e.Description, AmountVND: *e.AmountVND})
	}
	return in
}

func (r previewRequest) input() (usecase.PreviewInput, error) {
	var c fieldCheck
	start := c.date("period_start", r.PeriodStart)
	if err := c.err(); err != nil {
		return usecase.PreviewInput{}, err
	}
	return usecase.PreviewInput{PeriodStart: start, BillInput: billOf(r.RentUnitPriceVND, r.Readings, r.ExtraItems)}, nil
}

// dueOn reads the day a bill falls due: today or later.
func (c *fieldCheck) dueOn(raw *string, today time.Time) *time.Time {
	d := c.date("due_on", raw)
	if d != nil && d.Before(today) {
		c.add("due_on", apperr.RuleDateNotPast, nil)
		return nil
	}
	return d
}

func (r createRequest) input(contractID uuid.UUID, today time.Time) (usecase.CreateInput, error) {
	var c fieldCheck
	start := c.date("period_start", r.PeriodStart)
	due := c.dueOn(r.DueOn, today)
	c.label("payment_label", r.PaymentLabel)
	if err := c.err(); err != nil {
		return usecase.CreateInput{}, err
	}
	return usecase.CreateInput{
		ContractID: contractID, PeriodStart: start, BillInput: billOf(r.RentUnitPriceVND, r.Readings, r.ExtraItems), DueOn: due, Note: r.Note, Name: r.Name,
		PaymentLabel: r.PaymentLabel, Delivery: delivery(r.Delivery), AlreadyPaid: r.AlreadyPaid,
	}, nil
}

func (r updateRequest) input() (usecase.UpdateInput, error) {
	var c fieldCheck
	due := c.date("due_on", r.DueOn)
	c.label("payment_label", r.PaymentLabel)
	if r.Note.value != nil && len([]rune(*r.Note.value)) > domain.MaxNoteLength {
		c.add("note", apperr.RuleMaxString, map[string]string{"max": itoa(domain.MaxNoteLength)})
	}
	if err := c.err(); err != nil {
		return usecase.UpdateInput{}, err
	}
	in := usecase.UpdateInput{BillInput: billOf(r.RentUnitPriceVND, r.Readings, r.ExtraItems), DueOn: due, Name: r.Name, PaymentLabel: r.PaymentLabel, ResendEmail: r.ResendEmail}
	if r.Note.set {
		in.Note = usecase.Some(r.Note.value)
	}
	return in, nil
}
