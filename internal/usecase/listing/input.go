package listing

import (
	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// Optional is a field of a partial update that tells apart "absent" (leave it) from null (clear it).
type Optional[T any] struct {
	Set   bool
	Value *T
}

// CreateInput is a new listing: the room it offers and its headline and text.
type CreateInput struct {
	RoomID      uuid.UUID
	Title       string
	Description *string
}

// UpdateInput is a partial update of the headline and the text. The room of a listing never changes: write a new
// listing for another room.
type UpdateInput struct {
	Title       *string
	Description Optional[string]
}

// ListFilter narrows a list of listings; nil filters are off.
type ListFilter struct {
	Status       *domain.Status
	Search       string
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	Sort         string
}

// Page is a page of listings with how many there are in each status.
type Page struct {
	Items  []domain.Detail
	Total  int
	Counts domain.StatusCounts
}

// OfferFilter narrows the search of find rooms; nil filters are off.
type OfferFilter struct {
	LandlordID   *uuid.UUID
	HouseName    string
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	Sort         string
}

// OfferPage is a page of offered rooms.
type OfferPage struct {
	Items []domain.Offer
	Total int
}

// Action is what an admin does to several listings at once.
type Action string

// The bulk actions.
const (
	ActionApprove Action = "approve"
	ActionReject  Action = "reject"
)

// BulkFailure is a listing a bulk operation refused; Err is the domain error that says why.
type BulkFailure struct {
	ID   uuid.UUID
	Name string
	Err  error
}

// BulkResult tells which listings a bulk operation did and which it refused.
type BulkResult struct {
	Done   []uuid.UUID
	Failed []BulkFailure
}
