package invoice

import (
	"math"
	"strconv"

	"github.com/google/uuid"
)

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// RoundVND rounds an amount to whole VND.
func RoundVND(x float64) int64 { return int64(math.Round(x)) }

// FormatNumber writes a reading the short way ("100", "12.5").
func FormatNumber(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// CarryOver completes the readings of a bill the landlord writes: every metered charge needs its new index, so one
// that was left out keeps the last stored index (nothing is consumed on this bill). A meter that was never read has no
// index to fall back on, so its new index has to be entered. stored holds the last stored index by charge id.
func CarryOver(readings []ReadingInput, charges []Charge, stored map[uuid.UUID]float64) ([]ReadingInput, error) {
	given := make(map[uuid.UUID]bool, len(readings))
	for _, r := range readings {
		given[r.ChargeID] = true
	}
	out := append([]ReadingInput(nil), readings...)
	for _, c := range charges {
		if !c.IsMetered() || given[c.ID] {
			continue
		}
		last, ok := stored[c.ID]
		if !ok {
			return nil, violation(FieldReadings, ReasonNewReadingRequired, map[string]string{"service": c.Name})
		}
		out = append(out, ReadingInput{ChargeID: c.ID, Value: last})
	}
	return out, nil
}

// MeteredIDs lists the charges that are read from a meter.
func MeteredIDs(charges []Charge) []uuid.UUID {
	var ids []uuid.UUID
	for _, c := range charges {
		if c.IsMetered() {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// Consumptions works out what each meter used on this bill. The previous index is the one given with the reading,
// otherwise the last stored one (stored, by charge id; a missing key means never read). A reading may not lower the
// index.
func Consumptions(readings []ReadingInput, charges []Charge, stored map[uuid.UUID]float64) ([]Consumption, error) {
	metered := make(map[uuid.UUID]Charge, len(charges))
	for _, c := range charges {
		if c.IsMetered() {
			metered[c.ID] = c
		}
	}
	out := make([]Consumption, 0, len(readings))
	for _, r := range readings {
		charge, ok := metered[r.ChargeID]
		if !ok {
			return nil, violation(FieldReadings, ReasonUnknownService, nil)
		}
		// The landlord can correct the previous index (a wrong earlier reading, a replaced meter); it only changes this
		// bill's consumption.
		var previous float64
		switch last, hasLast := stored[r.ChargeID]; {
		case r.Previous != nil:
			previous = *r.Previous
		case hasLast:
			previous = last
		default:
			return nil, violation(FieldReadings, ReasonPreviousReadingRequired, map[string]string{"service": charge.Name})
		}
		if r.Value < previous {
			return nil, violation(FieldReadings, ReasonReadingLowerThanPrevious,
				map[string]string{"service": charge.Name, "previous": FormatNumber(previous)})
		}
		price := charge.AmountVND
		if r.UnitPriceVND != nil {
			price = *r.UnitPriceVND
		}
		out = append(out, Consumption{
			ChargeID: r.ChargeID, Previous: previous, Value: r.Value, Used: round2(r.Value - previous), UnitPriceVND: price,
		})
	}
	return out, nil
}

// LineInput is what the lines of a bill are worked out from.
type LineInput struct {
	// Units is how many months or days the period counts; PeriodIndex is 0 for the first bill of the contract.
	Units       int
	PeriodIndex int
	Occupants   int
	// RentUnitPriceVND is the rent of one unit; RentDescription is the words of the rent line.
	RentUnitPriceVND int64
	RentDescription  string
	Charges          []Charge
	Consumption      []Consumption
	Extras           []Extra
}

// Lines builds the lines of a bill (rent, charges, extra lines) with their amounts, and the total.
func Lines(in LineInput) ([]Item, int64) {
	used := make(map[uuid.UUID]Consumption, len(in.Consumption))
	for _, c := range in.Consumption {
		used[c.ChargeID] = c
	}
	type line struct {
		kind, description string
		quantity          float64
		unitPrice         int64
	}
	lines := []line{{KindRent, in.RentDescription, float64(in.Units), in.RentUnitPriceVND}}
	for _, c := range in.Charges {
		var quantity float64
		switch c.Unit {
		case UnitPerPeriod:
			quantity = float64(in.Units)
		case UnitPerPerson:
			quantity = float64(in.Occupants * in.Units)
		case UnitPerKWh, UnitPerCubicMeter:
			quantity = used[c.ID].Used
		case UnitOneTime:
			// A set-up fee goes on the first bill only.
			if in.PeriodIndex == 0 {
				quantity = 1
			}
		}
		if quantity <= 0 {
			continue
		}
		l := line{c.Kind, c.Name, quantity, c.AmountVND}
		if read, ok := used[c.ID]; ok {
			l.description = c.Name + " (" + FormatNumber(read.Previous) + " → " + FormatNumber(read.Value) + ")"
			l.unitPrice = read.UnitPriceVND
		}
		lines = append(lines, l)
	}
	for _, e := range in.Extras {
		lines = append(lines, line{KindOther, e.Description, 1, e.AmountVND})
	}
	items := make([]Item, len(lines))
	var total int64
	for i, l := range lines {
		amount := RoundVND(l.quantity * float64(l.unitPrice))
		total += amount
		items[i] = Item{Kind: l.kind, Description: l.description, Quantity: l.quantity, UnitPriceVND: l.unitPrice, AmountVND: amount, DisplayOrder: i + 1}
	}
	return items, total
}

// Readings turns the consumption of a bill into the meter readings it records.
func Readings(consumption []Consumption) []Reading {
	out := make([]Reading, len(consumption))
	for i, c := range consumption {
		out[i] = Reading{ChargeID: c.ChargeID, Value: c.Value, Consumption: c.Used, UnitPriceVND: c.UnitPriceVND}
	}
	return out
}
