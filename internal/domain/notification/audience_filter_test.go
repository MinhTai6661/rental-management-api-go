package notification

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	now      = time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	admin    = Sender{UserID: uuid.New(), Role: "admin"}
	super    = Sender{UserID: uuid.New(), Role: "super_admin"}
	landlord = Sender{UserID: uuid.New(), Role: "landlord"}
)

func TestResolveFilterScopesPerSender(t *testing.T) {
	cases := []struct {
		scope  Scope
		sender Sender
		want   string // "" = refused
	}{
		{ScopeEveryone, admin, "all"}, {ScopeLandlords, admin, "landlords"}, {ScopeTenants, super, "tenants"}, {ScopeCustom, super, "all"},
		{ScopeMyTenants, landlord, "my_tenants"}, {ScopeMyPeople, landlord, "my_tenants"}, {ScopeSelectedPeople, landlord, "picked"},
		{ScopeMyTenants, admin, ""}, {ScopeEveryone, landlord, ""}, {ScopeCustom, landlord, ""},
		{ScopeEveryone, Sender{Role: "tenant"}, ""},
	}
	for _, c := range cases {
		crit := Criteria{}
		if c.scope == ScopeCustom {
			crit.ProvinceCodes = []int{1}
		}
		f, err := ResolveFilter(c.scope, c.sender, crit, nil, now)
		if c.want == "" {
			if !errors.Is(err, ErrScopeNotAllowed) {
				t.Errorf("%s by %s: %v", c.scope, c.sender.Role, err)
			}
			continue
		}
		if err != nil || f.Scope != c.want {
			t.Errorf("%s by %s: %q %v", c.scope, c.sender.Role, f.Scope, err)
		}
	}
}

func TestResolveFilterCustomNeedsCriteria(t *testing.T) {
	if _, err := ResolveFilter(ScopeCustom, admin, Criteria{Roles: []string{"nobody"}}, nil, now); !errors.Is(err, ErrAudienceEmpty) {
		t.Errorf("%v", err)
	}
}

func TestResolveFilterCriteriaOnlyForEveryoneAndCustom(t *testing.T) {
	crit := Criteria{ProvinceCodes: []int{1}, Roles: []string{"tenant"}}
	f, _ := ResolveFilter(ScopeLandlords, admin, crit, nil, now)
	if f.Roles != nil || f.ProvinceCodes != nil {
		t.Errorf("landlords ignores criteria: %+v", f)
	}
	f, _ = ResolveFilter(ScopeMyTenants, landlord, crit, nil, now)
	if f.Roles != nil || f.ProvinceCodes != nil || f.LandlordID == nil || *f.LandlordID != landlord.UserID {
		t.Errorf("landlord scopes ignore criteria and carry the landlord: %+v", f)
	}
	f, _ = ResolveFilter(ScopeEveryone, admin, crit, nil, now)
	if !reflect.DeepEqual(f.Roles, []string{"tenant"}) || !reflect.DeepEqual(f.ProvinceCodes, []int{1}) {
		t.Errorf("everyone applies them: %+v", f)
	}
}

func TestResolveFilterStaffSeniority(t *testing.T) {
	staff := []string{"admin", "super_admin"}
	cases := []struct {
		name   string
		scope  Scope
		sender Sender
		crit   Criteria
		want   []string
	}{
		{"admin everyone", ScopeEveryone, admin, Criteria{}, staff},
		{"super admin everyone", ScopeEveryone, super, Criteria{}, staff},
		{"landlord", ScopeMyTenants, landlord, Criteria{}, staff},
		{"super names admin", ScopeCustom, super, Criteria{Roles: []string{"admin"}}, []string{"super_admin"}},
		{"admin names admin", ScopeCustom, admin, Criteria{Roles: []string{"admin"}}, staff},
	}
	for _, c := range cases {
		f, err := ResolveFilter(c.scope, c.sender, c.crit, nil, now)
		if err != nil || !reflect.DeepEqual(f.ExcludedRoles, c.want) {
			t.Errorf("%s: %v %v", c.name, f.ExcludedRoles, err)
		}
	}
}

func TestResolveFilterDatesAndPlans(t *testing.T) {
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f, err := ResolveFilter(ScopeCustom, admin, Criteria{
		RegisteredWithinDays: intp(14), RegisteredAtLeastDays: intp(3), ActiveWithinDays: intp(7),
		RegisteredBefore: &before, PlanCodes: []string{"pro", "none"},
	}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !f.CreatedFrom.Equal(now.AddDate(0, 0, -14)) || !f.CreatedUntil.Equal(now.AddDate(0, 0, -3)) || !f.ActiveSince.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("%v %v %v", f.CreatedFrom, f.CreatedUntil, f.ActiveSince)
	}
	if !f.CreatedBefore.Equal(before) || !reflect.DeepEqual(f.PlanCodes, []string{"pro"}) || !f.PlanNone {
		t.Errorf("%v %v %v", f.CreatedBefore, f.PlanCodes, f.PlanNone)
	}
}

func TestResolveFilterPicked(t *testing.T) {
	id := uuid.New()
	f, _ := ResolveFilter(ScopeSelectedPeople, landlord, Criteria{}, []uuid.UUID{id}, now)
	if f.Scope != "picked" || len(f.Picked) != 1 || f.Picked[0] != id {
		t.Errorf("%+v", f)
	}
}
