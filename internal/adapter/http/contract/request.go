package contracthttp

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// The bodies of the contract requests. The validate tags are the rules of the API; errors are reported under the
// json names.

const maxMoney = 1_000_000_000_000

type registerRequest struct {
	RoomID        *uuid.UUID `json:"room_id" validate:"required"`
	StartsOn      string     `json:"starts_on" validate:"required"`
	EndsOn        *string    `json:"ends_on"`
	OccupantCount *int       `json:"occupant_count" validate:"omitempty,min=1,max=50"`
	Notes         *string    `json:"notes" validate:"omitempty,max=1000"`
}

type bulkRequest struct {
	Action string      `json:"action" validate:"required,oneof=approve reject"`
	IDs    []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
}

type orderRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
}

type terminateRequest struct {
	Reason *string `json:"reason" validate:"omitempty,max=500"`
}

type cancellationRequest struct {
	Reason *string `json:"reason" validate:"omitempty,max=1000"`
}

type declineRequest struct {
	Note *string `json:"note" validate:"omitempty,max=1000"`
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

// updateRequest is a partial update: only the fields the body carries change. Its rules are checked by input(),
// because an optional field has no validate tag.
type updateRequest struct {
	Status        *string          `json:"status"`
	PriceVND      *int64           `json:"price_vnd"`
	DepositVND    optional[int64]  `json:"deposit_vnd"`
	OccupantCount *int             `json:"occupant_count"`
	StartsOn      *string          `json:"starts_on"`
	EndsOn        optional[string] `json:"ends_on"`
	Notes         optional[string] `json:"notes"`
	TenantName    optional[string] `json:"tenant_name"`
	TenantPhone   optional[string] `json:"tenant_phone"`
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
	if raw == nil {
		return nil
	}
	d, err := domain.ParseDate(*raw)
	if err != nil {
		c.add(name, apperr.RuleDate, nil)
		return nil
	}
	return &d
}

func (c *fieldCheck) maxLen(name string, v *string, max int) {
	if v != nil && len([]rune(*v)) > max {
		c.add(name, apperr.RuleMaxString, map[string]string{"max": itoa(max)})
	}
}

func (c *fieldCheck) money(name string, v *int64) {
	switch {
	case v == nil:
	case *v < 0:
		c.add(name, apperr.RuleMinNumber, map[string]string{"min": "0"})
	case *v > maxMoney:
		c.add(name, apperr.RuleMaxNumber, map[string]string{"max": itoa(maxMoney)})
	}
}

func (r registerRequest) input() (usecase.RegisterInput, error) {
	var c fieldCheck
	starts := c.date("starts_on", &r.StartsOn)
	ends := c.date("ends_on", r.EndsOn)
	if err := c.err(); err != nil {
		return usecase.RegisterInput{}, err
	}
	in := usecase.RegisterInput{RoomID: *r.RoomID, StartsOn: *starts, EndsOn: ends, Notes: r.Notes}
	if r.OccupantCount != nil {
		in.OccupantCount = *r.OccupantCount
	}
	return in, nil
}

func (r updateRequest) input() (usecase.UpdateInput, error) {
	var c fieldCheck
	in := usecase.UpdateInput{PriceVND: r.PriceVND, OccupantCount: r.OccupantCount}
	if r.Status != nil {
		switch s := domain.Status(*r.Status); s {
		case domain.StatusPending, domain.StatusActive, domain.StatusEnded, domain.StatusRejected:
			in.Status = &s
		default:
			c.add("status", apperr.RuleOneOf, nil)
		}
	}
	c.money("price_vnd", r.PriceVND)
	c.money("deposit_vnd", r.DepositVND.value)
	if r.OccupantCount != nil && (*r.OccupantCount < 1 || *r.OccupantCount > domain.MaxOccupants) {
		rule, params := apperr.RuleMinNumber, map[string]string{"min": "1"}
		if *r.OccupantCount > domain.MaxOccupants {
			rule, params = apperr.RuleMaxNumber, map[string]string{"max": itoa(domain.MaxOccupants)}
		}
		c.add("occupant_count", rule, params)
	}
	in.StartsOn = c.date("starts_on", r.StartsOn)
	if r.EndsOn.set {
		in.EndsOn = usecase.Some(c.date("ends_on", r.EndsOn.value))
	}
	c.maxLen("notes", r.Notes.value, 1000)
	c.maxLen("tenant_name", r.TenantName.value, 100)
	c.maxLen("tenant_phone", r.TenantPhone.value, 30)
	if r.DepositVND.set {
		in.DepositVND = usecase.Some(r.DepositVND.value)
	}
	if r.Notes.set {
		in.Notes = usecase.Some(r.Notes.value)
	}
	if r.TenantName.set {
		in.TenantName = usecase.Some(r.TenantName.value)
	}
	if r.TenantPhone.set {
		in.TenantPhone = usecase.Some(r.TenantPhone.value)
	}
	if err := c.err(); err != nil {
		return usecase.UpdateInput{}, err
	}
	return in, nil
}
