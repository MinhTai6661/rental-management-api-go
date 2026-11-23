// Package authz holds the pure authorization rules: roles and their seniority, which roles may be held together,
// how the role of a request is chosen and how permissions are checked. It imports nothing but the standard library,
// so every other package (HTTP layer, identity, modules) can depend on it.
package authz

import (
	"strings"
	"time"
)

// Role codes.
const (
	RoleSuperAdmin = "super_admin"
	RoleAdmin      = "admin"
	RoleLandlord   = "landlord"
	RoleManager    = "manager"
	RoleTenant     = "tenant"
)

// roleOrder lists the roles from the most senior to the least; the index plus one is the seniority.
var roleOrder = [...]string{RoleSuperAdmin, RoleAdmin, RoleLandlord, RoleManager, RoleTenant}

// Roles returns every role code, most senior first.
func Roles() []string { return append([]string(nil), roleOrder[:]...) }

// Seniority is 1 (super admin) to 5 (tenant); 0 means the role is unknown.
func Seniority(role string) int {
	for i, r := range roleOrder {
		if r == role {
			return i + 1
		}
	}
	return 0
}

// IsStaff reports whether the role runs the platform (admin, super admin) rather than renting or letting rooms.
func IsStaff(role string) bool { return role == RoleSuperAdmin || role == RoleAdmin }

// MixesStaffWithOthers reports whether the roles put a staff role together with another kind of role.
// Staff accounts stay apart from the renter and landlord sides.
func MixesStaffWithOthers(roles []string) bool {
	hasStaff, hasOther := false, false
	for _, role := range roles {
		if IsStaff(role) {
			hasStaff = true
		} else {
			hasOther = true
		}
	}
	return hasStaff && hasOther
}

// StatusActive is the only account status that may use the API.
const StatusActive = "active"

// ResolveActiveRole is the role a request acts in. A request is made in exactly one role, even when the user holds
// several: the role named by the token if the user still holds it, otherwise the role used last if still held,
// otherwise the most senior role held. "" means the user holds no known role at all.
func ResolveActiveRole(tokenRole string, held []string, lastActive string) string {
	has := func(role string) bool {
		if role == "" {
			return false
		}
		for _, h := range held {
			if h == role {
				return true
			}
		}
		return false
	}
	switch {
	case has(tokenRole):
		return tokenRole
	case has(lastActive):
		return lastActive
	}
	for _, role := range roleOrder {
		if has(role) {
			return role
		}
	}
	return ""
}

// HasPermission reports whether the granted permissions allow perm. "<resource>.manage" allows every
// "<resource>.<action>" (so "rooms.manage" allows "rooms.view").
func HasPermission(granted map[string]struct{}, perm string) bool {
	if _, ok := granted[perm]; ok {
		return true
	}
	resource, _, _ := strings.Cut(perm, ".")
	_, ok := granted[resource+".manage"]
	return ok
}

// Principal is the authenticated caller of one request.
type Principal struct {
	UserID    string // user uuid
	SessionID string // login session uuid (the token's sid)
	// Role is the role this request acts in ("" when the user holds none).
	Role string
	// Permissions are the permissions of Role. The set is shared between requests: never modify it.
	Permissions map[string]struct{}
	// TokenID is the jti of the access token of the request and TokenExpiresAt its expiry (to revoke it on logout).
	TokenID        string
	TokenExpiresAt time.Time
}

// Can reports whether the principal may perform the permission.
func (p Principal) Can(perm string) bool { return HasPermission(p.Permissions, perm) }

// HasRole reports whether the request acts in one of the roles.
func (p Principal) HasRole(roles ...string) bool {
	for _, r := range roles {
		if p.Role == r {
			return true
		}
	}
	return false
}
