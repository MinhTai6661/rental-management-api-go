package identity

import (
	"regexp"
	"testing"

	"rental-management/pkg/authz"
)

// permissionCode is the same shape the database enforces with a CHECK constraint.
var permissionCode = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)

func TestCatalogPermissionsAreWellFormedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Permissions() {
		if !permissionCode.MatchString(p.Code) {
			t.Errorf("permission code %q does not match resource.action", p.Code)
		}
		if p.Description == "" {
			t.Errorf("permission %q has no description", p.Code)
		}
		if seen[p.Code] {
			t.Errorf("permission %q is listed twice", p.Code)
		}
		seen[p.Code] = true
	}
}

func TestEveryRoleHasPermissionsAndTheyExist(t *testing.T) {
	known := map[string]bool{}
	for _, p := range Permissions() {
		known[p.Code] = true
	}
	granted := RolePermissions()
	for _, role := range authz.Roles() {
		if len(granted[role]) == 0 {
			t.Errorf("role %s has no permissions", role)
		}
		for _, code := range granted[role] {
			if !known[code] {
				t.Errorf("role %s is granted unknown permission %q", role, code)
			}
		}
	}
	if len(granted) != len(authz.Roles()) {
		t.Errorf("catalog has permissions for %d roles, authz knows %d", len(granted), len(authz.Roles()))
	}
}

// The roles must not overlap in ways that break the business rules: staff cannot manage rooms or contracts.
func TestStaffRolesCannotRunRentals(t *testing.T) {
	granted := RolePermissions()
	for _, role := range []string{authz.RoleSuperAdmin, authz.RoleAdmin} {
		set := map[string]struct{}{}
		for _, code := range granted[role] {
			set[code] = struct{}{}
		}
		for _, perm := range []string{"rooms.view", "contracts.view", "invoices.view", "buildings.view"} {
			if authz.HasPermission(set, perm) {
				t.Errorf("%s must not have %s", role, perm)
			}
		}
	}
}

func TestOnlySuperAdminManagesRoles(t *testing.T) {
	for role, codes := range RolePermissions() {
		has := false
		for _, c := range codes {
			if c == "roles.manage" {
				has = true
			}
		}
		if has != (role == authz.RoleSuperAdmin) {
			t.Errorf("role %s: roles.manage = %v", role, has)
		}
	}
}

// The contract routes ask for these permissions (see adapter/http/contract/routes.go): landlord and tenant must hold
// exactly what the Laravel RoleSeeder gives them, and nobody else may.
func TestContractPermissionsFollowTheRoles(t *testing.T) {
	granted := RolePermissions()
	set := func(role string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, code := range granted[role] {
			out[code] = struct{}{}
		}
		return out
	}
	landlord, tenant := set(authz.RoleLandlord), set(authz.RoleTenant)
	for _, perm := range []string{"contracts.view", "contracts.create", "contracts.update", "contracts.delete"} {
		if !authz.HasPermission(landlord, perm) {
			t.Errorf("landlord must have %s", perm)
		}
	}
	for _, perm := range []string{"contracts.view", "contracts.create", "contracts.delete", "contracts.request_cancel"} {
		if !authz.HasPermission(tenant, perm) {
			t.Errorf("tenant must have %s", perm)
		}
	}
	// A tenant never edits a contract; the routes of the landlord check the role as well.
	if authz.HasPermission(tenant, "contracts.update") {
		t.Error("tenant must not have contracts.update")
	}
}

// The plan routes ask for subscriptions.manage (see adapter/http/plan/routes.go); the routes of the catalog check the
// super admin role on top. Laravel's RoleSeeder gives it to the two staff roles and to nobody else: a landlord's or a
// tenant's own plan needs no permission.
func TestPlanPermissionFollowsTheStaffRoles(t *testing.T) {
	granted := RolePermissions()
	holders := map[string]bool{}
	for role, codes := range granted {
		for _, code := range codes {
			if code == "subscriptions.manage" {
				holders[role] = true
			}
		}
	}
	if len(holders) != 2 || !holders[authz.RoleAdmin] || !holders[authz.RoleSuperAdmin] {
		t.Errorf("subscriptions.manage is held by %v, want admin and super_admin only", holders)
	}
}
