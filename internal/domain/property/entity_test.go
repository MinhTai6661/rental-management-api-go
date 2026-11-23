package property

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeCharges(t *testing.T) {
	if got, v := NormalizeCharges(nil); got != nil || v != nil {
		t.Errorf("a missing list stays missing: %v %v", got, v)
	}
	note := "  "
	got, violations := NormalizeCharges([]ChargeInput{{Name: " Wifi ", Note: &note}, {Name: "  "}})
	if got[0].Name != "Wifi" || got[0].Note != nil {
		t.Errorf("text is trimmed and a blank note dropped: %+v", got[0])
	}
	if len(violations) != 1 || violations[0].Name != "charges.1.name" || violations[0].Rule != RuleRequired {
		t.Errorf("%v", violations)
	}
}

func TestChargeSameAs(t *testing.T) {
	note := "n"
	c := Charge{Name: "A", Kind: "water", AmountVND: 5, Unit: "per_period", Order: 2, Note: &note}
	same := ChargeInput{Name: "A", Kind: "water", AmountVND: 5, Unit: "per_period", Note: &note}
	if !c.SameAs(same, 2) || c.SameAs(same, 3) {
		t.Error("the position is part of it")
	}
	same.AmountVND = 6
	if c.SameAs(same, 2) {
		t.Error("another amount")
	}
	tmpl := uuid.Must(uuid.NewV7())
	if (Charge{ChargeRow: ChargeRow{TemplateID: &tmpl}}).FollowsBuilding() != true || c.FollowsBuilding() {
		t.Error("a copy of a building charge follows the building")
	}
}

func TestRoomContentDiffers(t *testing.T) {
	area := 20.0
	base := RoomData{
		Name: "P1", RoomValues: RoomValues{AddressDetail: "x", PriceVND: 1, Area: &area, MinPeriod: 1, PeriodUnit: UnitMonth},
		Status: StatusAvailable, Inherits: []Group{GroupPrice},
	}
	if base.ContentDiffers(base) {
		t.Error("the same room")
	}
	moved := base
	moved.Status = StatusRented
	if base.ContentDiffers(moved) {
		t.Error("the status is not content a listing shows")
	}
	for name, change := range map[string]func(r *RoomData){
		"name":     func(r *RoomData) { r.Name = "P2" },
		"price":    func(r *RoomData) { r.PriceVND = 2 },
		"follows":  func(r *RoomData) { r.Inherits = nil },
		"building": func(r *RoomData) { id := uuid.Must(uuid.NewV7()); r.BuildingID = &id },
		"area":     func(r *RoomData) { r.Area = nil },
	} {
		next := base
		change(&next)
		if !base.ContentDiffers(next) {
			t.Errorf("%s change must be seen", name)
		}
	}
	if st := base.State(); st.InheritsCharges || st.InheritsMedia || !base.Follows(GroupPrice) {
		t.Errorf("%+v", st)
	}
}

func TestErrorsCarryTheirNumbers(t *testing.T) {
	var limit *BuildingLimitError
	if err := error(&BuildingLimitError{Limit: 3}); !errors.As(err, &limit) || limit.Limit != 3 || limit.Error() == "" {
		t.Error("the limit travels with the error")
	}
	v := Invalid(Violation{Name: "name", Rule: RuleRequired})
	if v.Error() != "invalid: name:required" {
		t.Errorf("%q", v.Error())
	}
}
