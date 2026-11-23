package postgres

import (
	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/property"
)

// The mapping between the generated property rows and the property domain types.

// periodToDB narrows an optional minimum rental period to the column's int16 (validated to 1..3650 before).
func periodToDB(v *int32) *int16 {
	if v == nil {
		return nil
	}
	n := int16(*v) //nolint:gosec // validated to 1..3650
	return &n
}

// periodFromDB widens the column back to the domain's int32.
func periodFromDB(v *int16) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v)
	return &n
}

func wardRef(code *int32, name *string, provinceCode *int32, provinceName *string) *domain.WardRef {
	if code == nil || name == nil || provinceCode == nil || provinceName == nil {
		return nil
	}
	return &domain.WardRef{Code: *code, Name: *name, Province: domain.ProvinceRef{Code: *provinceCode, Name: *provinceName}}
}

func coverRef(key, thumbKey *string) *domain.Cover {
	if key == nil || thumbKey == nil {
		return nil
	}
	return &domain.Cover{Key: *key, ThumbKey: *thumbKey}
}

// inheritsOf lists the groups a room follows, in the fixed order (never nil).
func inheritsOf(price, area, rentalPeriod, description, charges, media bool) []domain.Group {
	follows := map[domain.Group]bool{
		domain.GroupPrice: price, domain.GroupArea: area, domain.GroupRentalPeriod: rentalPeriod,
		domain.GroupDescription: description, domain.GroupCharges: charges, domain.GroupMedia: media,
	}
	out := []domain.Group{}
	for _, g := range domain.Groups() {
		if follows[g] {
			out = append(out, g)
		}
	}
	return out
}

// inheritFlags are the inherits_* columns of a room.
type inheritFlags struct {
	price, area, rentalPeriod, description, charges, media bool
}

func flagsOf(groups []domain.Group) inheritFlags {
	has := func(g domain.Group) bool {
		for _, x := range groups {
			if x == g {
				return true
			}
		}
		return false
	}
	return inheritFlags{
		price: has(domain.GroupPrice), area: has(domain.GroupArea), rentalPeriod: has(domain.GroupRentalPeriod),
		description: has(domain.GroupDescription), charges: has(domain.GroupCharges), media: has(domain.GroupMedia),
	}
}

func buildingSummaryOf(r sqlcgen.SearchBuildingsRow) domain.BuildingSummary {
	v := domain.BuildingSummary{
		ID: r.ID, Name: r.Name, WardCode: r.WardCode, Ward: wardRef(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName),
		AddressDetail: r.AddressDetail, Description: r.Description, PriceVND: r.PriceVnd, Area: r.AreaSquareMeters,
		PeriodUnit: r.RentalPeriodUnit, Cover: coverRef(r.CoverKey, r.CoverThumbKey), DisplayOrder: int(r.DisplayOrder),
		RoomsCount: int(r.RoomsCount), AvailableRooms: int(r.AvailableRoomsCount), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.MinimumRentalPeriod != nil {
		n := int(*r.MinimumRentalPeriod)
		v.MinPeriod = &n
	}
	return v
}

func roomSummaryOf(r sqlcgen.SearchRoomsRow) domain.RoomSummary {
	v := domain.RoomSummary{
		ID: r.ID, Name: r.Name, Status: domain.Status(r.Status), DisplayOrder: int(r.DisplayOrder), PriceVND: r.PriceVnd,
		PeriodUnit: r.RentalPeriodUnit, MinPeriod: int(r.MinimumRentalPeriod), Area: r.AreaSquareMeters,
		AddressDetail: r.AddressDetail, Ward: wardRef(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName),
		Description: r.Description,
		Inherits: inheritsOf(r.InheritsPrice, r.InheritsArea, r.InheritsRentalPeriod, r.InheritsDescription,
			r.InheritsCharges, r.InheritsMedia),
		Cover: coverRef(r.CoverKey, r.CoverThumbKey), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.BuildingID != nil && r.BuildingName != nil {
		v.Building = &domain.BuildingRef{ID: *r.BuildingID, Name: *r.BuildingName}
	}
	return v
}

func buildingOf(r sqlcgen.GetBuildingForUpdateRow) domain.Building {
	return domain.Building{ID: r.ID, BuildingData: domain.BuildingData{Name: r.Name, BuildingDefaults: domain.BuildingDefaults{
		WardCode: r.WardCode, AddressDetail: r.AddressDetail, PriceVND: r.PriceVnd, Area: r.AreaSquareMeters,
		MinPeriod: periodFromDB(r.MinimumRentalPeriod), PeriodUnit: r.RentalPeriodUnit, Description: r.Description,
	}}}
}

func roomOf(r sqlcgen.GetRoomForUpdateRow) domain.Room {
	return domain.Room{ID: r.ID, RoomData: domain.RoomData{
		BuildingID: r.BuildingID, Name: r.Name,
		RoomValues: domain.RoomValues{
			WardCode: r.WardCode, AddressDetail: r.AddressDetail, PriceVND: r.PriceVnd, Area: r.AreaSquareMeters,
			MinPeriod: int32(r.MinimumRentalPeriod), PeriodUnit: r.RentalPeriodUnit, Description: r.Description,
		},
		Status:   domain.Status(r.Status),
		Inherits: inheritsOf(r.InheritsPrice, r.InheritsArea, r.InheritsRentalPeriod, r.InheritsDescription, r.InheritsCharges, r.InheritsMedia),
	}}
}

func mediaOf(m sqlcgen.Medium) domain.Media {
	return domain.Media{
		ID: m.ID, RoomID: m.RoomID, Kind: m.Kind, FileKey: m.FileKey, ThumbKey: m.ThumbKey, ContentType: m.ContentType,
		Width: int(m.Width), Height: int(m.Height), SizeBytes: int(m.SizeBytes), OriginalName: m.OriginalName,
		DisplayOrder: int(m.DisplayOrder),
	}
}

func mediasOf(rows []sqlcgen.Medium) []domain.Media {
	out := make([]domain.Media, len(rows))
	for i, m := range rows {
		out[i] = mediaOf(m)
	}
	return out
}

// ownerColumns gives the two owner columns of the media queries: the one that matches is set, the other is nil.
func ownerColumns(o domain.MediaOwner) (room, building *uuid.UUID) {
	id := o.ID()
	if o.IsRoom() {
		return &id, nil
	}
	return nil, &id
}
