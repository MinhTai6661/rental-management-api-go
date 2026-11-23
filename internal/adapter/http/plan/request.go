package planhttp

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/plan"
	usecase "rental-management/internal/usecase/plan"
)

// The bodies of the plan requests. The validate tags are the rules of the API; errors are reported under the json
// names.

const (
	maxMoney = 1_000_000_000_000
	maxCount = 1_000_000
)

type subscribeRequest struct {
	PlanID *uuid.UUID `json:"plan_id" validate:"required"`
}

type assignRequest struct {
	PlanID *uuid.UUID `json:"plan_id" validate:"required"`
	// EndsAt is when the plan lapses (RFC 3339); left out, a paid plan lasts one billing interval and a free one forever.
	EndsAt *string `json:"ends_at"`
}

// endsAt reads the end; a value that is not a date and time is reported on the field.
func (r assignRequest) endsAt() (*time.Time, error) {
	if r.EndsAt == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *r.EndsAt)
	if err != nil {
		return nil, apperr.Invalid(apperr.Field{Name: "ends_at", Rule: apperr.RuleDateTime})
	}
	return &t, nil
}

type createPlanRequest struct {
	Code                    string   `json:"code" validate:"required,max=50"`
	Name                    string   `json:"name" validate:"required,max=100"`
	Description             *string  `json:"description" validate:"omitempty,max=1000"`
	Audience                string   `json:"audience" validate:"required,oneof=landlord tenant"`
	PriceVND                *int64   `json:"price_vnd" validate:"required,min=0,max=1000000000000"`
	BillingInterval         *string  `json:"billing_interval" validate:"omitempty,oneof=month year"`
	MaxRooms                *int     `json:"max_rooms" validate:"omitempty,min=0,max=1000000"`
	MaxBuildings            *int     `json:"max_buildings" validate:"omitempty,min=0,max=1000000"`
	MaxImagesPerRoom        *int     `json:"max_images_per_room" validate:"omitempty,min=1,max=100"`
	MaxActiveContracts      *int     `json:"max_active_contracts" validate:"omitempty,min=0,max=1000000"`
	MaxPendingRegistrations *int     `json:"max_pending_registrations" validate:"omitempty,min=0,max=1000000"`
	SearchPriority          *int     `json:"search_priority" validate:"omitempty,min=0,max=65535"`
	Features                []string `json:"features" validate:"omitempty,unique,dive,oneof=auto_invoicing vip_badge priority_support"`
	IsDefault               *bool    `json:"is_default"`
	IsActive                *bool    `json:"is_active"`
	DisplayOrder            *int     `json:"display_order" validate:"omitempty,min=0,max=65535"`
}

// input turns the validated body into the use case's input. A plan is active unless the request says otherwise.
func (r createPlanRequest) input() usecase.PlanInput {
	audience, _ := domain.ParseAudience(r.Audience)
	in := usecase.PlanInput{
		Code: r.Code, Name: r.Name, Description: r.Description, Audience: audience, PriceVND: *r.PriceVND,
		Interval: domain.IntervalMonth, Limits: domain.Limits{
			MaxRooms: r.MaxRooms, MaxBuildings: r.MaxBuildings, MaxImagesPerRoom: r.MaxImagesPerRoom,
			MaxActiveContracts: r.MaxActiveContracts, MaxPendingRegistrations: r.MaxPendingRegistrations,
		},
		Features: featuresOf(r.Features), IsActive: true,
	}
	if r.BillingInterval != nil {
		in.Interval, _ = domain.ParseInterval(*r.BillingInterval)
	}
	if r.SearchPriority != nil {
		in.SearchPriority = *r.SearchPriority
	}
	if r.IsDefault != nil {
		in.IsDefault = *r.IsDefault
	}
	if r.IsActive != nil {
		in.IsActive = *r.IsActive
	}
	if r.DisplayOrder != nil {
		in.DisplayOrder = *r.DisplayOrder
	}
	return in
}

func featuresOf(codes []string) []domain.Feature {
	out := make([]domain.Feature, 0, len(codes))
	for _, c := range codes {
		f, _ := domain.ParseFeature(c)
		out = append(out, f)
	}
	return out
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

// updatePlanRequest is a partial update: only the fields the body carries change, and a null limit means unlimited.
// The audience cannot change. The limits and the description are checked by patch(), because an optional field has no
// validate tag.
type updatePlanRequest struct {
	Code                    *string          `json:"code" validate:"omitempty,min=1,max=50"`
	Name                    *string          `json:"name" validate:"omitempty,min=1,max=100"`
	Description             optional[string] `json:"description"`
	PriceVND                *int64           `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	BillingInterval         *string          `json:"billing_interval" validate:"omitempty,oneof=month year"`
	MaxRooms                optional[int]    `json:"max_rooms"`
	MaxBuildings            optional[int]    `json:"max_buildings"`
	MaxImagesPerRoom        optional[int]    `json:"max_images_per_room"`
	MaxActiveContracts      optional[int]    `json:"max_active_contracts"`
	MaxPendingRegistrations optional[int]    `json:"max_pending_registrations"`
	SearchPriority          *int             `json:"search_priority" validate:"omitempty,min=0,max=65535"`
	Features                *[]string        `json:"features" validate:"omitempty,unique,dive,oneof=auto_invoicing vip_badge priority_support"`
	IsDefault               *bool            `json:"is_default"`
	IsActive                *bool            `json:"is_active"`
	DisplayOrder            *int             `json:"display_order" validate:"omitempty,min=0,max=65535"`
}

// patch turns the body into the use case's patch, reporting the rules the validate tags cannot express.
func (r updatePlanRequest) patch() (usecase.PlanPatch, error) {
	var fields []apperr.Field
	limit := func(name string, o optional[int], lower, upper int) usecase.Optional[int] {
		if o.value != nil {
			switch {
			case *o.value < lower:
				fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleMinNumber, Params: map[string]string{"min": itoa(lower)}})
			case *o.value > upper:
				fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": itoa(upper)}})
			}
		}
		return usecase.Optional[int]{Set: o.set, Value: o.value}
	}
	p := usecase.PlanPatch{
		Code: r.Code, Name: r.Name, PriceVND: r.PriceVND, SearchPriority: r.SearchPriority, IsDefault: r.IsDefault,
		IsActive: r.IsActive, DisplayOrder: r.DisplayOrder,
		Description:             usecase.Optional[string]{Set: r.Description.set, Value: r.Description.value},
		MaxRooms:                limit("max_rooms", r.MaxRooms, 0, maxCount),
		MaxBuildings:            limit("max_buildings", r.MaxBuildings, 0, maxCount),
		MaxImagesPerRoom:        limit("max_images_per_room", r.MaxImagesPerRoom, 1, 100),
		MaxActiveContracts:      limit("max_active_contracts", r.MaxActiveContracts, 0, maxCount),
		MaxPendingRegistrations: limit("max_pending_registrations", r.MaxPendingRegistrations, 0, maxCount),
	}
	if r.Description.value != nil && len([]rune(*r.Description.value)) > 1000 {
		fields = append(fields, apperr.Field{Name: "description", Rule: apperr.RuleMaxString, Params: map[string]string{"max": "1000"}})
	}
	if r.BillingInterval != nil {
		i, _ := domain.ParseInterval(*r.BillingInterval)
		p.Interval = &i
	}
	if r.Features != nil {
		f := featuresOf(*r.Features)
		p.Features = &f
	}
	if len(fields) > 0 {
		return usecase.PlanPatch{}, apperr.Invalid(fields...)
	}
	return p, nil
}
