// Package property is the domain of a landlord's buildings, rooms, charges and pictures: the types and the pure
// rules (what a room inherits from its building, how a run of rooms is named, how a list is reordered, which charge
// changes a request asks for). It imports no framework, driver or clock: the use cases load the data, call the rules
// and act on the answer.
package property

import (
	"strings"

	"github.com/google/uuid"
)

// Group is a part of a room that can follow its building.
type Group string

// The groups, in the fixed order the API lists them.
const (
	GroupPrice        Group = "price"
	GroupArea         Group = "area"
	GroupRentalPeriod Group = "rental_period"
	GroupDescription  Group = "description"
	GroupCharges      Group = "charges"
	GroupMedia        Group = "media"
)

// Groups lists every group in its fixed order.
func Groups() []Group {
	return []Group{GroupPrice, GroupArea, GroupRentalPeriod, GroupDescription, GroupCharges, GroupMedia}
}

// Status is the state of a room.
type Status string

// Room statuses.
const (
	StatusAvailable   Status = "available"
	StatusRented      Status = "rented"
	StatusMaintenance Status = "maintenance"
)

// Rental period units.
const (
	UnitMonth = "month"
	UnitDay   = "day"
)

// Limits of the data.
const (
	MaxRoomsPerRun    = 100
	MaxRoomNameLength = 50
	// DefaultPeriod is the minimum rental period of a room that sets none.
	DefaultPeriod = 1
)

// Picture kinds.
const (
	KindPhoto = "photo"
	KindCover = "cover"
)

// MediaKeyPrefix is the start of every storage key this context writes; the public picture route serves nothing else.
const MediaKeyPrefix = "property/"

// ChargeRow is a stored charge as the rules see it.
type ChargeRow struct {
	ID         uuid.UUID
	TemplateID *uuid.UUID // on a room's copy: the building charge it follows
}

// ChargeInput is a charge of a request; ID names a charge already stored (for an update).
type ChargeInput struct {
	ID        *uuid.UUID
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
}

// ChargeUpdate and ChargeCreate are the steps PlanChargeSync asks for; Order is the 1-based position.
type ChargeUpdate struct {
	ID    uuid.UUID
	Input ChargeInput
	Order int
}

// ChargeCreate adds a new charge at a position.
type ChargeCreate struct {
	Input ChargeInput
	Order int
}

// BuildingDefaults is what a building offers its rooms.
type BuildingDefaults struct {
	WardCode      *int32
	AddressDetail string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
}

// RoomFields is what a room request carries.
type RoomFields struct {
	WardCode      *int32
	AddressDetail *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
	Charges       []ChargeInput // nil = the request does not mention charges
}

// RoomState is what an existing room currently follows.
type RoomState struct {
	InheritsCharges bool
	InheritsMedia   bool
}

// RoomValues are the values in force for a room after resolving the inheritance.
type RoomValues struct {
	WardCode      *int32
	AddressDetail string
	PriceVND      int64
	Area          *float64
	MinPeriod     int32
	PeriodUnit    string
	Description   *string
}

// OrderedID is a row with its stored position.
type OrderedID struct {
	ID    uuid.UUID
	Order int
}

// Position is a new place in a list.
type Position struct {
	ID    uuid.UUID
	Order int
}

// Blocker is what stops a room from being changed or deleted; the contract and listing contexts fill it in.
type Blocker struct {
	ActiveContract bool
	OpenListing    bool
}

// StatusChange is a room that moved from one status to another; From is "" for a new room.
type StatusChange struct {
	RoomID uuid.UUID
	From   Status
	To     Status
}

// MediaRow is a picture as the rules see it.
type MediaRow struct {
	ID           uuid.UUID
	Kind         string
	DisplayOrder int
}

// ArrangeItem is one picture of an arrangement request.
type ArrangeItem struct {
	ID   uuid.UUID
	Kind string
}

// MediaOwner is what a picture belongs to: a room or a building. Both work the same way.
type MediaOwner struct {
	room bool
	id   uuid.UUID
}

// RoomMedia is the pictures of a room.
func RoomMedia(id uuid.UUID) MediaOwner { return MediaOwner{room: true, id: id} }

// BuildingMedia is the pictures of a building.
func BuildingMedia(id uuid.UUID) MediaOwner { return MediaOwner{id: id} }

// IsRoom says whether the owner is a room (otherwise a building).
func (o MediaOwner) IsRoom() bool { return o.room }

// ID is the id of the room or building.
func (o MediaOwner) ID() uuid.UUID { return o.id }

// BlankToNil trims s and returns nil for blank text.
func BlankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
