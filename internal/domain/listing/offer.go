package listing

import (
	"time"

	"github.com/google/uuid"
)

// An offer is a room as find rooms shows it: a vacant room (available, nobody with an account holds a running
// contract on it, its landlord is still a landlord) with an approved listing. The offer read models carry what the
// search shows; the HTTP layer decides how much of it a guest or a renter gets.

// OfferText is the headline and the text of the approved listing behind an offer.
type OfferText struct {
	ID          uuid.UUID
	Title       string
	Description *string
}

// OfferLandlord is the landlord of an offered room: the name to show and nothing to reach them with.
type OfferLandlord struct {
	ID       uuid.UUID
	FullName string
}

// Offer is an offered room as a search result.
type Offer struct {
	RoomID     uuid.UUID
	Name       string
	BuildingID *uuid.UUID
	// HouseName is the name of the boarding house: the building the room is in, or the landlord's own name when the
	// room stands outside any building.
	HouseName     string
	Area          *float64
	PriceVND      int64
	MinPeriod     int32
	PeriodUnit    string
	Description   *string
	AddressDetail string
	Ward          *WardRef
	Cover         *Cover
	Listing       OfferText
	Landlord      OfferLandlord
	CreatedAt     time.Time
}

// OfferDetail is an offered room with its costs and pictures.
type OfferDetail struct {
	Offer
	Charges  []ChargeLine
	Pictures []Picture
}

// LandlordCard is a landlord as a renter or a guest may see them: their name, the houses they run and how many
// rooms of theirs are on find rooms now.
type LandlordCard struct {
	ID          uuid.UUID
	FullName    string
	Houses      []string
	VacantRooms int
}

// Priority is how high one landlord's rooms rank in the default order of the search (greater first).
type Priority struct {
	LandlordID uuid.UUID
	Value      int
}

// OfferQuery selects a page of offered rooms. Nil filters are off.
type OfferQuery struct {
	LandlordID *uuid.UUID
	// HouseLike is a lower case LIKE pattern body, already escaped, matched against the house name: the building a
	// room is in, or the landlord's name for a room outside any building.
	HouseLike    *string
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	// Sort is "" for the default order (the landlord's plan priority, then newest first), or one of price, -price,
	// area, -area, created_at, -created_at.
	Sort string
	// Priorities are the plan priorities of the landlords that matter for the default order; a landlord left out
	// counts as 0.
	Priorities []Priority
	Limit      int
	Offset     int
}

// Sorts of the search.
var OfferSorts = []string{"price", "-price", "area", "-area", "created_at", "-created_at"}

// Sorts of a list of listings.
var ListingSorts = []string{"created_at", "-created_at", "submitted_at", "title", "price", "-price", "area", "-area"}

// DefaultSort is the order of a list when no sort is asked for: oldest waiting first for the admins' queue, else newest.
func DefaultSort(status Status) string {
	if status == StatusPending {
		return "submitted_at"
	}
	return "-created_at"
}

// RoomState is what decides whether a room is on find rooms. The repository's search applies the same rule in SQL;
// this is the rule in words, and what its tests pin down.
type RoomState struct {
	// Status is the room's status (available, rented, maintenance).
	Status string
	// Live is false for a deleted room.
	Live bool
	// TenantContractRunning: somebody with an account holds a running contract on the room (a blank contract does
	// not count: it exists only while the room is marked rented).
	TenantContractRunning bool
	// LandlordHolds: the owner still holds the landlord role (a landlord whose role was revoked is not offered).
	LandlordHolds bool
	// ApprovedListing: an admin approved a listing of the room.
	ApprovedListing bool
}

// Offered reports whether find rooms shows the room.
func (s RoomState) Offered() bool {
	return s.Live && s.Status == "available" && !s.TenantContractRunning && s.LandlordHolds && s.ApprovedListing
}
