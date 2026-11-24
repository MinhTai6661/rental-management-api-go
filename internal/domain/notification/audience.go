package notification

import (
	"errors"

	"rental-management/pkg/authz"
)

// Audience is the side of an account a notification is for. One person can be a tenant, a landlord and a manager, and
// acts in one role at a time, so each role has its own inbox; a notification without an audience shows in all of them.
type Audience string

// The audiences.
const (
	AudienceNone     Audience = ""
	AudienceLandlord Audience = "landlord"
	AudienceTenant   Audience = "tenant"
	AudienceManager  Audience = "manager"
)

// ErrUnknownAudience is returned for an audience that is not one of the above.
var ErrUnknownAudience = errors.New("unknown notification audience")

// ParseAudience returns the audience named s; "" is AudienceNone.
func ParseAudience(s string) (Audience, error) {
	switch a := Audience(s); a {
	case AudienceNone, AudienceLandlord, AudienceTenant, AudienceManager:
		return a, nil
	}
	return AudienceNone, ErrUnknownAudience
}

// ResolveAudience is the audience a notification is stored with: the one the sender chose, else the one of its kind.
func ResolveAudience(explicit Audience, k Kind) Audience {
	if explicit != AudienceNone {
		return explicit
	}
	return k.DefaultAudience()
}

// VisibleTo says whose notifications a person sees while acting in activeRole. Acting as a tenant, landlord or manager
// shows the ones for that side and the ones for every side (filtered is true and only is that side). Acting as staff
// shows everything addressed to the person (filtered is false).
func VisibleTo(activeRole string) (only Audience, filtered bool) {
	switch activeRole {
	case authz.RoleTenant:
		return AudienceTenant, true
	case authz.RoleLandlord:
		return AudienceLandlord, true
	case authz.RoleManager:
		return AudienceManager, true
	}
	return AudienceNone, false
}
