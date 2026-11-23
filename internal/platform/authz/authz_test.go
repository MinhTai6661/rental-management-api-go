package authz

import "testing"

func TestResolveActiveRole(t *testing.T) {
	cases := map[string]struct {
		token string
		held  []string
		last  string
		want  string
	}{
		"token role still held":           {RoleTenant, []string{RoleLandlord, RoleTenant}, RoleLandlord, RoleTenant},
		"token role revoked, last active": {RoleAdmin, []string{RoleLandlord, RoleTenant}, RoleTenant, RoleTenant},
		"no token role, last active":      {"", []string{RoleLandlord, RoleTenant}, RoleTenant, RoleTenant},
		"last active not held, seniority": {"", []string{RoleTenant, RoleLandlord}, RoleAdmin, RoleLandlord},
		"seniority order":                 {"", []string{RoleTenant, RoleManager, RoleAdmin}, "", RoleAdmin},
		"manager before tenant":           {"", []string{RoleTenant, RoleManager}, "", RoleManager},
		"unknown role held is ignored":    {"", []string{"auditor"}, "", ""},
		"unknown token role":              {"auditor", []string{RoleTenant}, "", RoleTenant},
		"no roles":                        {RoleTenant, nil, RoleTenant, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ResolveActiveRole(tc.token, tc.held, tc.last); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func set(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

func TestHasPermission(t *testing.T) {
	cases := map[string]struct {
		granted map[string]struct{}
		perm    string
		want    bool
	}{
		"exact":                   {set("rooms.view"), "rooms.view", true},
		"other action":            {set("rooms.view"), "rooms.update", false},
		"manage covers the whole": {set("rooms.manage"), "rooms.update", true},
		"manage of another":       {set("contracts.manage"), "rooms.view", false},
		"nothing granted":         {set(), "rooms.view", false},
		"nil set":                 {nil, "rooms.view", false},
		"no dot":                  {set("stats.manage"), "stats", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HasPermission(tc.granted, tc.perm); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMixesStaffWithOthers(t *testing.T) {
	cases := map[string]struct {
		roles []string
		want  bool
	}{
		"no roles":           {nil, false},
		"admin alone":        {[]string{RoleAdmin}, false},
		"both staff roles":   {[]string{RoleAdmin, RoleSuperAdmin}, false},
		"tenant and manager": {[]string{RoleTenant, RoleManager, RoleLandlord}, false},
		"admin and tenant":   {[]string{RoleAdmin, RoleTenant}, true},
		"super and landlord": {[]string{RoleLandlord, RoleSuperAdmin}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := MixesStaffWithOthers(tc.roles); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRolesAndSeniority(t *testing.T) {
	roles := Roles()
	if len(roles) != 5 || roles[0] != RoleSuperAdmin || roles[4] != RoleTenant {
		t.Fatalf("roles = %v", roles)
	}
	for i, r := range roles {
		if got := Seniority(r); got != i+1 {
			t.Errorf("Seniority(%q) = %d, want %d", r, got, i+1)
		}
	}
	if Seniority("auditor") != 0 {
		t.Error("an unknown role has seniority 0")
	}
	roles[0] = "changed"
	if Roles()[0] != RoleSuperAdmin {
		t.Error("Roles must return a copy")
	}
}

func TestPrincipal(t *testing.T) {
	p := Principal{UserID: "u", Role: RoleAdmin, Permissions: set("users.manage")}
	if !p.Can("users.view") || p.Can("rooms.view") {
		t.Errorf("unexpected permission answers for %+v", p)
	}
	if !p.HasRole(RoleAdmin, RoleSuperAdmin) || p.HasRole(RoleTenant) {
		t.Errorf("unexpected role answers for %+v", p)
	}
}
