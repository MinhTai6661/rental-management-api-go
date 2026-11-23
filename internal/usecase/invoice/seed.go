package invoice

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// Seeder gives the demo contracts some bills.
type Seeder struct {
	*core
	uc *Invoices
}

// NewSeeder returns a seeder on the invoice use cases (so the rules apply to the demo data too).
func NewSeeder(d Deps, o Options) *Seeder { return &Seeder{core: newCore(d, o), uc: NewInvoices(d, o)} }

// SeedDemo gives the demo contracts bills of every kind: on the contract of P103, which ended, a paid bill, a sent one
// and a sent one passed on by link; on the running contract of P101 a draft for the current period. It is safe to
// repeat: a contract that already has a bill is left alone, and so is one that is missing (the demo contracts were not
// loaded). It returns how many bills it made.
func (s *Seeder) SeedDemo(ctx context.Context, landlordID uuid.UUID) (int, error) {
	made := 0
	for _, room := range []string{"P103", "P101"} {
		f, err := s.contracts.FindByRoomName(ctx, landlordID, room)
		if errors.Is(err, shared.ErrNotFound) {
			continue
		}
		if err != nil {
			return made, err
		}
		have, err := s.invoices.CountOfContract(ctx, f.ID)
		if err != nil || have > 0 {
			if err != nil {
				return made, err
			}
			continue
		}
		n, err := s.seedContract(ctx, landlordID, f)
		made += n
		if err != nil {
			return made, err
		}
	}
	return made, nil
}

// seedContract writes the bills of one demo contract: an ended one has its first periods billed (paid, sent, sent by
// link), a running one a draft for its current period.
func (s *Seeder) seedContract(ctx context.Context, landlordID uuid.UUID, f domain.ContractFacts) (int, error) {
	charges, err := s.charges.ChargesOf(ctx, f.RoomID)
	if err != nil {
		return 0, err
	}
	periods := domain.PeriodsOf(f, s.today())
	if len(periods) == 0 {
		return 0, nil
	}
	// The first bill of a meter starts from zero, the next ones from the index of the one before.
	read := func(step int) []domain.ReadingInput {
		var out []domain.ReadingInput
		zero := 0.0
		for _, id := range domain.MeteredIDs(charges) {
			in := domain.ReadingInput{ChargeID: id, Value: float64(120 * (step + 1))}
			if step == 0 {
				in.Previous = &zero
			}
			out = append(out, in)
		}
		return out
	}
	create := func(step, readStep int) (View, error) {
		start := periods[step].Start
		return s.uc.create(ctx, landlordID, CreateInput{
			ContractID: f.ID, PeriodStart: &start, BillInput: BillInput{Readings: read(readStep)},
		}, false)
	}

	if f.Running() {
		_, err := create(len(periods)-1, 0)
		return 1, err
	}
	link := domain.DeliveryLink
	steps := []func(View) error{
		func(v View) error { _, err := s.uc.SetStatus(ctx, landlordID, v.ID, domain.StatusPaid); return err },
		func(v View) error { _, err := s.uc.Send(ctx, landlordID, v.ID, nil); return err },
		func(v View) error { _, err := s.uc.Send(ctx, landlordID, v.ID, &link); return err },
	}
	made := 0
	for i, step := range steps {
		if i >= len(periods) {
			break
		}
		v, err := create(i, i)
		if err != nil {
			return made, err
		}
		made++
		if err := step(v); err != nil {
			return made, err
		}
	}
	return made, nil
}
