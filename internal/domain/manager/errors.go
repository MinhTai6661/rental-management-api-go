package manager

import "errors"

// The errors of the manager rules and use cases. A link that is wrong, expired, answered, withdrawn, not for the caller
// or whose rooms changed all read the same (ErrInvitationInvalid), so a link tells nothing to whoever should not have it.
var (
	// ErrNotFound: no such hand-over or invitation for the caller.
	ErrNotFound = errors.New("manager record not found")
	// ErrInvitationInvalid: the link or invitation cannot be used by this caller.
	ErrInvitationInvalid = errors.New("invitation is not valid")
	// ErrInvitationNotPending: the invitation was already answered or withdrawn.
	ErrInvitationNotPending = errors.New("invitation is not waiting for an answer")
	// ErrLinkUnavailable: the link cannot be shown again (make a new one).
	ErrLinkUnavailable = errors.New("invitation link cannot be shown again")
	// ErrCannotManage: the account cannot be asked to manage (the landlord's own, inactive, not a tenant, staff).
	ErrCannotManage = errors.New("account cannot manage rooms")
	// ErrRoomNotOwned: a room is not the landlord's (or does not exist): both read the same.
	ErrRoomNotOwned = errors.New("room is not the landlord's")
	// ErrRoomsManaged: some rooms already have a manager.
	ErrRoomsManaged = errors.New("rooms already have a manager")
	// ErrRoomsPending: some rooms are in an invitation that waits for an answer.
	ErrRoomsPending = errors.New("rooms are in a waiting invitation")
	// ErrRoomsChanged: the rooms of an invitation are not what they were (deleted or handed to somebody else).
	ErrRoomsChanged = errors.New("rooms of the invitation changed")
	// ErrNoRooms: an invitation without rooms.
	ErrNoRooms = errors.New("no rooms given")
	// ErrReplaceNotListed: a room to replace is not among the rooms of the request.
	ErrReplaceNotListed = errors.New("replace_room_ids must be among room_ids")
	// ErrOwnInvitation: the landlord cannot answer their own invitation.
	ErrOwnInvitation = errors.New("cannot answer one's own invitation")
)

// RoomsError carries which rooms an error is about (their names, as the landlord knows them).
type RoomsError struct {
	Err   error
	Names []string
}

// Error describes the error.
func (e *RoomsError) Error() string { return e.Err.Error() }

// Unwrap lets errors.Is find the cause.
func (e *RoomsError) Unwrap() error { return e.Err }

// MaxRoomNames is how many room names an error message lists before it ends with an ellipsis.
const MaxRoomNames = 5

// NamesLabel joins up to MaxRoomNames names with ", " and ends with " …" when there are more.
func NamesLabel(names []string) string {
	shown := names
	more := false
	if len(shown) > MaxRoomNames {
		shown, more = shown[:MaxRoomNames], true
	}
	out := ""
	for i, n := range shown {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	if more {
		out += " …"
	}
	return out
}
