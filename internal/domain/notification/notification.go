package notification

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Notification is one message in a person's inbox.
type Notification struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Kind   Kind
	// Params are the values the web puts in the text of the kind (room, invoice_number, amount_vnd, ...).
	Params map[string]string
	// URL is the page of the app the message leads to.
	URL      string
	Audience Audience
	// AnnouncementID is the announcement the message belongs to, nil for the system's own messages.
	AnnouncementID *uuid.UUID
	ReadAt         *time.Time
	// CreatedAt is when the message was sent.
	CreatedAt time.Time
}

// Read reports whether the person has read it.
func (n Notification) Read() bool { return n.ReadAt != nil }

// Limits of the inbox.
const (
	// DefaultLatest and MaxLatest bound the bell: how many of the newest messages it shows.
	DefaultLatest = 8
	MaxLatest     = 20
	// MaxBulkIDs is how many messages one bulk request may name.
	MaxBulkIDs = 100
	// MaxURLChars is the longest address a notification may lead to.
	MaxURLChars = 500
)

// DeleteScope says which messages "clear my inbox" removes.
type DeleteScope string

// The scopes.
const (
	DeleteRead DeleteScope = "read"
	DeleteAll  DeleteScope = "all"
)

// ParseDeleteScope returns the scope named s.
func ParseDeleteScope(s string) (DeleteScope, bool) {
	switch d := DeleteScope(s); d {
	case DeleteRead, DeleteAll:
		return d, true
	}
	return "", false
}

// BulkAction is what a bulk request does to the messages it names.
type BulkAction string

// The bulk actions.
const (
	BulkRead   BulkAction = "read"
	BulkDelete BulkAction = "delete"
)

// ParseBulkAction returns the action named s.
func ParseBulkAction(s string) (BulkAction, bool) {
	switch b := BulkAction(s); b {
	case BulkRead, BulkDelete:
		return b, true
	}
	return "", false
}

// ErrTooManyIDs is returned when a bulk request names more than MaxBulkIDs messages.
var ErrTooManyIDs = errors.New("too many notification ids")

// ClampLatest is the number of messages the bell shows: DefaultLatest for none asked, else 1 to MaxLatest.
func ClampLatest(n int) int {
	switch {
	case n <= 0:
		return DefaultLatest
	case n > MaxLatest:
		return MaxLatest
	}
	return n
}
