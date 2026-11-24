package admin

import (
	"slices"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// Who may manage whom. A staff account holds one staff role (a staff role never mixes with another), so the roles
// of the actor are the roles of the account.

// manageable lists the roles each role may manage; a role that is not listed manages nobody. The empty string stands
// for an account that holds no role.
var manageable = map[string][]string{
	authz.RoleAdmin: {authz.RoleLandlord, authz.RoleManager, authz.RoleTenant, ""},
}

// assignable lists the roles each role may give to an account.
var assignable = map[string][]string{
	authz.RoleAdmin: {authz.RoleLandlord, authz.RoleManager, authz.RoleTenant},
}

// CanManage reports whether every role of the actor allows every role of the target (no role counts as "").
func CanManage(actor, target []string) bool {
	if len(target) == 0 {
		target = []string{""}
	}
	if len(actor) == 0 {
		return false
	}
	for _, a := range actor {
		allowed := manageable[a]
		for _, t := range target {
			if !slices.Contains(allowed, t) {
				return false
			}
		}
	}
	return true
}

// AssignableBy returns the roles the actor may give, most senior first: every role for a super admin, the
// landlord, manager and tenant roles for an admin, none for anybody else.
func AssignableBy(actor []string) []string {
	if slices.Contains(actor, authz.RoleSuperAdmin) {
		return authz.Roles()
	}
	var out []string
	for _, role := range authz.Roles() {
		ok := len(actor) > 0
		for _, a := range actor {
			ok = ok && slices.Contains(assignable[a], role)
		}
		if ok {
			out = append(out, role)
		}
	}
	return out
}

// SeesStaff reports whether the actor sees the accounts of staff in lists: only a super admin does.
func SeesStaff(actor []string) bool { return slices.Contains(actor, authz.RoleSuperAdmin) }

// CanDelete decides whether the actor may delete the target account. Nobody deletes themselves or a super admin; a
// super admin deletes anybody else; an admin only deletes what CanManage lets them.
func CanDelete(actorID uuid.UUID, actor []string, targetID uuid.UUID, target []string) error {
	switch {
	case actorID == targetID, slices.Contains(target, authz.RoleSuperAdmin):
		return ErrForbidden
	case slices.Contains(actor, authz.RoleSuperAdmin):
		return nil
	case CanManage(actor, target):
		return nil
	}
	return ErrForbidden
}

// CanChangeRoles decides whether the actor may give the target the new roles. Nobody changes their own roles, nobody
// gives the super admin role and nobody changes a super admin's; a super admin may change anybody else's; an admin
// only changes accounts and roles CanManage lets them.
func CanChangeRoles(actorID uuid.UUID, actor []string, targetID uuid.UUID, target, next []string) error {
	switch {
	case actorID == targetID, slices.Contains(next, authz.RoleSuperAdmin), slices.Contains(target, authz.RoleSuperAdmin):
		return ErrForbidden
	case slices.Contains(actor, authz.RoleSuperAdmin):
		return nil
	case CanManage(actor, target) && CanManage(actor, next):
		return nil
	}
	return ErrForbidden
}

// ValidateRoles checks the roles an account is to hold: at least one, all known, and a staff role never together with
// another kind. It returns the roles sorted most senior first and without duplicates.
func ValidateRoles(roles []string) ([]string, error) {
	if len(roles) == 0 {
		return nil, ErrRolesEmpty
	}
	seen := make(map[string]struct{}, len(roles))
	var out []string
	for _, r := range roles {
		if authz.Seniority(r) == 0 {
			return nil, ErrUnknownRole
		}
		if _, dup := seen[r]; !dup {
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	if authz.MixesStaffWithOthers(out) {
		return nil, ErrStaffRolesMixed
	}
	slices.SortFunc(out, func(a, b string) int { return authz.Seniority(a) - authz.Seniority(b) })
	return out, nil
}

// LosesLandlord reports whether the change takes the landlord role away.
func LosesLandlord(before, after []string) bool {
	return slices.Contains(before, authz.RoleLandlord) && !slices.Contains(after, authz.RoleLandlord)
}

// GainsLandlord reports whether the change gives the landlord role.
func GainsLandlord(before, after []string) bool {
	return !slices.Contains(before, authz.RoleLandlord) && slices.Contains(after, authz.RoleLandlord)
}

// CheckRevocable refuses to take the landlord role away while contracts still run.
func CheckRevocable(activeContracts int) error {
	if activeContracts > 0 {
		return &HasActiveContractsError{Count: activeContracts}
	}
	return nil
}
