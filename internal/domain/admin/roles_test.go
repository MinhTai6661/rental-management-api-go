package admin

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

var (
	idA = uuid.MustParse("00000000-0000-7000-8000-00000000000a")
	idB = uuid.MustParse("00000000-0000-7000-8000-00000000000b")
)

func TestCanDelete(t *testing.T) {
	super, admin := []string{authz.RoleSuperAdmin}, []string{authz.RoleAdmin}
	tests := []struct {
		name          string
		actor, target []string
		same          bool
		allowed       bool
	}{
		{"nobody deletes themselves", super, super, true, false},
		{"nobody deletes a super admin", super, super, false, false},
		{"admin cannot delete a super admin", admin, super, false, false},
		{"super admin deletes an admin", super, admin, false, true},
		{"admin deletes a landlord", admin, []string{authz.RoleLandlord, authz.RoleTenant}, false, true},
		{"admin deletes an account without roles", admin, nil, false, true},
		{"admin cannot delete an admin", admin, admin, false, false},
		{"a landlord manages nobody", []string{authz.RoleLandlord}, []string{authz.RoleTenant}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := idB
			if tc.same {
				target = idA
			}
			err := CanDelete(idA, tc.actor, target, tc.target)
			if (err == nil) != tc.allowed || (err != nil && !errors.Is(err, ErrForbidden)) {
				t.Fatalf("allowed=%v, got %v", tc.allowed, err)
			}
		})
	}
}

func TestCanChangeRoles(t *testing.T) {
	super, admin := []string{authz.RoleSuperAdmin}, []string{authz.RoleAdmin}
	tenant, landlord := []string{authz.RoleTenant}, []string{authz.RoleLandlord, authz.RoleTenant}
	tests := []struct {
		name         string
		actor        []string
		target, next []string
		self         bool
		allowed      bool
	}{
		{"not your own roles", super, tenant, tenant, true, false},
		{"nobody gives super admin", super, tenant, []string{authz.RoleSuperAdmin}, false, false},
		{"nobody changes a super admin", super, super, tenant, false, false},
		{"super admin makes an admin", super, tenant, admin, false, true},
		{"admin makes a landlord", admin, tenant, landlord, false, true},
		{"admin cannot make an admin", admin, tenant, admin, false, false},
		{"admin cannot change another admin", admin, admin, tenant, false, false},
		{"landlord cannot change roles", []string{authz.RoleLandlord}, tenant, landlord, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := idB
			if tc.self {
				target = idA
			}
			err := CanChangeRoles(idA, tc.actor, target, tc.target, tc.next)
			if (err == nil) != tc.allowed || (err != nil && !errors.Is(err, ErrForbidden)) {
				t.Fatalf("allowed=%v, got %v", tc.allowed, err)
			}
		})
	}
}

func TestValidateRoles(t *testing.T) {
	if _, err := ValidateRoles(nil); !errors.Is(err, ErrRolesEmpty) {
		t.Fatalf("empty roles: %v", err)
	}
	if _, err := ValidateRoles([]string{"wizard"}); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("unknown role: %v", err)
	}
	if _, err := ValidateRoles([]string{authz.RoleAdmin, authz.RoleTenant}); !errors.Is(err, ErrStaffRolesMixed) {
		t.Fatalf("staff with tenant: %v", err)
	}
	got, err := ValidateRoles([]string{authz.RoleTenant, authz.RoleLandlord, authz.RoleTenant})
	if err != nil || !slices.Equal(got, []string{authz.RoleLandlord, authz.RoleTenant}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestAssignableBy(t *testing.T) {
	if got := AssignableBy([]string{authz.RoleAdmin}); !slices.Equal(got, []string{authz.RoleLandlord, authz.RoleManager, authz.RoleTenant}) {
		t.Fatalf("admin: %v", got)
	}
	if got := AssignableBy([]string{authz.RoleSuperAdmin}); len(got) != 5 {
		t.Fatalf("super admin: %v", got)
	}
	if got := AssignableBy([]string{authz.RoleLandlord}); len(got) != 0 {
		t.Fatalf("landlord: %v", got)
	}
}

func TestLandlordRoleChange(t *testing.T) {
	landlord, tenant := []string{authz.RoleLandlord, authz.RoleTenant}, []string{authz.RoleTenant}
	if !LosesLandlord(landlord, tenant) || LosesLandlord(tenant, landlord) || !GainsLandlord(tenant, landlord) {
		t.Fatal("landlord role change detected wrongly")
	}
	var blocked *HasActiveContractsError
	if err := CheckRevocable(2); !errors.As(err, &blocked) || blocked.Count != 2 {
		t.Fatalf("running contracts must block: %v", err)
	}
	if err := CheckRevocable(0); err != nil {
		t.Fatal(err)
	}
}

func TestEscapeLike(t *testing.T) {
	if EscapeLike("  ") != nil {
		t.Fatal("blank search must be no search")
	}
	if got := *EscapeLike(`50%_A\b`); got != `50\%\_a\\b` {
		t.Fatalf("got %q", got)
	}
}
