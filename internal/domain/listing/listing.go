// Package listing holds the rules of listings, the posts with which a landlord offers a room on find rooms: the
// statuses and which can follow which, what an edit of the text does, and the read models of the search renters
// use. It imports the standard library and uuid only: no database, no HTTP, no other context.
package listing

import (
	"time"

	"github.com/google/uuid"
)

// Status is where a listing stands.
type Status string

// The statuses of a listing.
const (
	// StatusDraft is being written; nobody else sees it.
	StatusDraft Status = "draft"
	// StatusPending was sent to the admins and waits for a decision.
	StatusPending Status = "pending"
	// StatusApproved is live: renters find the room on find rooms (while it is vacant).
	StatusApproved Status = "approved"
	// StatusRejected was refused by the admins; the landlord can edit it and send it again.
	StatusRejected Status = "rejected"
	// StatusUnpublished was taken down (by the landlord or an admin).
	StatusUnpublished Status = "unpublished"
)

// ParseStatus reads a status code.
func ParseStatus(s string) (Status, bool) {
	switch st := Status(s); st {
	case StatusDraft, StatusPending, StatusApproved, StatusRejected, StatusUnpublished:
		return st, true
	}
	return "", false
}

// Statuses lists every status, in the order a landlord works through them.
func Statuses() []Status {
	return []Status{StatusDraft, StatusPending, StatusApproved, StatusRejected, StatusUnpublished}
}

// IsOpen reports whether the listing still claims its room: a room has at most one open listing.
func (s Status) IsOpen() bool { return s == StatusPending || s == StatusApproved }

// Limits of the text of a listing.
const (
	MaxTitleLength       = 150
	MaxDescriptionLength = 5000
	MaxNoteLength        = 1000
)

// Listing is a landlord's post offering one room. The price, pictures and address are the room's own.
type Listing struct {
	ID          uuid.UUID
	RoomID      uuid.UUID
	LandlordID  uuid.UUID
	Title       string
	Description *string
	Status      Status
	SubmittedAt *time.Time
	// ReviewedByID, ReviewedAt and ReviewNote are the admin's last decision (an approval, a refusal or a take-down).
	ReviewedByID  *uuid.UUID
	ReviewedAt    *time.Time
	ReviewNote    *string
	PublishedAt   *time.Time
	UnpublishedAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Person is somebody named on a listing (the landlord, the reviewer): what an admin needs to know who they are.
type Person struct {
	ID          uuid.UUID
	FullName    string
	Email       *string
	PhoneNumber *string
}

// Cover is the cover picture of a room.
type Cover struct {
	Key      string
	ThumbKey string
}

// WardRef is the ward of an address with its province.
type WardRef struct {
	Code         int32
	Name         string
	ProvinceCode int32
	ProvinceName string
}

// RoomRef is the room a listing offers, as the landlord and the admins see it in a list.
type RoomRef struct {
	ID            uuid.UUID
	Name          string
	Status        string
	PriceVND      int64
	MinPeriod     int32
	PeriodUnit    string
	AddressDetail string
	Area          *float64
	Description   *string
	Cover         *Cover
	Ward          *WardRef
	BuildingID    *uuid.UUID
	// Live is false when the room was deleted after the listing was written.
	Live bool
}

// ChargeLine is a cost on top of the rent of a room.
type ChargeLine struct {
	ID        uuid.UUID
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
}

// Picture is a picture of a room: its own, or the building's while it follows them.
type Picture struct {
	ID       uuid.UUID
	Kind     string
	Key      string
	ThumbKey string
	Width    int
	Height   int
}

// Detail is a listing with its room and the people it names. Charges and Pictures are filled by the detail
// lookups only (a list leaves them empty).
type Detail struct {
	Listing
	Room     RoomRef
	Landlord Person
	Reviewer *Person
	Charges  []ChargeLine
	Pictures []Picture
}

// RoomFacts are the facts about a room that writing a listing needs.
type RoomFacts struct {
	ID         uuid.UUID
	LandlordID uuid.UUID
	Name       string
	// Live is false for a deleted room.
	Live bool
}

// SeedRoom is a room the demo seed can write a listing for.
type SeedRoom struct {
	ID          uuid.UUID
	Name        string
	Description *string
}

// Query selects a page of listings, newest first by default. Nil filters are off.
type Query struct {
	// LandlordID limits the list to one landlord's listings (nil: every landlord's, for the admins).
	LandlordID *uuid.UUID
	Status     *Status
	// ExcludeDrafts hides the drafts: a draft is the landlord's own business until it is sent.
	ExcludeDrafts bool
	// SearchLike is a lower case LIKE pattern body, already escaped, matched against the title and the room name.
	SearchLike   *string
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	Sort         string
	Limit        int
	Offset       int
}

// StatusCounts says how many listings there are in each status.
type StatusCounts map[Status]int
