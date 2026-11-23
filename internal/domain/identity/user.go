// Package identity is the enterprise side of accounts and login sessions: the entities, the role and permission
// catalog, the pure rules and the errors. It imports nothing but the standard library, uuid and pkg/authz.
package identity

import (
	"time"

	"github.com/google/uuid"
)

// User is an account as the login needs it.
type User struct {
	ID              uuid.UUID
	Email           *string
	PasswordHash    *string
	Status          string
	LastActiveRole  *string
	EmailVerifiedAt *time.Time
	FullName        string
}

// UserProfile is an account with the ward and province of its address, for the user object of the API.
type UserProfile struct {
	ID             uuid.UUID
	Email          *string
	FullName       string
	PhoneNumber    *string
	Status         string
	AddressDetail  *string
	LastActiveRole *string
	TermsVersion   *string
	// AcceptsInvitations is false when the person asked not to be invited to rent a room or manage one.
	AcceptsInvitations bool
	CreatedAt          time.Time
	WardCode           *int32
	WardName           *string
	ProvinceCode       *int32
	ProvinceName       *string
}

// AuthUser is what a request needs to know about its caller: the account state and every role it holds,
// most senior first.
type AuthUser struct {
	Status         string
	LastActiveRole *string
	Roles          []string
}

// NewUser is an account to create.
type NewUser struct {
	ID              uuid.UUID
	Email           string
	PasswordHash    string
	FullName        string
	PhoneNumber     *string
	WardCode        *int32
	AddressDetail   *string
	LastActiveRole  string
	TermsVersion    string
	TermsAcceptedAt time.Time
	CreatedAt       time.Time
}

// DemoUser is a development account to create or refresh; its email counts as verified.
type DemoUser struct {
	ID             uuid.UUID
	Email          string
	PasswordHash   string
	FullName       string
	WardCode       *int32
	LastActiveRole string
	TermsVersion   string
	Now            time.Time
}
