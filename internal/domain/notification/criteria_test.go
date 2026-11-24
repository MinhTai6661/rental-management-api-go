package notification

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func intp(n int) *int { return &n }

func TestNormalizeCleansTheCriteria(t *testing.T) {
	c := Criteria{
		Roles:                 []string{"landlord", "landlord", "nobody", " tenant ", ""},
		ProvinceCodes:         []int{79, 1, 0, -3, 1},
		PlanCodes:             []string{"pro", " ", "pro", "none"},
		RegisteredWithinDays:  intp(0),
		RegisteredAtLeastDays: intp(30),
		ActiveWithinDays:      intp(500),
	}.Normalize()
	if !reflect.DeepEqual(c.Roles, []string{"landlord", "tenant"}) {
		t.Errorf("roles: %v", c.Roles)
	}
	if !reflect.DeepEqual(c.ProvinceCodes, []int{1, 79}) {
		t.Errorf("provinces: %v", c.ProvinceCodes)
	}
	if !reflect.DeepEqual(c.PlanCodes, []string{"none", "pro"}) {
		t.Errorf("plans: %v", c.PlanCodes)
	}
	if c.RegisteredWithinDays != nil {
		t.Error("a day count below one is dropped")
	}
	if c.RegisteredAtLeastDays == nil || *c.RegisteredAtLeastDays != 30 {
		t.Error("registered_at_least_days is kept as a number")
	}
	if c.ActiveWithinDays == nil || *c.ActiveWithinDays != MaxActiveDays {
		t.Errorf("active days are capped at %d: %v", MaxActiveDays, c.ActiveWithinDays)
	}
}

func TestNormalizeCapsUserIDs(t *testing.T) {
	var ids []uuid.UUID
	for range 80 {
		ids = append(ids, uuid.New())
	}
	ids = append(ids, ids[0], uuid.Nil)
	if got := (Criteria{UserIDs: ids}).Normalize().UserIDs; len(got) != MaxUserIDs || got[0] != ids[0] {
		t.Errorf("%d ids", len(got))
	}
}

func TestIsEmpty(t *testing.T) {
	if !(Criteria{}).IsEmpty() || !(Criteria{Roles: []string{"nobody"}}).Normalize().IsEmpty() {
		t.Error("nothing set is empty")
	}
	if (Criteria{ProvinceCodes: []int{1}}).IsEmpty() {
		t.Error("a province is a criterion")
	}
}

func TestStaffRules(t *testing.T) {
	if got := StaffRolesBelow("super_admin"); !reflect.DeepEqual(got, []string{"admin"}) {
		t.Errorf("super admin: %v", got)
	}
	if StaffRolesBelow("admin") != nil || StaffRolesBelow("landlord") != nil {
		t.Error("nobody else names staff")
	}
	cases := []struct {
		sender string
		roles  []string
		want   []string
	}{
		{"admin", []string{"admin"}, []string{"admin", "super_admin"}},
		{"super_admin", []string{"admin"}, []string{"super_admin"}},
		{"super_admin", []string{"super_admin"}, []string{"admin", "super_admin"}},
		{"super_admin", nil, []string{"admin", "super_admin"}},
		{"landlord", []string{"admin"}, []string{"admin", "super_admin"}},
	}
	for _, c := range cases {
		if got := ExcludedStaffRoles(c.sender, Criteria{Roles: c.roles}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: %v, want %v", c.sender, c.roles, got, c.want)
		}
	}
	if (Criteria{Roles: []string{"tenant"}}).ReachesStaff() || !(Criteria{Roles: []string{"admin"}}).ReachesStaff() {
		t.Error("ReachesStaff")
	}
}
