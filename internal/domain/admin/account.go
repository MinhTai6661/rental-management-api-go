package admin

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Account is a user as the admin sees them.
type Account struct {
	ID             uuid.UUID
	Email          *string
	FullName       string
	PhoneNumber    *string
	Status         string
	LastActiveRole *string
	WardCode       *int32
	AddressDetail  *string
	CreatedAt      time.Time
	// Roles are the roles held, most senior first.
	Roles []string
}

// UserSort is how the user list is ordered.
type UserSort string

// The orders of the user list; the newest first is the default.
const (
	SortNewest UserSort = "created_at"
	SortName   UserSort = "name"
	SortEmail  UserSort = "email"
)

// ParseUserSort returns the order named s; "" is the default.
func ParseUserSort(s string) (UserSort, bool) {
	switch UserSort(s) {
	case "":
		return SortNewest, true
	case SortNewest, SortName, SortEmail:
		return UserSort(s), true
	}
	return "", false
}

// Account statuses.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// UserFilter is what the user list is narrowed by.
type UserFilter struct {
	// ViewerID is never listed; IncludeStaff shows the accounts of staff (only a super admin sees them).
	ViewerID     uuid.UUID
	IncludeStaff bool
	Status       *string
	RoleName     *string
	// Search is one word looked for in the name or the email, lower case, with LIKE wildcards escaped.
	Search       *string
	WardCode     *int32
	ProvinceCode *int32
	Sort         UserSort
}

// EscapeLike makes text safe inside a LIKE pattern and lower-cases it ("" gives nil: no search).
func EscapeLike(text string) *string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	out := r.Replace(text)
	return &out
}

// BulkFailure is one item of a bulk request that was refused.
type BulkFailure struct {
	ID     uuid.UUID
	Name   string
	Reason error
}

// BulkResult is what a bulk request did: the ids done and the items refused (the rest still ran).
type BulkResult struct {
	Done   []uuid.UUID
	Failed []BulkFailure
}

// CheckBulkIDs checks the number of ids of a bulk request (1 to MaxBulkIDs).
func CheckBulkIDs(n int) error {
	switch {
	case n == 0:
		return ErrNoIDs
	case n > MaxBulkIDs:
		return ErrTooManyIDs
	}
	return nil
}

// ErasedName is the name an erased account is left with.
const ErasedName = "Người dùng đã xóa"
