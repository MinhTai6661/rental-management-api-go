package listinghttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// The bodies of the listing responses. The json tags are the contract with the web app.

// FileURLs turns the storage key of a picture into the public address the client loads it from.
type FileURLs interface {
	URL(key string) string
}

// CoverView holds the URLs of a cover picture.
type CoverView struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
}

// WardView is the ward of a room's address.
type WardView struct {
	Code     int32  `json:"code"`
	Name     string `json:"name"`
	Province struct {
		Code int32  `json:"code"`
		Name string `json:"name"`
	} `json:"province"`
}

// ChargeView is a cost on top of the rent.
type ChargeView struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	AmountVND int64     `json:"amount_vnd"`
	Unit      string    `json:"unit"`
	Note      *string   `json:"note"`
}

// PictureView is a picture of a room.
type PictureView struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	URL      string    `json:"url"`
	ThumbURL string    `json:"thumb_url"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
}

// PersonView is the landlord or the reviewer of a listing as an admin sees them.
type PersonView struct {
	ID          uuid.UUID `json:"id"`
	FullName    string    `json:"full_name"`
	Email       *string   `json:"email"`
	PhoneNumber *string   `json:"phone_number"`
}

// ListingRoomView is the room a listing offers, in a landlord's or an admin's list.
type ListingRoomView struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	PriceVND            int64      `json:"price_vnd"`
	MinimumRentalPeriod int32      `json:"minimum_rental_period"`
	RentalPeriodUnit    string     `json:"rental_period_unit"`
	AddressDetail       string     `json:"address_detail"`
	AreaSquareMeters    *float64   `json:"area_square_meters"`
	Description         *string    `json:"description"`
	BuildingID          *uuid.UUID `json:"building_id"`
	Ward                *WardView  `json:"ward"`
	Cover               *CoverView `json:"cover"`
}

// ListingView is a listing in the lists. The landlord and the reviewer are given to the admins only.
type ListingView struct {
	ID            uuid.UUID       `json:"id"`
	RoomID        uuid.UUID       `json:"room_id"`
	Title         string          `json:"title"`
	Description   *string         `json:"description"`
	Status        string          `json:"status"`
	SubmittedAt   *time.Time      `json:"submitted_at"`
	ReviewedAt    *time.Time      `json:"reviewed_at"`
	ReviewNote    *string         `json:"review_note"`
	PublishedAt   *time.Time      `json:"published_at"`
	UnpublishedAt *time.Time      `json:"unpublished_at"`
	Room          ListingRoomView `json:"room"`
	Landlord      *PersonView     `json:"landlord,omitempty"`
	Reviewer      *PersonView     `json:"reviewer,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// ListingDetailRoomView is the room of a listing's detail: with its costs and its pictures.
type ListingDetailRoomView struct {
	ListingRoomView
	Charges  []ChargeView  `json:"charges"`
	Pictures []PictureView `json:"pictures"`
}

// ListingDetailView is the detail of a listing; its room carries the costs and the pictures (the field of the same
// name shadows the embedded one).
type ListingDetailView struct {
	ListingView
	Room ListingDetailRoomView `json:"room"`
}

// FailureView is a listing a bulk review refused.
type FailureView struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// BulkView is the outcome of a bulk review.
type BulkView struct {
	Done   []uuid.UUID   `json:"done"`
	Failed []FailureView `json:"failed"`
}

// OfferTextView is the headline and the text of the approved listing behind an offered room.
type OfferTextView struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Description *string   `json:"description"`
}

// OfferLandlordView is the landlord of an offered room: the name to show and nothing to reach them with.
type OfferLandlordView struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
}

// PublicOfferView is an offered room as a guest sees it: what it costs, how it looks and in which ward. Never the
// street address, who rents it, or how to reach the landlord.
type PublicOfferView struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	HouseName           string            `json:"house_name"`
	BuildingID          *uuid.UUID        `json:"building_id"`
	AreaSquareMeters    *float64          `json:"area_square_meters"`
	PriceVND            int64             `json:"price_vnd"`
	MinimumRentalPeriod int32             `json:"minimum_rental_period"`
	RentalPeriodUnit    string            `json:"rental_period_unit"`
	Description         *string           `json:"description"`
	Ward                *WardView         `json:"ward"`
	Cover               *CoverView        `json:"cover"`
	Listing             OfferTextView     `json:"listing"`
	Landlord            OfferLandlordView `json:"landlord"`
	CreatedAt           time.Time         `json:"created_at"`
}

// OfferView is an offered room as a signed in renter sees it: the public side and the address.
type OfferView struct {
	PublicOfferView
	AddressDetail string `json:"address_detail"`
}

// PublicOfferDetailView is the detail of an offered room for a guest: with its costs and its pictures.
type PublicOfferDetailView struct {
	PublicOfferView
	Charges  []ChargeView  `json:"charges"`
	Pictures []PictureView `json:"pictures"`
}

// OfferDetailView is the detail of an offered room for a renter.
type OfferDetailView struct {
	OfferView
	Charges  []ChargeView  `json:"charges"`
	Pictures []PictureView `json:"pictures"`
}

// LandlordView is a landlord as a renter or a guest sees them: the name, the houses they run, how many rooms of
// theirs are on find rooms and where to list them. Never an email, a phone number or an address.
type LandlordView struct {
	ID               uuid.UUID `json:"id"`
	FullName         string    `json:"full_name"`
	Houses           []string  `json:"houses"`
	VacantRoomsCount int       `json:"vacant_rooms_count"`
	RoomsURL         string    `json:"rooms_url"`
}

func coverViewOf(c *domain.Cover, urls FileURLs) *CoverView {
	if c == nil {
		return nil
	}
	return &CoverView{URL: urls.URL(c.Key), ThumbURL: urls.URL(c.ThumbKey)}
}

func wardViewOf(w *domain.WardRef) *WardView {
	if w == nil {
		return nil
	}
	v := &WardView{Code: w.Code, Name: w.Name}
	v.Province.Code, v.Province.Name = w.ProvinceCode, w.ProvinceName
	return v
}

func personViewOf(p domain.Person) *PersonView {
	return &PersonView{ID: p.ID, FullName: p.FullName, Email: p.Email, PhoneNumber: p.PhoneNumber}
}

func roomViewOf(r domain.RoomRef, urls FileURLs) ListingRoomView {
	return ListingRoomView{
		ID: r.ID, Name: r.Name, Status: r.Status, PriceVND: r.PriceVND, MinimumRentalPeriod: r.MinPeriod,
		RentalPeriodUnit: r.PeriodUnit, AddressDetail: r.AddressDetail, AreaSquareMeters: r.Area, Description: r.Description,
		BuildingID: r.BuildingID, Ward: wardViewOf(r.Ward), Cover: coverViewOf(r.Cover, urls),
	}
}

// listingViewOf is a listing as its landlord sees it (admin: with the people it names).
func listingViewOf(d domain.Detail, urls FileURLs, admin bool) ListingView {
	v := ListingView{
		ID: d.ID, RoomID: d.RoomID, Title: d.Title, Description: d.Description, Status: string(d.Status),
		SubmittedAt: d.SubmittedAt, ReviewedAt: d.ReviewedAt, ReviewNote: d.ReviewNote, PublishedAt: d.PublishedAt,
		UnpublishedAt: d.UnpublishedAt, Room: roomViewOf(d.Room, urls), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
	if admin {
		v.Landlord = personViewOf(d.Landlord)
		if d.Reviewer != nil {
			v.Reviewer = personViewOf(*d.Reviewer)
		}
	}
	return v
}

func listingViewsOf(items []domain.Detail, urls FileURLs, admin bool) []ListingView {
	out := make([]ListingView, len(items))
	for i, d := range items {
		out[i] = listingViewOf(d, urls, admin)
	}
	return out
}

func chargeViewsOf(items []domain.ChargeLine) []ChargeView {
	out := make([]ChargeView, len(items))
	for i, c := range items {
		out[i] = ChargeView{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVND, Unit: c.Unit, Note: c.Note}
	}
	return out
}

func pictureViewsOf(items []domain.Picture, urls FileURLs) []PictureView {
	out := make([]PictureView, len(items))
	for i, p := range items {
		out[i] = PictureView{ID: p.ID, Kind: p.Kind, URL: urls.URL(p.Key), ThumbURL: urls.URL(p.ThumbKey), Width: p.Width, Height: p.Height}
	}
	return out
}

func listingDetailViewOf(d domain.Detail, urls FileURLs, admin bool) ListingDetailView {
	return ListingDetailView{
		ListingView: listingViewOf(d, urls, admin),
		Room: ListingDetailRoomView{
			ListingRoomView: roomViewOf(d.Room, urls), Charges: chargeViewsOf(d.Charges), Pictures: pictureViewsOf(d.Pictures, urls),
		},
	}
}

func publicOfferViewOf(o domain.Offer, urls FileURLs) PublicOfferView {
	return PublicOfferView{
		ID: o.RoomID, Name: o.Name, HouseName: o.HouseName, BuildingID: o.BuildingID, AreaSquareMeters: o.Area,
		PriceVND: o.PriceVND, MinimumRentalPeriod: o.MinPeriod, RentalPeriodUnit: o.PeriodUnit, Description: o.Description,
		Ward: wardViewOf(o.Ward), Cover: coverViewOf(o.Cover, urls),
		Listing:  OfferTextView{ID: o.Listing.ID, Title: o.Listing.Title, Description: o.Listing.Description},
		Landlord: OfferLandlordView{ID: o.Landlord.ID, FullName: o.Landlord.FullName}, CreatedAt: o.CreatedAt,
	}
}

func offerViewOf(o domain.Offer, urls FileURLs) OfferView {
	return OfferView{PublicOfferView: publicOfferViewOf(o, urls), AddressDetail: o.AddressDetail}
}

func landlordViewOf(c domain.LandlordCard, roomsPath string) LandlordView {
	return LandlordView{
		ID: c.ID, FullName: c.FullName, Houses: c.Houses, VacantRoomsCount: c.VacantRooms,
		RoomsURL: roomsPath + "?landlord_id=" + c.ID.String(),
	}
}
