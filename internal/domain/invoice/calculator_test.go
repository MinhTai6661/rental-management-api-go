package invoice

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func charge(unit string, name string, amount int64) Charge {
	return Charge{ID: uuid.Must(uuid.NewV7()), Name: name, Kind: "other", Unit: unit, AmountVND: amount}
}

func amounts(items []Item) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.AmountVND
	}
	return out
}

func violationOf(t *testing.T, err error) *RuleViolation {
	t.Helper()
	var v *RuleViolation
	if !errors.As(err, &v) {
		t.Fatalf("want a rule violation, got %v", err)
	}
	return v
}

func TestLinesAddRentPeriodPersonAndOneTimeAndTotalThem(t *testing.T) {
	charges := []Charge{charge(UnitPerPeriod, "Internet", 100), charge(UnitPerPerson, "Garbage", 20), charge(UnitOneTime, "Setup", 500)}
	items, total := Lines(LineInput{Units: 1, Occupants: 2, RentUnitPriceVND: 1000, RentDescription: "Rent", Charges: charges})
	if !reflect.DeepEqual(amounts(items), []int64{1000, 100, 40, 500}) || total != 1640 {
		t.Fatalf("amounts %v total %d", amounts(items), total)
	}
	for i, it := range items {
		if it.DisplayOrder != i+1 {
			t.Errorf("display order of %d is %d", i, it.DisplayOrder)
		}
	}
	if items[0].Kind != KindRent || items[0].Description != "Rent" {
		t.Errorf("rent line: %+v", items[0])
	}
}

func TestLinesLeaveTheOneTimeFeeOffEveryBillButTheFirst(t *testing.T) {
	items, _ := Lines(LineInput{Units: 1, PeriodIndex: 2, Occupants: 1, RentUnitPriceVND: 1000, Charges: []Charge{charge(UnitOneTime, "Setup", 500)}})
	if len(items) != 1 {
		t.Fatalf("items: %+v", items)
	}
}

func TestLinesMultiplyChargesByTheUnitsOfAPeriodCountedInDays(t *testing.T) {
	items, total := Lines(LineInput{Units: 10, Occupants: 3, RentUnitPriceVND: 50, Charges: []Charge{charge(UnitPerPerson, "Garbage", 2)}})
	if items[1].Quantity != 30 || total != 560 {
		t.Fatalf("quantity %v total %d", items[1].Quantity, total)
	}
}

func TestMeteredChargesAreBilledByConsumptionWithTheIndexRangeInTheDescription(t *testing.T) {
	electricity := charge(UnitPerKWh, "Electricity", 4)
	charges := []Charge{electricity}
	used, err := Consumptions([]ReadingInput{{ChargeID: electricity.ID, Value: 150}}, charges, map[uuid.UUID]float64{electricity.ID: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 1 || used[0].Used != 50 || used[0].UnitPriceVND != 4 || used[0].Previous != 100 {
		t.Fatalf("consumption: %+v", used)
	}
	items, total := Lines(LineInput{Units: 1, PeriodIndex: 1, Occupants: 1, RentUnitPriceVND: 1000, Charges: charges, Consumption: used})
	if items[1].Description != "Electricity (100 → 150)" || total != 1200 || items[1].Quantity != 50 {
		t.Fatalf("items %+v total %d", items, total)
	}
	recorded := Readings(used)
	if len(recorded) != 1 || recorded[0].Value != 150 || recorded[0].Consumption != 50 || recorded[0].ChargeID != electricity.ID {
		t.Errorf("readings: %+v", recorded)
	}
}

func TestTheLandlordCanGiveThePreviousIndexAndThePriceOfAReading(t *testing.T) {
	water := charge(UnitPerCubicMeter, "Water", 10)
	previous, price := 10.0, int64(12)
	used, err := Consumptions([]ReadingInput{{ChargeID: water.ID, Value: 30, Previous: &previous, UnitPriceVND: &price}}, []Charge{water},
		map[uuid.UUID]float64{water.ID: 25})
	if err != nil || used[0].Previous != 10 || used[0].Used != 20 || used[0].UnitPriceVND != 12 {
		t.Fatalf("got %+v, %v", used, err)
	}
}

func TestAMeteredChargeThatUsedNothingHasNoLine(t *testing.T) {
	electricity := charge(UnitPerKWh, "Electricity", 4)
	used, err := Consumptions([]ReadingInput{{ChargeID: electricity.ID, Value: 100}}, []Charge{electricity}, map[uuid.UUID]float64{electricity.ID: 100})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := Lines(LineInput{Units: 1, PeriodIndex: 1, Occupants: 1, RentUnitPriceVND: 1000, Charges: []Charge{electricity}, Consumption: used})
	if len(items) != 1 {
		t.Fatalf("items: %+v", items)
	}
}

func TestMeteredChargesWithoutAnyReadingHaveNoLine(t *testing.T) {
	// The automatic bill carries the rent and the fixed charges only.
	items, total := Lines(LineInput{Units: 1, Occupants: 1, RentUnitPriceVND: 1000, Charges: []Charge{charge(UnitPerKWh, "Electricity", 4)}})
	if len(items) != 1 || total != 1000 {
		t.Fatalf("items %+v total %d", items, total)
	}
}

func TestLinesAddExtraLines(t *testing.T) {
	items, total := Lines(LineInput{Units: 1, PeriodIndex: 1, Occupants: 1, RentUnitPriceVND: 1000, Extras: []Extra{{Description: "Repair", AmountVND: 55_500}}})
	if items[1].Kind != KindOther || items[1].Description != "Repair" || items[1].AmountVND != 55_500 || total != 56_500 {
		t.Fatalf("items %+v total %d", items, total)
	}
}

func TestLinesRoundTheAmountToWholeVND(t *testing.T) {
	electricity := charge(UnitPerKWh, "Electricity", 3500)
	used, _ := Consumptions([]ReadingInput{{ChargeID: electricity.ID, Value: 1123.45}}, []Charge{electricity}, map[uuid.UUID]float64{electricity.ID: 1000})
	items, _ := Lines(LineInput{Units: 1, Occupants: 1, Charges: []Charge{electricity}, Consumption: used})
	if items[1].Quantity != 123.45 || items[1].AmountVND != 432_075 {
		t.Fatalf("item: %+v", items[1])
	}
}

func TestAReadingForAChargeThatIsNotMeteredIsRefused(t *testing.T) {
	internet := charge(UnitPerPeriod, "Internet", 100)
	_, err := Consumptions([]ReadingInput{{ChargeID: internet.ID, Value: 5}}, []Charge{internet}, nil)
	if v := violationOf(t, err); v.Reason != ReasonUnknownService || v.Field != FieldReadings {
		t.Fatalf("violation: %+v", v)
	}
	// Nor is a charge that does not exist.
	if _, err := Consumptions([]ReadingInput{{ChargeID: uuid.New(), Value: 5}}, nil, nil); err == nil {
		t.Fatal("an unknown charge must be refused")
	}
}

func TestAFirstReadingNeedsItsPreviousIndex(t *testing.T) {
	water := charge(UnitPerCubicMeter, "Water", 10)
	_, err := Consumptions([]ReadingInput{{ChargeID: water.ID, Value: 5}}, []Charge{water}, nil)
	v := violationOf(t, err)
	if v.Reason != ReasonPreviousReadingRequired || v.Field != FieldReadings || v.Params["service"] != "Water" {
		t.Fatalf("violation: %+v", v)
	}
}

func TestAnIndexLowerThanThePreviousOneIsRefused(t *testing.T) {
	water := charge(UnitPerCubicMeter, "Water", 10)
	_, err := Consumptions([]ReadingInput{{ChargeID: water.ID, Value: 5}}, []Charge{water}, map[uuid.UUID]float64{water.ID: 9})
	v := violationOf(t, err)
	if v.Reason != ReasonReadingLowerThanPrevious || v.Params["previous"] != "9" || v.Params["service"] != "Water" {
		t.Fatalf("violation: %+v", v)
	}
	// The index may stay the same.
	if _, err := Consumptions([]ReadingInput{{ChargeID: water.ID, Value: 9}}, []Charge{water}, map[uuid.UUID]float64{water.ID: 9}); err != nil {
		t.Errorf("an equal index is fine: %v", err)
	}
}

func TestCarryOverKeepsThePreviousIndexOfAMeterLeftOutAndNeedsOneForAMeterNeverRead(t *testing.T) {
	electricity, water := charge(UnitPerKWh, "Electricity", 3500), charge(UnitPerCubicMeter, "Water", 10000)
	charges := []Charge{electricity, charge(UnitPerPeriod, "Internet", 1), water}

	got, err := CarryOver([]ReadingInput{{ChargeID: water.ID, Value: 7}}, charges, map[uuid.UUID]float64{electricity.ID: 1500, water.ID: 5})
	if err != nil || len(got) != 2 || got[0].ChargeID != water.ID || got[1].ChargeID != electricity.ID || got[1].Value != 1500 || got[1].Previous != nil {
		t.Fatalf("got %+v, %v", got, err)
	}

	_, err = CarryOver(nil, charges, map[uuid.UUID]float64{water.ID: 5})
	if v := violationOf(t, err); v.Reason != ReasonNewReadingRequired || v.Params["service"] != "Electricity" {
		t.Fatalf("violation: %+v", v)
	}
	if ids := MeteredIDs(charges); len(ids) != 2 || ids[0] != electricity.ID || ids[1] != water.ID {
		t.Errorf("metered: %v", ids)
	}
}

func TestReadingPreviousIsWhatTheReadingCountedFrom(t *testing.T) {
	if got := (Reading{Value: 1300, Consumption: 200}).Previous(); got != 1100 {
		t.Errorf("got %v", got)
	}
}
