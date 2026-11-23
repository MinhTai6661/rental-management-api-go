// Package invitation is the shared kernel of every kind of invitation (a room offered to a tenant now, a room handed
// to a manager later): who an invitation is for, who may answer it, which accounts can be invited and how an address
// is masked, normalized and remembered as "do not mail". It is pure and has no state and no ports; any context may
// import it, and only the invitation context holds the use cases and adapters that work with accounts.
package invitation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// MaxEmailLength is the longest address an invitation can be sent to.
const MaxEmailLength = 190

// Audience says who may answer an invitation.
type Audience string

const (
	// AudienceAccount: one account only.
	AudienceAccount Audience = "account"
	// AudienceEmail: a signed-in account whose verified email is the address.
	AudienceEmail Audience = "email"
	// AudiencePublic: whoever holds the link and is signed in.
	AudiencePublic Audience = "public"
)

// AudienceOf says who an invitation with these recipients is for: an account when one is named (even with an
// address too), else an address, else anybody.
func AudienceOf(recipientID *uuid.UUID, recipientEmail *string) Audience {
	switch {
	case recipientID != nil:
		return AudienceAccount
	case recipientEmail != nil:
		return AudienceEmail
	default:
		return AudiencePublic
	}
}

// Allows says whether the signed-in user may answer an invitation for these recipients. An address counts only when
// the user's own address is verified and equal (case does not matter).
func Allows(recipientID *uuid.UUID, recipientEmail *string, userID uuid.UUID, userEmail *string, emailVerified bool) bool {
	switch AudienceOf(recipientID, recipientEmail) {
	case AudienceAccount:
		return *recipientID == userID
	case AudienceEmail:
		return emailVerified && userEmail != nil &&
			strings.EqualFold(strings.TrimSpace(*userEmail), strings.TrimSpace(*recipientEmail))
	default:
		return true
	}
}

// LookupBy is how a landlord names the person to invite.
type LookupBy string

const (
	// LookupByEmail looks an account up by its whole email.
	LookupByEmail LookupBy = "email"
	// LookupByID looks an account up by its id.
	LookupByID LookupBy = "id"
)

// LookupBys lists the ways to look an account up.
func LookupBys() []LookupBy { return []LookupBy{LookupByEmail, LookupByID} }

// ParseLookupBy turns the text into a LookupBy.
func ParseLookupBy(s string) (LookupBy, bool) {
	for _, by := range LookupBys() {
		if string(by) == s {
			return by, true
		}
	}
	return "", false
}

// Person is an account as the invitation rules need it.
type Person struct {
	ID                 uuid.UUID
	FullName           string
	Email              *string
	EmailVerified      bool
	Active             bool
	AcceptsInvitations bool
	// Roles is every role the account holds.
	Roles []string
}

// Invitable says whether inviterID may invite the person: active, willing to be invited, somebody else, a tenant,
// and not staff.
func Invitable(p Person, inviterID uuid.UUID) bool {
	if !p.Active || !p.AcceptsInvitations || p.ID == inviterID || !slices.Contains(p.Roles, authz.RoleTenant) {
		return false
	}
	return !slices.ContainsFunc(p.Roles, authz.IsStaff)
}

// Addressee is who an invitation is for. Email is normalized; Recipient is nil when no account is named.
type Addressee struct {
	Recipient *Person
	Email     *string
}

// Audience says who may answer an invitation to this addressee.
func (a Addressee) Audience() Audience {
	var id *uuid.UUID
	if a.Recipient != nil {
		id = &a.Recipient.ID
	}
	return AudienceOf(id, a.Email)
}

// NormalizeEmail is the form an address is stored, compared and hashed in.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// MaskEmail hides most of the local part (first letter, at least two stars, the domain): enough to recognize the
// account, not to learn the address. Nil when there is nothing to show.
func MaskEmail(email *string) *string {
	if email == nil {
		return nil
	}
	local, domain, ok := strings.Cut(*email, "@")
	if !ok {
		return nil
	}
	runes := []rune(local)
	if len(runes) == 0 {
		return nil
	}
	stars := max(len(runes)-1, 2)
	masked := string(runes[0]) + strings.Repeat("*", stars) + "@" + domain
	return &masked
}

// BlockHash is what is stored to remember an address that must not be mailed: the SHA-256 of the normalized address.
func BlockHash(email string) string {
	sum := sha256.Sum256([]byte(NormalizeEmail(email)))
	return hex.EncodeToString(sum[:])
}

// The errors of resolving who to invite and of the opt-out link.
var (
	// ErrRecipientInvalid: no account can be invited with the id given.
	ErrRecipientInvalid = errors.New("no account can be invited with this id")
	// ErrOwnEmail: the address is the inviter's own.
	ErrOwnEmail = errors.New("the address is the inviter's own")
	// ErrRecipientMismatch: the id and the address name two different accounts.
	ErrRecipientMismatch = errors.New("the id and the address belong to different accounts")
	// ErrOptOutInvalid: the opt-out link does not match the address.
	ErrOptOutInvalid = errors.New("the opt-out link is not valid")
)
