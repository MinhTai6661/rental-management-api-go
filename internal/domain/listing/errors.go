package listing

import (
	"errors"
	"fmt"
)

// The errors of the listing use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code, the fields and the message parameters the client sees.
var (
	// ErrNotFound: the listing does not exist, or the caller may not see it.
	ErrNotFound = errors.New("listing not found")
	// ErrRoomNotFound: the room of a new listing is not a live room of the landlord.
	ErrRoomNotFound = errors.New("room does not exist")
	// ErrItemsNotFound: not every id of a list names a listing the caller may review.
	ErrItemsNotFound = errors.New("some ids do not exist")
	// ErrInvalidTransition: the listing cannot move to that status.
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrUnpublishFirst: a live listing must be taken down before it is deleted.
	ErrUnpublishFirst = errors.New("take the listing down first")
	// ErrNoteRequired: a refusal or a take-down must say why.
	ErrNoteRequired = errors.New("a reason is required")
)

// RoomAlreadyListedError: the room has a listing that is waiting for review or live.
type RoomAlreadyListedError struct{ Room string }

func (e *RoomAlreadyListedError) Error() string {
	return fmt.Sprintf("room %s already has an open listing", e.Room)
}

// LimitError: the landlord's plan allows no more open listings.
type LimitError struct{ Limit int }

func (e *LimitError) Error() string { return fmt.Sprintf("plan allows %d open listings", e.Limit) }
