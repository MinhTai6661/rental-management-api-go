// Package contract is the domain of rental contracts between a landlord's room and a tenant: the types and the pure
// rules (which status can follow which, when a contract ends, how a rental period is counted, how the landlord's list
// is reordered). It imports no framework, driver or clock: the use cases load the data, pass "today" in and act on
// the answer. Dates are calendar dates, kept as time.Time at midnight UTC.
package contract

import (
	"time"

	"github.com/google/uuid"
)

// Status is where a contract stands in its life.
type Status string

// Contract statuses.
const (
	// StatusPending: a tenant registered for the room and waits for the landlord's answer.
	StatusPending  Status = "pending"
	StatusActive   Status = "active"
	StatusEnded    Status = "ended"
	StatusRejected Status = "rejected"
)

// CancellationStatus is where a tenant's request to end a running contract stands (nil on the contract: never asked).
type CancellationStatus string

// Cancellation statuses.
const (
	CancellationRequested CancellationStatus = "requested"
	CancellationApproved  CancellationStatus = "approved"
	CancellationDeclined  CancellationStatus = "declined"
)

// PeriodUnit is how a room measures its rental period: whole calendar months or plain days.
type PeriodUnit string

// Rental period units.
const (
	UnitMonth PeriodUnit = "month"
	UnitDay   PeriodUnit = "day"
)

// The room statuses contracts care about (a room under repair has another one and is left alone).
const (
	RoomAvailable = "available"
	RoomRented    = "rented"
)

// Limits of the data.
const (
	MaxOccupants = 50
)

// Cancellation is a tenant's request to end a running contract and the landlord's answer.
type Cancellation struct {
	Status       *CancellationStatus
	RequestedAt  *time.Time
	Reason       *string
	ResponseNote *string
}

// Contract is the state of a contract the rules work on. TenantID is nil for the blank contract the system opens for
// a room marked rented that nobody with an account holds (TenantName and TenantPhone then say who lives there).
type Contract struct {
	ID            uuid.UUID
	RoomID        uuid.UUID
	LandlordID    uuid.UUID
	TenantID      *uuid.UUID
	TenantName    *string
	TenantPhone   *string
	Status        Status
	PriceVND      int64
	DepositVND    *int64
	OccupantCount int
	StartsOn      time.Time
	EndsOn        *time.Time
	Notes         *string
	ConfirmedAt   *time.Time
	DisplayOrder  int
	Cancellation  Cancellation
}

// Unassigned says whether nobody with an account holds the contract.
func (c Contract) Unassigned() bool { return c.TenantID == nil }

// HeldBy says whether the user is the tenant of the contract.
func (c Contract) HeldBy(userID uuid.UUID) bool { return c.TenantID != nil && *c.TenantID == userID }

// Viewer is who is looking at contracts: a landlord (the contracts on their rooms) or a tenant (their own).
type Viewer struct {
	UserID     uuid.UUID
	AsLandlord bool
	// AsManager: the contracts of the rooms handed to the viewer, except the one they hold as tenant (read only).
	AsManager bool
}

// WardRef is the ward and province of a room's address.
type WardRef struct {
	Code     int32
	Name     string
	Province ProvinceRef
}

// ProvinceRef is a province.
type ProvinceRef struct {
	Code int32
	Name string
}

// Cover holds the storage keys of a room's cover picture.
type Cover struct{ Key, ThumbKey string }

// RoomRef is the room of a contract as a contract shows it.
type RoomRef struct {
	ID            uuid.UUID
	BuildingID    *uuid.UUID
	Name          string
	Status        string
	PriceVND      int64
	MinPeriod     int32
	PeriodUnit    PeriodUnit
	AddressDetail string
	Description   *string
	Area          *float64
	Ward          *WardRef
	Cover         *Cover
}

// Person is the other party of a contract as the viewer sees them: only what a contract needs to name and reach them.
type Person struct {
	ID          uuid.UUID
	FullName    string
	Email       *string
	PhoneNumber *string
}

// Detail is a contract as the lists and the detail show it.
type Detail struct {
	Contract
	Room     RoomRef
	Tenant   *Person
	Landlord Person
	// Billing is read for the lists (see Billing); it is the zero value elsewhere.
	Billing   Billing
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChargeLine is a cost on top of the rent shown to the renter.
type ChargeLine struct {
	ID        uuid.UUID
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
}

// Picture is a picture of a rented room.
type Picture struct {
	ID       uuid.UUID
	Kind     string
	FileKey  string
	ThumbKey string
	Width    int
	Height   int
}

// RentalDetail is a contract as its tenant sees it ("my rooms"): the room with its costs and pictures.
type RentalDetail struct {
	Detail
	Charges  []ChargeLine
	Pictures []Picture
}

// Query is a page of a viewer's contract list. A nil or empty filter is off.
type Query struct {
	Statuses           []Status
	RoomID             *uuid.UUID
	BuildingID         *uuid.UUID
	NoBuilding         bool
	CancellationStatus *CancellationStatus
	// SearchLike is a lower case LIKE pattern body, already escaped.
	SearchLike *string
	// PaymentState keeps the contracts in that state of money (the renter's filter).
	PaymentState *PaymentState
	// HasUnpaidInvoice keeps the contracts that have a bill not paid yet (the landlord's filter).
	HasUnpaidInvoice bool
	// Sort is "" for the landlord's own order, else created_at, starts_on or ends_on with an optional "-" prefix.
	Sort   string
	Limit  int
	Offset int
}

// RoomFacts is what contracts need to know about a room.
type RoomFacts struct {
	ID         uuid.UUID
	LandlordID uuid.UUID
	Name       string
	Status     string
	PriceVND   int64
	MinPeriod  int32
	PeriodUnit PeriodUnit
	// Live is false for a room that was deleted.
	Live bool
	// Listed: an admin approved a listing of the room. Only a listed room can be registered for.
	Listed bool
}

// Occupancy tells how many contracts run on a room.
type Occupancy struct {
	// Running counts every running contract, blank ones included; WithTenant only those a tenant holds.
	Running    int
	WithTenant int
}

// OrderedID is a contract in the landlord's list order.
type OrderedID struct {
	ID    uuid.UUID
	Order int
}

// Position is the place a contract takes in the list.
type Position struct {
	ID    uuid.UUID
	Order int
}

// StatusCounts is how many contracts a tenant has in each status.
type StatusCounts map[Status]int
