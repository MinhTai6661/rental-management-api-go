// Package identity owns accounts, roles, permissions and login sessions. This file is the catalog: the roles,
// the permissions and which role has which. `rental-management seed` makes the database match it.
package identity

import (
	"fmt"
	"slices"

	"rental-management/pkg/authz"
)

// Permission is one thing a role may be allowed to do. Codes are "<resource>.<action>"; "<resource>.manage"
// allows every action on the resource (see authz.HasPermission).
type Permission struct {
	Code        string
	Description string
}

// Permissions returns every permission of the platform, sorted by code.
func Permissions() []Permission {
	// Every CRUD resource has these actions plus "manage".
	resources := []string{"users", "roles", "rooms", "buildings", "contracts", "invoices", "reports"}
	resourceActions := []string{"view", "create", "update", "delete"}

	var all []Permission
	for _, resource := range resources {
		for _, action := range resourceActions {
			all = append(all, Permission{Code: resource + "." + action, Description: fmt.Sprintf("%s %s", action, resource)})
		}
		all = append(all, Permission{Code: resource + ".manage", Description: "every action on " + resource})
	}
	all = append(all,
		Permission{"users.assign_roles", "assign roles to users"},
		Permission{"contracts.request_cancel", "ask the landlord to end a contract"},
		Permission{"subscriptions.manage", "manage plans and subscriptions"},
		Permission{"announcements.send", "send announcements to all users"},
		Permission{"announcements.send_to_tenants", "send announcements to one's own tenants"},
		Permission{"payment_methods.manage", "manage the payment methods shown to tenants"},
		Permission{"managers.manage", "hand rooms over to managers"},
		Permission{"manager_invitations.respond", "answer an invitation to manage rooms"},
		Permission{"contract_invitations.respond", "answer an invitation to rent a room"},
		Permission{"stats.admin", "see platform statistics"},
		Permission{"stats.system", "see system statistics"},
		Permission{"stats.view", "see one's own statistics"},
		Permission{"audit.view", "read the audit log"},
		Permission{"landlord_applications.review", "approve or reject landlord applications"},
		Permission{"my_listings.manage", "manage one's own listings"},
		Permission{"listings.review", "approve or reject listings"},
	)
	slices.SortFunc(all, func(a, b Permission) int {
		switch {
		case a.Code < b.Code:
			return -1
		case a.Code > b.Code:
			return 1
		}
		return 0
	})
	return all
}

// RolePermissions returns the permission codes of every role, sorted.
func RolePermissions() map[string][]string {
	m := map[string][]string{
		authz.RoleSuperAdmin: {
			"users.manage", "roles.manage", "reports.manage", "subscriptions.manage", "announcements.send",
			"landlord_applications.review", "listings.review", "audit.view", "stats.admin", "stats.system",
		},
		authz.RoleAdmin: {
			"users.manage", "reports.manage", "subscriptions.manage", "announcements.send",
			"landlord_applications.review", "listings.review", "audit.view", "stats.admin",
		},
		authz.RoleLandlord: {
			"rooms.manage", "buildings.manage", "contracts.manage", "invoices.manage", "reports.manage",
			"announcements.send_to_tenants", "my_listings.manage", "managers.manage", "payment_methods.manage", "stats.view",
		},
		authz.RoleManager: {
			"rooms.view", "contracts.view", "contracts.create",
			"invoices.view", "invoices.create", "invoices.update", "invoices.delete",
		},
		authz.RoleTenant: {
			"rooms.view", "contracts.view", "contracts.create", "contracts.delete", "contracts.request_cancel",
			"invoices.view", "reports.create", "reports.view", "manager_invitations.respond", "contract_invitations.respond",
		},
	}
	for _, codes := range m {
		slices.Sort(codes)
	}
	return m
}
