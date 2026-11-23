package planhttp

import (
	"encoding/json"
	"testing"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/plan"
)

func TestUpdateRequestTellsAbsentFromNull(t *testing.T) {
	var r updatePlanRequest
	if err := json.Unmarshal([]byte(`{"name": "Starter", "max_rooms": null, "max_buildings": 7, "description": null, "features": []}`), &r); err != nil {
		t.Fatal(err)
	}
	p, err := r.patch()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name == nil || *p.Name != "Starter" || p.Code != nil || p.IsActive != nil {
		t.Errorf("name set, code and flag absent: %+v", p)
	}
	if !p.MaxRooms.Set || p.MaxRooms.Value != nil {
		t.Errorf("null clears the limit: %+v", p.MaxRooms)
	}
	if !p.MaxBuildings.Set || p.MaxBuildings.Value == nil || *p.MaxBuildings.Value != 7 {
		t.Errorf("a value sets it: %+v", p.MaxBuildings)
	}
	if p.MaxActiveContracts.Set || !p.Description.Set || p.Description.Value != nil {
		t.Errorf("absent stays, null description clears: %+v %+v", p.MaxActiveContracts, p.Description)
	}
	if p.Features == nil || len(*p.Features) != 0 {
		t.Errorf("an empty list removes every feature: %v", p.Features)
	}
}

func TestUpdateRequestChecksTheLimitsItCarries(t *testing.T) {
	var r updatePlanRequest
	if err := json.Unmarshal([]byte(`{"max_rooms": -1, "max_images_per_room": 0, "max_buildings": 2000000}`), &r); err != nil {
		t.Fatal(err)
	}
	_, err := r.patch()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindValidation || len(e.Fields) != 3 {
		t.Fatalf("err = %+v", err)
	}
	want := map[string]string{"max_rooms": apperr.RuleMinNumber, "max_images_per_room": apperr.RuleMinNumber, "max_buildings": apperr.RuleMaxNumber}
	for _, f := range e.Fields {
		if want[f.Name] != f.Rule {
			t.Errorf("%s: rule %s", f.Name, f.Rule)
		}
	}
}

func TestCreateRequestDefaultsToAnActiveMonthlyPlan(t *testing.T) {
	price := int64(0)
	in := createPlanRequest{Code: "x", Name: "X", Audience: "tenant", PriceVND: &price}.input()
	if !in.IsActive || in.IsDefault || in.Interval != domain.IntervalMonth || in.Audience != domain.AudienceTenant || in.Features == nil {
		t.Errorf("input = %+v", in)
	}
	off, year := false, "year"
	in = createPlanRequest{Code: "x", Name: "X", Audience: "landlord", PriceVND: &price, IsActive: &off, BillingInterval: &year,
		Features: []string{"vip_badge"}}.input()
	if in.IsActive || in.Interval != domain.IntervalYear || len(in.Features) != 1 || in.Features[0] != domain.FeatureVIPBadge {
		t.Errorf("input = %+v", in)
	}
}

func TestAssignRequestReadsTheEnd(t *testing.T) {
	good := "2027-01-02T03:04:05+07:00"
	end, err := assignRequest{EndsAt: &good}.endsAt()
	if err != nil || end == nil || end.Year() != 2027 {
		t.Fatalf("end = %v, %v", end, err)
	}
	if end, err := (assignRequest{}).endsAt(); err != nil || end != nil {
		t.Errorf("no end: %v, %v", end, err)
	}
	bad := "tomorrow"
	if _, err := (assignRequest{EndsAt: &bad}).endsAt(); err == nil {
		t.Error("a bad date is refused")
	}
}
