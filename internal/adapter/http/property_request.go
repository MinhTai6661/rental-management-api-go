package httpadapter

import (
	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	usecase "rental-management/internal/usecase/property"
)

// The bodies of the property requests. The validate tags are the rules of the API; errors are reported under the
// json names.

type chargeRequest struct {
	ID        *uuid.UUID `json:"id"`
	Name      string     `json:"name" validate:"required,max=100"`
	Kind      string     `json:"kind" validate:"required,oneof=service electricity water internet parking cleaning other"`
	AmountVND *int64     `json:"amount_vnd" validate:"required,min=0,max=1000000000000"`
	Unit      string     `json:"unit" validate:"required,oneof=per_period per_person per_kwh per_cubic_meter one_time"`
	Note      *string    `json:"note" validate:"omitempty,max=255"`
}

type buildingRequest struct {
	Name                string          `json:"name" validate:"required,max=50"`
	WardCode            *int32          `json:"ward_code"`
	AddressDetail       string          `json:"address_detail" validate:"required,max=500"`
	Description         *string         `json:"description" validate:"omitempty,max=2000"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
}

type copyBuildingRequest struct {
	Name         string `json:"name" validate:"required,max=50"`
	IncludeRooms bool   `json:"include_rooms"`
}

type roomRequest struct {
	BuildingID          *uuid.UUID      `json:"building_id"`
	Name                string          `json:"name" validate:"required,max=50"`
	WardCode            *int32          `json:"ward_code"`
	AddressDetail       *string         `json:"address_detail" validate:"omitempty,max=500"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Description         *string         `json:"description" validate:"omitempty,max=5000"`
	Status              *string         `json:"status" validate:"omitempty,oneof=available rented maintenance"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
	Inherit             *[]string       `json:"inherit" validate:"omitempty,dive,oneof=price area rental_period description charges media"`
}

type runRequest struct {
	NamePrefix          string          `json:"name_prefix" validate:"max=50"`
	From                *int32          `json:"from" validate:"required,min=0,max=999999"`
	To                  *int32          `json:"to" validate:"required,min=0,max=999999"`
	Padding             *int32          `json:"padding" validate:"omitempty,min=0,max=10"`
	Status              *string         `json:"status" validate:"omitempty,oneof=available maintenance"`
	PriceVND            *int64          `json:"price_vnd" validate:"omitempty,min=0,max=1000000000000"`
	AreaSquareMeters    *float64        `json:"area_square_meters" validate:"omitempty,min=0.01,max=99999.99"`
	MinimumRentalPeriod *int32          `json:"minimum_rental_period" validate:"omitempty,min=1,max=3650"`
	RentalPeriodUnit    *string         `json:"rental_period_unit" validate:"omitempty,oneof=month day"`
	Description         *string         `json:"description" validate:"omitempty,max=5000"`
	Charges             []chargeRequest `json:"charges" validate:"omitempty,max=30,dive"`
}

type bulkStatusRequest struct {
	IDs    []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
	Status string      `json:"status" validate:"required,oneof=available rented maintenance"`
}

type idsRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
}

type orderRequest struct {
	IDs []uuid.UUID `json:"ids" validate:"required,min=1,max=1000,unique"`
}

type arrangeRequest struct {
	Items []arrangeItemRequest `json:"items" validate:"required,min=1,max=50,dive"`
}

type arrangeItemRequest struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind" validate:"required,oneof=photo cover"`
}

func (r buildingRequest) input() usecase.BuildingInput {
	return usecase.BuildingInput{
		Name: r.Name, WardCode: r.WardCode, AddressDetail: r.AddressDetail, Description: r.Description,
		PriceVND: r.PriceVND, Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Charges: chargeInputs(r.Charges),
	}
}

// chargeInputs converts the charges of a request; nil stays nil (the list is not part of the request).
func chargeInputs(items []chargeRequest) []domain.ChargeInput {
	if items == nil {
		return nil
	}
	out := make([]domain.ChargeInput, len(items))
	for i, c := range items {
		out[i] = domain.ChargeInput{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: *c.AmountVND, Unit: c.Unit, Note: c.Note}
	}
	return out
}

func statusPtr(s *string) *domain.Status {
	if s == nil {
		return nil
	}
	status := domain.Status(*s)
	return &status
}

func (r roomRequest) input() usecase.RoomInput {
	in := usecase.RoomInput{
		BuildingID: r.BuildingID, Name: r.Name, WardCode: r.WardCode, AddressDetail: r.AddressDetail, PriceVND: r.PriceVND,
		Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Description: r.Description, Status: statusPtr(r.Status), Charges: chargeInputs(r.Charges),
	}
	if r.Inherit != nil {
		groups := make([]domain.Group, len(*r.Inherit))
		for i, g := range *r.Inherit {
			groups[i] = domain.Group(g)
		}
		in.Inherit = &groups
	}
	return in
}

func (r runRequest) input() usecase.RunInput {
	in := usecase.RunInput{
		NamePrefix: r.NamePrefix, From: int(*r.From), To: int(*r.To), Status: statusPtr(r.Status), PriceVND: r.PriceVND,
		Area: r.AreaSquareMeters, MinPeriod: r.MinimumRentalPeriod, PeriodUnit: r.RentalPeriodUnit,
		Description: r.Description, Charges: chargeInputs(r.Charges),
	}
	if r.Padding != nil {
		in.Padding = int(*r.Padding)
	}
	return in
}
