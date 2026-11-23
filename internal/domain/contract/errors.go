package contract

import (
	"errors"
	"fmt"
	"time"
)

// The errors of the contract use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code, the fields and the message parameters the client sees.
var (
	// ErrNotFound: the contract does not exist, or the caller may not see it.
	ErrNotFound = errors.New("contract not found")
	// ErrRoomNotFound: the room a tenant registers for does not exist.
	ErrRoomNotFound = errors.New("room does not exist")
	// ErrItemsNotFound: not every id of a list names a contract the caller sees.
	ErrItemsNotFound = errors.New("some ids do not exist")
	// ErrInvalidTransition: the contract cannot move to that status (or be answered now).
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrRoomOccupied: another contract already runs on the room.
	ErrRoomOccupied = errors.New("room already has a running contract")
	// ErrRoomNotVacant: the room is not free to be registered for.
	ErrRoomNotVacant = errors.New("room is not vacant")
	// ErrRoomNotListed: the room has no approved listing, so it is not on offer.
	ErrRoomNotListed = errors.New("room has no approved listing")
	// ErrAlreadyRegistered: the tenant already has a waiting or running contract on the room.
	ErrAlreadyRegistered = errors.New("tenant already registered for the room")
	// ErrOwnRoom: nobody rents their own room.
	ErrOwnRoom = errors.New("cannot rent one's own room")
	// ErrCancellationNotAllowed: the request to end a contract (or its answer) does not fit the contract's state.
	ErrCancellationNotAllowed = errors.New("cancellation request is not allowed now")
	// ErrNotDeletable: a tenant may only take back a registration that still waits.
	ErrNotDeletable = errors.New("contract cannot be deleted by the tenant")
	// ErrStartInPast: a registration cannot start before today.
	ErrStartInPast = errors.New("start date is in the past")
	// ErrEndNotAfterStart: the end date must come after the start date.
	ErrEndNotAfterStart = errors.New("end date must be after the start date")
	// ErrOccupantsInvalid: the number of occupants is outside 1..MaxOccupants.
	ErrOccupantsInvalid = errors.New("occupant count is invalid")
)

// BelowMinimumPeriodError: the contract ends before the room's minimum rental period is over.
type BelowMinimumPeriodError struct {
	Amount   int32
	Unit     PeriodUnit
	Earliest time.Time
}

func (e *BelowMinimumPeriodError) Error() string {
	return fmt.Sprintf("contract must last at least %d %s, until %s", e.Amount, e.Unit, e.Earliest.Format(DateLayout))
}

// ContractLimitError: the landlord's plan allows no more running contracts.
type ContractLimitError struct{ Limit int }

func (e *ContractLimitError) Error() string {
	return fmt.Sprintf("plan allows %d running contracts", e.Limit)
}

// RegistrationLimitError: the tenant's plan allows no more waiting registrations.
type RegistrationLimitError struct{ Limit int }

func (e *RegistrationLimitError) Error() string {
	return fmt.Sprintf("plan allows %d waiting registrations", e.Limit)
}
