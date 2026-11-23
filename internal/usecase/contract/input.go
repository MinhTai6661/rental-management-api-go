package contract

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// Opt is an optional field of a partial update: Set says the request carried it, Value is what it said (a nil
// pointer inside means "clear the field").
type Opt[T any] struct {
	Set   bool
	Value T
}

// Some builds an Opt that carries v.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: v} }

// ListFilter narrows a viewer's contract list.
type ListFilter struct {
	Status             *domain.Status
	RoomID             *uuid.UUID
	BuildingID         *uuid.UUID
	NoBuilding         bool
	CancellationStatus *domain.CancellationStatus
	// HasUnpaidInvoice keeps the contracts that have a bill not paid yet.
	HasUnpaidInvoice bool
	// Search matches the room name and the tenant's name or email.
	Search string
	// Sort is "" for the landlord's own order, else created_at, starts_on or ends_on with an optional "-" prefix.
	Sort string
}

// UpdateInput is a partial update of a contract by its landlord: only what is set changes. A nil Status keeps it
// (the status moves along the life cycle: this is how a registration is answered, a contract ended or reopened).
type UpdateInput struct {
	Status        *domain.Status
	PriceVND      *int64
	DepositVND    Opt[*int64]
	OccupantCount *int
	StartsOn      *time.Time
	EndsOn        Opt[*time.Time]
	Notes         Opt[*string]
	// TenantName and TenantPhone are only taken by a contract nobody holds (an account's own name is never
	// overwritten).
	TenantName  Opt[*string]
	TenantPhone Opt[*string]

	// requireFrom, when set, refuses the change unless the stored contract has that status (a bulk answer only
	// answers what still waits, even when an earlier answer in the same request settled it).
	requireFrom domain.Status
}

// RegisterInput is a tenant's request to rent a room.
type RegisterInput struct {
	RoomID        uuid.UUID
	StartsOn      time.Time
	EndsOn        *time.Time
	OccupantCount int // 0 means one
	Notes         *string
}

// Answer is what a landlord does to a waiting registration in a bulk action.
type Answer string

// The answers.
const (
	AnswerApprove Answer = "approve"
	AnswerReject  Answer = "reject"
)

// Rental groups: the tabs of the renter's "my rooms" page.
const (
	GroupActive  = "active"
	GroupPending = "pending"
	GroupHistory = "history"
)

// GroupStatuses lists the statuses behind a group (nil for an unknown group: all contracts).
func GroupStatuses(group string) []domain.Status {
	switch group {
	case GroupActive:
		return []domain.Status{domain.StatusActive}
	case GroupPending:
		return []domain.Status{domain.StatusPending}
	case GroupHistory:
		return []domain.Status{domain.StatusEnded, domain.StatusRejected}
	}
	return nil
}
