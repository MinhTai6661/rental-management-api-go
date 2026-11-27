package identity

import "rental-management/pkg/authz"

// DemoAccount is an account `seed -demo` creates for development.
type DemoAccount struct {
	Email    string
	FullName string
	Roles    []string
	// HasAddress gives the account a ward (the first one of the seeded divisions).
	HasAddress bool
}

// DemoAccounts are the accounts of the development data: one for each side of the platform.
func DemoAccounts() []DemoAccount {
	return []DemoAccount{
		{Email: "superadmin@rental.test", FullName: "Demo Super Admin", Roles: []string{authz.RoleSuperAdmin}},
		{Email: "admin@rental.test", FullName: "Demo Admin", Roles: []string{authz.RoleAdmin}},
		{Email: "landlord@rental.test", FullName: "Demo Landlord", Roles: []string{authz.RoleLandlord, authz.RoleTenant}, HasAddress: true},
		{Email: "tenant@rental.test", FullName: "Demo Tenant", Roles: []string{authz.RoleTenant}, HasAddress: true},
	}
}

// SampleAccounts are the accounts of the project's sample-account sheet (docs/api-features.xlsx), for people used to
// signing in with them. They have a password of their own and hold no demo data.
func SampleAccounts() []DemoAccount {
	landlord := []string{authz.RoleLandlord, authz.RoleTenant}
	return []DemoAccount{
		{Email: "superadmin@rental.com", FullName: "Super Admin", Roles: []string{authz.RoleSuperAdmin}},
		{Email: "admin@rental.com", FullName: "Default Admin", Roles: []string{authz.RoleAdmin}},
		{Email: "landlord@rental.com", FullName: "Default Landlord", Roles: landlord, HasAddress: true},
		{Email: "landlord2@rental.com", FullName: "Second Landlord", Roles: landlord, HasAddress: true},
		{Email: "landlord3@rental.com", FullName: "Third Landlord", Roles: landlord, HasAddress: true},
		{Email: "tenant@rental.com", FullName: "Default Tenant", Roles: []string{authz.RoleTenant}, HasAddress: true},
	}
}
