package plan

import (
	"errors"
	"testing"
	"time"

	"rental-management/pkg/authz"
)

func ptr[T any](v T) *T { return &v }

func TestLimitReached(t *testing.T) {
	cases := map[string]struct {
		limit   *int
		current int
		want    bool
	}{
		"no limit lets anything through": {nil, 9999, false},
		"below the limit":                {ptr(5), 4, false},
		"at the limit":                   {ptr(5), 5, true},
		"past the limit":                 {ptr(5), 6, true},
		"nothing used":                   {ptr(5), 0, false},
		"a limit of zero allows none":    {ptr(0), 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := LimitReached(tc.limit, tc.current); got != tc.want {
				t.Errorf("LimitReached(%v, %d) = %v, want %v", tc.limit, tc.current, got, tc.want)
			}
		})
	}
}

func TestCheckAssignable(t *testing.T) {
	cases := map[string]struct {
		acting   string
		held     []string
		audience Audience
		active   bool
		want     error
	}{
		"holds the role, nobody acts":           {"", []string{"tenant", "landlord"}, AudienceLandlord, true, nil},
		"holds the role and acts in it":         {"landlord", []string{"tenant", "landlord"}, AudienceLandlord, true, nil},
		"role not held":                         {"", []string{"tenant"}, AudienceLandlord, true, ErrWrongAudience},
		"acting as the other side":              {"tenant", []string{"tenant", "landlord"}, AudienceLandlord, true, ErrWrongAudience},
		"plan no longer offered":                {"landlord", []string{"landlord"}, AudienceLandlord, false, ErrPlanInactive},
		"staff holds neither side":              {"admin", []string{"admin"}, AudienceTenant, true, ErrWrongAudience},
		"a manager may take a tenant plan":      {"manager", []string{"manager", "tenant"}, AudienceTenant, true, nil},
		"wrong audience wins over an inactive":  {"", []string{"tenant"}, AudienceLandlord, false, ErrWrongAudience},
		"landlord acting as manager is let by":  {"manager", []string{"landlord", "manager"}, AudienceLandlord, true, nil},
		"a tenant plan for a landlord-only one": {"", []string{"landlord"}, AudienceTenant, true, ErrWrongAudience},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := CheckAssignable(tc.acting, tc.held, tc.audience, tc.active); !errors.Is(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectivePlanFailsOpen(t *testing.T) {
	running, def := &Plan{Code: "pro"}, &Plan{Code: "free"}
	if got := EffectivePlan(running, def); got != running {
		t.Error("a running subscription wins")
	}
	if got := EffectivePlan(nil, def); got != def {
		t.Error("without a subscription the default applies")
	}
	if got := EffectivePlan(nil, nil); got != nil {
		t.Error("no plan at all stays nil, which means nothing is limited")
	}
}

func TestAudienceOfRole(t *testing.T) {
	for role, want := range map[string]Audience{
		authz.RoleLandlord: AudienceLandlord, authz.RoleTenant: AudienceTenant, authz.RoleManager: AudienceTenant,
		authz.RoleAdmin: AudienceTenant, "": AudienceTenant,
	} {
		if got := AudienceOfRole(role); got != want {
			t.Errorf("AudienceOfRole(%q) = %s, want %s", role, got, want)
		}
	}
}

func TestIntervalEndsAfterNeverOverflows(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 9, 30, 0, 0, time.UTC) }
	cases := []struct {
		name     string
		interval Interval
		start    time.Time
		want     time.Time
	}{
		{"a month", IntervalMonth, at(2026, time.March, 10), at(2026, time.April, 10)},
		{"31 Jan + month", IntervalMonth, at(2026, time.January, 31), at(2026, time.February, 28)},
		{"31 Jan + month in a leap year", IntervalMonth, at(2028, time.January, 31), at(2028, time.February, 29)},
		{"december rolls into the next year", IntervalMonth, at(2026, time.December, 15), at(2027, time.January, 15)},
		{"a year", IntervalYear, at(2026, time.June, 5), at(2027, time.June, 5)},
		{"29 Feb + year", IntervalYear, at(2028, time.February, 29), at(2029, time.February, 28)},
		{"30 Nov + month", IntervalMonth, at(2026, time.November, 30), at(2026, time.December, 30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.interval.EndsAfter(tc.start); !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEndOf(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	free := Plan{PriceVND: 0, Interval: IntervalMonth}
	paid := Plan{PriceVND: 199_000, Interval: IntervalMonth}
	explicit := start.AddDate(0, 0, 3)

	if got := EndOf(free, start, nil); got != nil {
		t.Errorf("a free plan never ends, got %v", got)
	}
	if got := EndOf(paid, start, nil); got == nil || !got.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a paid plan lasts one interval, got %v", got)
	}
	if got := EndOf(paid, start, &explicit); got == nil || !got.Equal(explicit) {
		t.Errorf("an explicit end wins, got %v", got)
	}
	if got := EndOf(free, start, &explicit); got == nil || !got.Equal(explicit) {
		t.Errorf("an explicit end wins on a free plan too, got %v", got)
	}
}

func TestCheckEnd(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	later, same, earlier := now.Add(time.Hour), now, now.Add(-time.Hour)
	if err := CheckEnd(now, nil); err != nil {
		t.Errorf("no end is fine: %v", err)
	}
	if err := CheckEnd(now, &later); err != nil {
		t.Errorf("a later end is fine: %v", err)
	}
	for _, end := range []*time.Time{&same, &earlier} {
		if err := CheckEnd(now, end); !errors.Is(err, ErrEndInPast) {
			t.Errorf("end %s must be refused, got %v", end, err)
		}
	}
}

func TestSubscriptionRunningAt(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := map[string]struct {
		s    Subscription
		want bool
	}{
		"active, no end":       {Subscription{Status: StatusActive, StartsAt: past}, true},
		"active, not yet over": {Subscription{Status: StatusActive, StartsAt: past, EndsAt: &future}, true},
		"active but lapsed":    {Subscription{Status: StatusActive, StartsAt: past, EndsAt: &past}, false},
		"ends exactly now":     {Subscription{Status: StatusActive, StartsAt: past, EndsAt: &now}, false},
		"not started":          {Subscription{Status: StatusActive, StartsAt: future}, false},
		"pending":              {Subscription{Status: StatusPending, StartsAt: past}, false},
		"canceled":             {Subscription{Status: StatusCanceled, StartsAt: past}, false},
		"expired":              {Subscription{Status: StatusExpired, StartsAt: past}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.s.RunningAt(now); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidCode(t *testing.T) {
	for code, want := range map[string]bool{
		"landlord_pro": true, "a": true, "plan_2": true, "": false, "Bad Code": false, "Upper": false, "dash-ed": false,
		"this_code_is_way_too_long_to_be_accepted_by_the_catalog_ok": false,
	} {
		if got := ValidCode(code); got != want {
			t.Errorf("ValidCode(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestParsers(t *testing.T) {
	if _, ok := ParseAudience("admin"); ok {
		t.Error("admin is not an audience")
	}
	if a, ok := ParseAudience("tenant"); !ok || a != AudienceTenant {
		t.Error("tenant parses")
	}
	if _, ok := ParseInterval("week"); ok {
		t.Error("week is not an interval")
	}
	if _, ok := ParseFeature("teleport"); ok {
		t.Error("unknown feature")
	}
	for _, f := range Features() {
		if _, ok := ParseFeature(string(f)); !ok {
			t.Errorf("%s must parse", f)
		}
	}
	if _, ok := ParseStatus("deleted"); ok {
		t.Error("deleted is not a status")
	}
}

func TestBaseCatalog(t *testing.T) {
	defaults := map[Audience]int{}
	codes := map[string]bool{}
	for _, p := range Base() {
		if codes[p.Code] || !ValidCode(p.Code) {
			t.Errorf("code %q is duplicated or invalid", p.Code)
		}
		codes[p.Code] = true
		if p.IsDefault {
			defaults[p.Audience]++
			if !p.IsFree() {
				t.Errorf("default plan %s must be free", p.Code)
			}
		}
		for _, f := range p.Features {
			if _, ok := ParseFeature(string(f)); !ok {
				t.Errorf("%s has unknown feature %s", p.Code, f)
			}
		}
	}
	if len(codes) != 5 || defaults[AudienceLandlord] != 1 || defaults[AudienceTenant] != 1 {
		t.Errorf("catalog = %d plans, defaults %v", len(codes), defaults)
	}
	for _, d := range DemoAssignments() {
		if !codes[d.PlanCode] {
			t.Errorf("demo plan %s is not in the catalog", d.PlanCode)
		}
	}
}
