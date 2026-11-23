package httpadapter

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	usecase "rental-management/internal/usecase/property"
)

// The bodies of the property responses. The json tags are the contract with the web app.

// FileURLs turns the storage key of a picture into the public address the client loads it from.
type FileURLs interface {
	URL(key string) string
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

// BuildingSummaryView is a building as a list shows it.
type BuildingSummaryView struct {
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

// BuildingDetailView is one building with its charges and pictures.
type BuildingDetailView struct {
	BuildingSummaryView
	Charges []ChargeView `json:"charges"`
	Media   []MediaView  `json:"media"`
}

// CopyView is what copying a building answers.
type CopyView struct {
	Building    BuildingDetailView `json:"building"`
	RoomsCopied int                `json:"rooms_copied"`
}

// BuildingRefView names the building of a room.
type BuildingRefView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// RoomSummaryView is a room as a list shows it.
type RoomSummaryView struct {
	ID                  uuid.UUID        `json:"id"`
	Name                string           `json:"name"`
	Status              string           `json:"status"`
	DisplayOrder        int              `json:"display_order"`
	Building            *BuildingRefView `json:"building"`
	PriceVND            int64            `json:"price_vnd"`
	RentalPeriodUnit    string           `json:"rental_period_unit"`
	MinimumRentalPeriod int              `json:"minimum_rental_period"`
	AreaSquareMeters    *float64         `json:"area_square_meters"`
	AddressDetail       string           `json:"address_detail"`
	Ward                *WardView        `json:"ward"`
	// Inherits lists the parts that follow the building.
	Inherits  []string   `json:"inherits"`
	Cover     *CoverView `json:"cover"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// RoomDetailView is one room with its description, charges and pictures.
type RoomDetailView struct {
	RoomSummaryView
	Description *string      `json:"description"`
	Charges     []ChargeView `json:"charges"`
	Media       []MediaView  `json:"media"`
}

// RunView tells which rooms a run created, in the order of their names.
type RunView struct {
	IDs   []uuid.UUID `json:"ids"`
	Count int         `json:"count"`
}

type bulkFailureView struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// BulkView tells which items a bulk operation did and which it refused (with the reason in the request's language).
type BulkView struct {
	Done   []uuid.UUID       `json:"done"`
	Failed []bulkFailureView `json:"failed"`
}

func wardViewOf(w *domain.WardRef) *WardView {
	if w == nil {
		return nil
	}
	return &WardView{Code: w.Code, Name: w.Name, Province: ProvinceView{Code: w.Province.Code, Name: w.Province.Name}}
}

func coverViewOf(c *domain.Cover, urls FileURLs) *CoverView {
	if c == nil {
		return nil
	}
	return &CoverView{URL: urls.URL(c.Key), ThumbURL: urls.URL(c.ThumbKey)}
}

func chargeViewsOf(items []domain.Charge) []ChargeView {
	out := make([]ChargeView, 0, len(items))
	for _, c := range items {
		out = append(out, ChargeView{
			ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVND, Unit: c.Unit, Note: c.Note,
			DisplayOrder: c.Order, FollowsBuilding: c.FollowsBuilding(),
		})
	}
	return out
}

func mediaViewOf(m domain.Media, urls FileURLs) MediaView {
	return MediaView{
		ID: m.ID, Kind: m.Kind, URL: urls.URL(m.FileKey), ThumbURL: urls.URL(m.ThumbKey), ContentType: m.ContentType,
		Width: m.Width, Height: m.Height, SizeBytes: m.SizeBytes, OriginalName: m.OriginalName, DisplayOrder: m.DisplayOrder,
	}
}

func mediaViewsOf(items []domain.Media, urls FileURLs) []MediaView {
	out := make([]MediaView, 0, len(items))
	for _, m := range items {
		out = append(out, mediaViewOf(m, urls))
	}
	return out
}

func buildingSummaryViewOf(b domain.BuildingSummary, urls FileURLs) BuildingSummaryView {
	return BuildingSummaryView{
		ID: b.ID, Name: b.Name, AddressDetail: b.AddressDetail, Ward: wardViewOf(b.Ward), Description: b.Description,
		PriceVND: b.PriceVND, AreaSquareMeters: b.Area, MinimumRentalPeriod: b.MinPeriod, RentalPeriodUnit: b.PeriodUnit,
		Cover: coverViewOf(b.Cover, urls), DisplayOrder: b.DisplayOrder, RoomsCount: b.RoomsCount,
		AvailableRooms: b.AvailableRooms, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

func buildingSummaryViewsOf(items []domain.BuildingSummary, urls FileURLs) []BuildingSummaryView {
	out := make([]BuildingSummaryView, len(items))
	for i, b := range items {
		out[i] = buildingSummaryViewOf(b, urls)
	}
	return out
}

func buildingDetailViewOf(b domain.BuildingDetail, urls FileURLs) BuildingDetailView {
	return BuildingDetailView{
		BuildingSummaryView: buildingSummaryViewOf(b.BuildingSummary, urls),
		Charges:             chargeViewsOf(b.Charges),
		Media:               mediaViewsOf(b.Media, urls),
	}
}

func roomSummaryViewOf(r domain.RoomSummary, urls FileURLs) RoomSummaryView {
	v := RoomSummaryView{
		ID: r.ID, Name: r.Name, Status: string(r.Status), DisplayOrder: r.DisplayOrder, PriceVND: r.PriceVND,
		RentalPeriodUnit: r.PeriodUnit, MinimumRentalPeriod: r.MinPeriod, AreaSquareMeters: r.Area,
		AddressDetail: r.AddressDetail, Ward: wardViewOf(r.Ward), Inherits: []string{}, Cover: coverViewOf(r.Cover, urls),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Building != nil {
		v.Building = &BuildingRefView{ID: r.Building.ID, Name: r.Building.Name}
	}
	for _, g := range r.Inherits {
		v.Inherits = append(v.Inherits, string(g))
	}
	return v
}

func roomSummaryViewsOf(items []domain.RoomSummary, urls FileURLs) []RoomSummaryView {
	out := make([]RoomSummaryView, len(items))
	for i, r := range items {
		out[i] = roomSummaryViewOf(r, urls)
	}
	return out
}

func roomDetailViewOf(r domain.RoomDetail, urls FileURLs) RoomDetailView {
	return RoomDetailView{
		RoomSummaryView: roomSummaryViewOf(r.RoomSummary, urls), Description: r.Description,
		Charges: chargeViewsOf(r.Charges), Media: mediaViewsOf(r.Media, urls),
	}
}

func runViewOf(r usecase.RunResult) RunView { return RunView{IDs: r.IDs, Count: len(r.IDs)} }
