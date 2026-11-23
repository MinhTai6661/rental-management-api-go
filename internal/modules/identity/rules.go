package identity

import (
	"time"

	"rental-management/internal/platform/authz"
)

// The rules below are pure: they decide from values and a time, so they are tested without a database.

// refreshDecision is what to do with a refresh token that was presented.
type refreshDecision int

const (
	refreshAllow  refreshDecision = iota // rotate it
	refreshReject                        // unknown, expired or from a closed session: refuse
	refreshReuse                         // an old token used again: the family is stolen, close the session
)

// refreshState describes a stored refresh token and its session.
type refreshState struct {
	expiresAt        time.Time
	rotatedAt        *time.Time
	sessionRevokedAt *time.Time
}

// decideRefresh applies the rotation rules. A token is used once: using it a second time later than grace after
// its rotation means somebody kept a copy, so the whole session is closed. Within grace (two tabs refreshing at once)
// the second use is accepted.
func decideRefresh(s refreshState, now time.Time, grace time.Duration) refreshDecision {
	switch {
	case s.sessionRevokedAt != nil:
		return refreshReject
	case !now.Before(s.expiresAt):
		return refreshReject
	case s.rotatedAt != nil && now.Sub(*s.rotatedAt) > grace:
		return refreshReuse
	}
	return refreshAllow
}

// verificationUsable reports whether an email verification token can still be used: not used yet, not expired.
func verificationUsable(expiresAt time.Time, usedAt *time.Time, now time.Time) bool {
	return usedAt == nil && now.Before(expiresAt)
}

// resendAllowed reports whether another verification email may be sent: none was sent within the cooldown.
func resendAllowed(lastSent *time.Time, now time.Time, cooldown time.Duration) bool {
	return lastSent == nil || now.Sub(*lastSent) >= cooldown
}

// initialRole is the role a new login acts in: the one used last if the user still holds it, else the most senior.
func initialRole(held []string, lastActive string) string {
	return authz.ResolveActiveRole("", held, lastActive)
}
