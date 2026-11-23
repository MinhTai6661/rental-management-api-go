package property

import (
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// BuildingInput is the body of a building: a full replacement of its fields. A nil Charges keeps the current ones,
// an empty list removes them all.
type BuildingInput struct {
	Name          string
	WardCode      *int32
	AddressDetail string
	Description   *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Charges       []domain.ChargeInput
}

// BuildingFilter narrows the list of buildings.
type BuildingFilter struct {
	Name         string
	WardCode     *int32
	ProvinceCode *int32
	// Sort is "", "name", "-name", "created_at" or "-created_at"; the default is the landlord's own order.
	Sort string
}

// FirstBuilding is the building a new landlord starts with.
type FirstBuilding struct {
	Name          string
	WardCode      *int32
	AddressDetail string
}

// RoomInput is the body of a room: a full replacement of its fields. An absent value (nil) means the room has none
// of its own for that part; a room inside a building then follows the building (see domain.ResolveInheritance). A
// nil Charges keeps (update) or follows (create) the charges; an empty list means none. A nil Status keeps the
// current status on update and is "available" on create.
type RoomInput struct {
	BuildingID    *uuid.UUID
	Name          string
	WardCode      *int32
	AddressDetail *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
	Status        *domain.Status
	Charges       []domain.ChargeInput
	Inherit       *[]domain.Group
}

// RoomFilter narrows the list of rooms.
type RoomFilter struct {
	Name         string
	Status       *string
	BuildingID   *uuid.UUID
	NoBuilding   bool
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	// Sort is "", "name", "-name", "price", "-price", "area", "-area", "created_at" or "-created_at"; the default is
	// the landlord's own order.
	Sort string
}

// RunInput asks for a run of rooms in one building: NamePrefix + number From..To (zero padded to Padding digits).
// The values it carries are the rooms' own; those left out follow the building.
type RunInput struct {
	NamePrefix  string
	From, To    int
	Padding     int
	Status      *domain.Status
	PriceVND    *int64
	Area        *float64
	MinPeriod   *int32
	PeriodUnit  *string
	Description *string
	Charges     []domain.ChargeInput
}

// RunResult tells which rooms a run created, in the order of their names.
type RunResult struct {
	IDs []uuid.UUID
}

// Upload is a picture a client sent.
type Upload struct {
	FileName string
	Data     []byte
	// Kind is "photo" (the default when empty) or "cover".
	Kind string
}

// normalized checks what the request tags cannot and cleans the text: the name and address must not be blank, the
// rental period is given whole or not at all, the area keeps two decimals and the charges are trimmed.
func (in BuildingInput) normalized() (BuildingInput, []domain.ChargeInput, error) {
	var violations []domain.Violation
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		violations = append(violations, domain.Violation{Name: "name", Rule: domain.RuleRequired})
	}
	in.AddressDetail = strings.TrimSpace(in.AddressDetail)
	if in.AddressDetail == "" {
		violations = append(violations, domain.Violation{Name: "address_detail", Rule: domain.RuleRequired})
	}
	in.Description = domain.BlankToNil(in.Description)
	if (in.MinPeriod == nil) != (in.PeriodUnit == nil) {
		violations = append(violations, domain.Violation{Name: "minimum_rental_period", Rule: domain.RuleRentalPeriodPair})
	}
	if in.Area != nil {
		area := domain.RoundArea(*in.Area)
		in.Area = &area
	}
	var charges []domain.ChargeInput
	if in.Charges != nil {
		var chargeViolations []domain.Violation
		charges, chargeViolations = domain.NormalizeCharges(in.Charges)
		violations = append(violations, chargeViolations...)
	}
	if len(violations) > 0 {
		return in, nil, domain.Invalid(violations...)
	}
	return in, charges, nil
}

// data is what the building stores.
func (in BuildingInput) data() domain.BuildingData {
	return domain.BuildingData{Name: in.Name, BuildingDefaults: in.defaults()}
}

// defaults is what the building offers its rooms.
func (in BuildingInput) defaults() domain.BuildingDefaults {
	return domain.BuildingDefaults{
		WardCode: in.WardCode, AddressDetail: in.AddressDetail, PriceVND: in.PriceVND, Area: in.Area,
		MinPeriod: in.MinPeriod, PeriodUnit: in.PeriodUnit, Description: in.Description,
	}
}

// normalized cleans the text and rounds the area. It returns the charges in the form the rules use.
func (in RoomInput) normalized() (RoomInput, []domain.ChargeInput, error) {
	var violations []domain.Violation
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		violations = append(violations, domain.Violation{Name: "name", Rule: domain.RuleRequired})
	}
	in.AddressDetail = domain.BlankToNil(in.AddressDetail)
	in.Description = domain.BlankToNil(in.Description)
	if in.Area != nil {
		area := domain.RoundArea(*in.Area)
		in.Area = &area
	}
	charges, chargeViolations := domain.NormalizeCharges(in.Charges)
	violations = append(violations, chargeViolations...)
	if len(violations) > 0 {
		return in, nil, domain.Invalid(violations...)
	}
	return in, charges, nil
}

// fields is what the room request carries, in the form the inheritance rules take.
func (in RoomInput) fields(charges []domain.ChargeInput) domain.RoomFields {
	return domain.RoomFields{
		WardCode: in.WardCode, AddressDetail: in.AddressDetail, PriceVND: in.PriceVND, Area: in.Area,
		MinPeriod: in.MinPeriod, PeriodUnit: in.PeriodUnit, Description: in.Description, Charges: charges,
	}
}

func statusOrDefault(s *domain.Status, fallback domain.Status) domain.Status {
	if s == nil {
		return fallback
	}
	return *s
}
