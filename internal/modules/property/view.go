package property

import (
	"time"

	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	"rental-management/pkg/storage"
)

// The JSON views of the API.

// ProvinceView is a province inside a ward.
type ProvinceView struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}

// WardView is the ward of an address with its province.
type WardView struct {
	Code     int32        `json:"code"`
	Name     string       `json:"name"`
	Province ProvinceView `json:"province"`
}

// CoverView holds the URLs of a cover picture.
type CoverView struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
}

// ChargeView is a charge of a building or room.
type ChargeView struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	AmountVND       int64     `json:"amount_vnd"`
	Unit            string    `json:"unit"`
	Note            *string   `json:"note"`
	DisplayOrder    int       `json:"display_order"`
	FollowsBuilding bool      `json:"follows_building"`
}

// MediaView is a picture of a building or room.
type MediaView struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	URL          string    `json:"url"`
	ThumbURL     string    `json:"thumb_url"`
	ContentType  string    `json:"content_type"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int       `json:"size_bytes"`
	OriginalName *string   `json:"original_name"`
	DisplayOrder int       `json:"display_order"`
}

// BuildingSummary is a building as a list shows it.
type BuildingSummary struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	AddressDetail       string     `json:"address_detail"`
	Ward                *WardView  `json:"ward"`
	Description         *string    `json:"description"`
	PriceVND            *int64     `json:"price_vnd"`
	AreaSquareMeters    *float64   `json:"area_square_meters"`
	MinimumRentalPeriod *int       `json:"minimum_rental_period"`
	RentalPeriodUnit    *string    `json:"rental_period_unit"`
	Cover               *CoverView `json:"cover"`
	DisplayOrder        int        `json:"display_order"`
	RoomsCount          int        `json:"rooms_count"`
	AvailableRooms      int        `json:"available_rooms_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// BuildingDetail is one building with its charges and pictures.
type BuildingDetail struct {
	BuildingSummary
	Charges []ChargeView `json:"charges"`
	Media   []MediaView  `json:"media"`
}

// CopyResult is what copying a building answers.
type CopyResult struct {
	Building    BuildingDetail `json:"building"`
	RoomsCopied int            `json:"rooms_copied"`
}

func buildingSummary(r sqlcgen.SearchBuildingsRow, files storage.Storage) BuildingSummary {
	v := BuildingSummary{
		ID: r.ID, Name: r.Name, AddressDetail: r.AddressDetail, Description: r.Description, PriceVND: r.PriceVnd,
		AreaSquareMeters: r.AreaSquareMeters, RentalPeriodUnit: r.RentalPeriodUnit,
		Cover: coverView(files, r.CoverKey, r.CoverThumbKey), DisplayOrder: int(r.DisplayOrder),
		RoomsCount: int(r.RoomsCount), AvailableRooms: int(r.AvailableRoomsCount),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Ward: wardView(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName),
	}
	if r.MinimumRentalPeriod != nil {
		n := int(*r.MinimumRentalPeriod)
		v.MinimumRentalPeriod = &n
	}
	return v
}

func wardView(code *int32, name *string, provinceCode *int32, provinceName *string) *WardView {
	if code == nil || name == nil || provinceCode == nil || provinceName == nil {
		return nil
	}
	return &WardView{Code: *code, Name: *name, Province: ProvinceView{Code: *provinceCode, Name: *provinceName}}
}

func coverView(files storage.Storage, key, thumbKey *string) *CoverView {
	if key == nil || thumbKey == nil {
		return nil
	}
	return &CoverView{URL: files.URL(*key), ThumbURL: files.URL(*thumbKey)}
}

func chargeViews(rows []sqlcgen.ListChargesOfBuildingsRow) []ChargeView {
	out := make([]ChargeView, 0, len(rows))
	for _, c := range rows {
		out = append(out, ChargeView{
			ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note,
			DisplayOrder: int(c.DisplayOrder), FollowsBuilding: c.BuildingChargeID != nil,
		})
	}
	return out
}

func mediaView(m sqlcgen.Medium, files storage.Storage) MediaView {
	return MediaView{
		ID: m.ID, Kind: m.Kind, URL: files.URL(m.FileKey), ThumbURL: files.URL(m.ThumbKey),
		ContentType: m.ContentType, Width: int(m.Width), Height: int(m.Height), SizeBytes: int(m.SizeBytes),
		OriginalName: m.OriginalName, DisplayOrder: int(m.DisplayOrder),
	}
}

func mediaViews(rows []sqlcgen.Medium, files storage.Storage) []MediaView {
	out := make([]MediaView, 0, len(rows))
	for _, m := range rows {
		out = append(out, mediaView(m, files))
	}
	return out
}

// BuildingRef names the building of a room.
type BuildingRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// RoomSummary is a room as a list shows it.
type RoomSummary struct {
	ID                  uuid.UUID    `json:"id"`
	Name                string       `json:"name"`
	Status              string       `json:"status"`
	DisplayOrder        int          `json:"display_order"`
	Building            *BuildingRef `json:"building"`
	PriceVND            int64        `json:"price_vnd"`
	RentalPeriodUnit    string       `json:"rental_period_unit"`
	MinimumRentalPeriod int          `json:"minimum_rental_period"`
	AreaSquareMeters    *float64     `json:"area_square_meters"`
	AddressDetail       string       `json:"address_detail"`
	Ward                *WardView    `json:"ward"`
	// Inherits lists the parts that follow the building.
	Inherits  []string   `json:"inherits"`
	Cover     *CoverView `json:"cover"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// RoomDetail is one room with its description, charges and pictures.
type RoomDetail struct {
	RoomSummary
	Description *string      `json:"description"`
	Charges     []ChargeView `json:"charges"`
	Media       []MediaView  `json:"media"`
}

func roomSummary(r sqlcgen.SearchRoomsRow, files storage.Storage) RoomSummary {
	v := RoomSummary{
		ID: r.ID, Name: r.Name, Status: r.Status, DisplayOrder: int(r.DisplayOrder), PriceVND: r.PriceVnd,
		RentalPeriodUnit: r.RentalPeriodUnit, MinimumRentalPeriod: int(r.MinimumRentalPeriod),
		AreaSquareMeters: r.AreaSquareMeters, AddressDetail: r.AddressDetail,
		Ward:     wardView(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName),
		Inherits: []string{}, Cover: coverView(files, r.CoverKey, r.CoverThumbKey),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.BuildingID != nil && r.BuildingName != nil {
		v.Building = &BuildingRef{ID: *r.BuildingID, Name: *r.BuildingName}
	}
	follows := map[Group]bool{
		GroupPrice: r.InheritsPrice, GroupArea: r.InheritsArea, GroupRentalPeriod: r.InheritsRentalPeriod,
		GroupDescription: r.InheritsDescription, GroupCharges: r.InheritsCharges, GroupMedia: r.InheritsMedia,
	}
	for _, g := range Groups() {
		if follows[g] {
			v.Inherits = append(v.Inherits, string(g))
		}
	}
	return v
}

func roomChargeViews(rows []sqlcgen.ListChargesOfRoomsRow) []ChargeView {
	out := make([]ChargeView, 0, len(rows))
	for _, c := range rows {
		out = append(out, ChargeView{
			ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note,
			DisplayOrder: int(c.DisplayOrder), FollowsBuilding: c.BuildingChargeID != nil,
		})
	}
	return out
}
