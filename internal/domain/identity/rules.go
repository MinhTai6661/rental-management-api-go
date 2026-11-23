package identity

import (
	"fmt"
	"strings"
	"time"

	"rental-management/pkg/authz"
)

// The rules below are pure: they decide from values and a time, so they are tested without a database.

// RefreshDecision is what to do with a refresh token that was presented.
type RefreshDecision int

const (
	RefreshAllow  RefreshDecision = iota // rotate it
	RefreshReject                        // unknown, expired or from a closed session: refuse
	RefreshReuse                         // an old token used again: the family is stolen, close the session
)

// RefreshState describes a stored refresh token and its session.
type RefreshState struct {
	ExpiresAt        time.Time
	RotatedAt        *time.Time
	SessionRevokedAt *time.Time
}

// DecideRefresh applies the rotation rules. A token is used once: using it a second time later than grace after
// its rotation means somebody kept a copy, so the whole session is closed. Within grace (two tabs refreshing at once)
// the second use is accepted.
func DecideRefresh(s RefreshState, now time.Time, grace time.Duration) RefreshDecision {
	switch {
	case s.SessionRevokedAt != nil:
		return RefreshReject
	case !now.Before(s.ExpiresAt):
		return RefreshReject
	case s.RotatedAt != nil && now.Sub(*s.RotatedAt) > grace:
		return RefreshReuse
	}
	return RefreshAllow
}

// VerificationUsable reports whether an email verification token can still be used: not used yet, not expired.
func VerificationUsable(expiresAt time.Time, usedAt *time.Time, now time.Time) bool {
	return usedAt == nil && now.Before(expiresAt)
}

// ResendAllowed reports whether another verification email may be sent: none was sent within the cooldown.
func ResendAllowed(lastSent *time.Time, now time.Time, cooldown time.Duration) bool {
	return lastSent == nil || now.Sub(*lastSent) >= cooldown
}

// InitialRole is the role a new login acts in: the one used last if the user still holds it, else the most senior.
func InitialRole(held []string, lastActive string) string {
	return authz.ResolveActiveRole("", held, lastActive)
}

// NormalizeEmail is the form an email is stored and looked up in: trimmed and lower case.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// HasStaffRole reports whether any of the roles is a staff role.
func HasStaffRole(roles []string) bool {
	for _, r := range roles {
		if authz.IsStaff(r) {
			return true
		}
	}
	return false
}

// ValidateRoleGrant checks that the roles may be given to one account: every role must exist, and a staff role is
// never put together with another kind of role.
func ValidateRoleGrant(roles []string) error {
	for _, role := range roles {
		if authz.Seniority(role) == 0 {
			return fmt.Errorf("%w %q", ErrUnknownRole, role)
		}
	}
	if authz.MixesStaffWithOthers(roles) {
		return ErrStaffRolesMixed
	}
	return nil
}
