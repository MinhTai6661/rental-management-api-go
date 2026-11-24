package admin

import (
	"slices"
	"time"

	"rental-management/pkg/authz"
)

// The reasons an account cannot be erased yet.
const (
	BlockerRunningRental   = "running_rental_as_tenant"
	BlockerRunningContract = "running_contract_as_landlord"
)

// ErasureFacts is what decides whether an account may be erased.
type ErasureFacts struct {
	// AsTenant: the account is the tenant of a contract that runs or waits for the landlord.
	AsTenant bool
	// AsLandlord: a room of the account has such a contract.
	AsLandlord bool
}

// Blockers lists why the account cannot be erased yet (empty: it can).
func (f ErasureFacts) Blockers() []string {
	out := []string{}
	if f.AsTenant {
		out = append(out, BlockerRunningRental)
	}
	if f.AsLandlord {
		out = append(out, BlockerRunningContract)
	}
	return out
}

// CheckErasable decides whether the account may erase itself: staff accounts never do, and a running or waiting
// contract blocks it.
func CheckErasable(held []string, facts ErasureFacts) error {
	if slices.ContainsFunc(held, authz.IsStaff) {
		return ErrStaffCannotErase
	}
	if blockers := facts.Blockers(); len(blockers) > 0 {
		return &ErasureBlockedError{Blockers: blockers}
	}
	return nil
}

// ExportAccount is the account part of a personal data export.
type ExportAccount struct {
	ID                 string
	Email              *string
	FullName           string
	PhoneNumber        *string
	Status             string
	AddressDetail      *string
	WardCode           *int32
	Roles              []string
	TermsVersion       *string
	TermsAcceptedAt    *time.Time
	AcceptsInvitations bool
	CreatedAt          time.Time
}

// ExportRoom is a room the person owns, in an export.
type ExportRoom struct {
	ID, Name, Address, Status string
	PriceVND                  int64
}

// ExportContract is a contract the person rents under, in an export.
type ExportContract struct {
	ID, RoomName, Status string
	PriceVND             int64
	StartsOn             time.Time
	EndsOn               *time.Time
}

// ExportPayment is a payment the person made, in an export.
type ExportPayment struct {
	ID, InvoiceID, Method, Status string
	AmountVND                     int64
	Note                          *string
	PaidAt                        *time.Time
	CreatedAt                     time.Time
}

// Export is a copy of the data the platform keeps about one person: no proof pictures, nothing about other people.
type Export struct {
	Account   ExportAccount
	Landlord  *Profile
	Rooms     []ExportRoom
	Contracts []ExportContract
	Payments  []ExportPayment
}
