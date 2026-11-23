package invoice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// attempts is how many times a bill is made again when its number or transfer text clashes with another bill that
// was made at the same moment (the random parts are drawn again).
const attempts = 5

// errClash says that the number or the transfer text of a new bill was taken; the use case draws new ones.
var errClash = errors.New("number or transfer text taken")

// Create writes a bill for a period of a running contract. It stays a draft (nobody but the landlord sees it, nothing
// is emailed) until it is sent; with AlreadyPaid it is created sent and paid at once, with nothing sent to anybody.
func (s *Invoices) Create(ctx context.Context, landlordID uuid.UUID, in CreateInput) (View, error) {
	return s.create(ctx, landlordID, in, true)
}

// create is Create; running false lets the demo seed write the bills of a contract that has ended.
func (s *Invoices) create(ctx context.Context, landlordID uuid.UUID, in CreateInput, mustRun bool) (View, error) {
	var id uuid.UUID
	for attempt := 0; ; attempt++ {
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			f, err := s.lockContract(ctx, landlordID, in.ContractID)
			if err != nil {
				return err
			}
			if mustRun && !f.Running() {
				return domain.ErrContractNotRunning
			}
			billed, err := s.billedSet(ctx, f.ID)
			if err != nil {
				return err
			}
			period, err := domain.ResolvePeriod(f, domain.PeriodsOf(f, s.today()), in.PeriodStart, billed)
			if err != nil {
				return err
			}
			if billed[period.Start] {
				return domain.ErrPeriodBilled
			}
			items, total, readings, err := s.compose(ctx, f, period, in.BillInput, true, nil)
			if err != nil {
				return err
			}
			inv, err := s.newInvoice(ctx, f, period, items, total, readings, newBill{
				createdBy: &landlordID, dueOn: in.DueOn, note: in.Note, name: in.Name, label: in.PaymentLabel, delivery: in.Delivery,
			})
			if err != nil {
				return err
			}
			if id, err = s.store(ctx, f, inv); err != nil {
				return err
			}
			inv.ID = id
			if err := s.record(ctx, AuditCreated, landlordID, inv, f, map[string]any{
				"number": inv.Number, "total_amount_vnd": total, "period_start": period.Start.Format(domain.DateLayout),
				"via": "draft", "readings": readingMeta(readings),
			}); err != nil {
				return err
			}
			if in.AlreadyPaid {
				d, err := s.invoices.ForUpdate(ctx, landlordView(landlordID), id)
				if err != nil {
					return err
				}
				return s.applyStatus(ctx, &d, domain.StatusPaid, landlordID)
			}
			return nil
		})
		if errors.Is(err, errClash) && attempt+1 < attempts {
			continue
		}
		if err != nil {
			return View{}, err
		}
		break
	}
	return s.Get(ctx, landlordView(landlordID), id)
}

// newBill is what a new bill carries besides its lines.
type newBill struct {
	createdBy *uuid.UUID
	// source is how it was sent; nil is a draft.
	source   *domain.Source
	dueOn    *time.Time
	note     *string
	name     *string
	label    *string
	delivery *domain.Delivery
}

// newInvoice builds the bill of a period with its number, name and transfer text. The contract is locked, so the bills
// of one contract are made one at a time.
func (s *Invoices) newInvoice(ctx context.Context, f domain.ContractFacts, p domain.Period, items []domain.Item, total int64, readings []domain.Reading, b newBill) (domain.Invoice, error) {
	number, err := s.nextNumber(ctx, p.Start)
	if err != nil {
		return domain.Invoice{}, err
	}
	month := domain.MonthKey(p.Start)
	counts, err := s.invoices.MonthCounts(ctx, f.LandlordID, firstOfMonth(p.Start), firstOfMonth(p.Start).AddDate(0, 1, -1))
	if err != nil {
		return domain.Invoice{}, err
	}
	label := domain.DefaultLabel(int(p.Start.Month()), f.RoomName, 1+counts[month])
	if given := trimmed(b.label); given != "" {
		label = given
	}
	code, err := s.codeFor(ctx, label, "", uuid.Nil)
	if err != nil {
		return domain.Invoice{}, err
	}
	name := domain.DefaultName(p.Start, f.RoomName)
	if given := trimmed(b.name); given != "" {
		name = given
	}
	now := s.clock.Now()
	inv := domain.Invoice{
		ContractID: f.ID, CreatedByID: b.createdBy, Number: number, Name: name, PaymentLabel: label, PaymentCode: code,
		PeriodStart: p.Start, PeriodEnd: p.End, TotalVND: total, DueAt: s.dueAt(b.dueOn), Status: domain.StatusDraft, Note: b.note,
		Delivery: domain.ResolveDelivery(f.HasAccount(), b.delivery), Items: items, Readings: readings,
	}
	if b.source != nil {
		// Only the plan's automatic invoicing sends right away; a landlord's bill starts as a draft.
		inv.Status, inv.SentAt, inv.SentVia = domain.StatusSent, &now, b.source
		inv.CreatedByID = nil
	}
	for i := range inv.Readings {
		inv.Readings[i].ReadOn = s.today()
	}
	return inv, nil
}

// trimmed is what the landlord wrote without the spaces around it ("" for nothing).
func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func firstOfMonth(d time.Time) time.Time { return domain.Date(d.Year(), d.Month(), 1) }

// store inserts the bill; a period that is already billed, and a number or transfer text that clashed, are told apart.
func (s *Invoices) store(ctx context.Context, f domain.ContractFacts, inv domain.Invoice) (uuid.UUID, error) {
	id, err := s.invoices.Insert(ctx, inv)
	var ce *shared.ConstraintError
	switch {
	case err == nil:
		return id, nil
	case errors.As(err, &ce) && ce.Kind == shared.Unique && ce.Constraint == ConstraintPeriod:
		return uuid.Nil, domain.ErrPeriodBilled
	case shared.IsUnique(err):
		return uuid.Nil, errClash
	}
	return uuid.Nil, err
}

// nextNumber draws an invoice number ("INV-202604-K3T9QX") that is not in use.
func (s *Invoices) nextNumber(ctx context.Context, periodStart time.Time) (string, error) {
	for range attempts {
		tail, err := s.random.Pick("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 6)
		if err != nil {
			return "", err
		}
		number := "INV-" + periodStart.Format("200601") + "-" + tail
		taken, err := s.invoices.NumberTaken(ctx, number)
		if err != nil || !taken {
			return number, err
		}
	}
	return "", errClash
}

// codeFor makes the transfer text of a bill: its words and a random tail that keeps the whole unique. A bill keeps its
// tail when only its words change (current is its present text, exceptID its id).
func (s *Invoices) codeFor(ctx context.Context, label, current string, exceptID uuid.UUID) (string, error) {
	tail, keep := domain.TailOf(current)
	for range attempts * 2 {
		if !keep {
			var err error
			if tail, err = s.random.Pick(domain.TailAlphabet, domain.TailLength); err != nil {
				return "", err
			}
		}
		code := domain.ComposeCode(label, tail)
		taken, err := s.invoices.CodeTaken(ctx, code, exceptID)
		if err != nil || !taken {
			return code, err
		}
		keep = false
	}
	return "", errClash
}

// readingMeta writes the readings of a bill for the audit trail.
func readingMeta(readings []domain.Reading) []map[string]any {
	out := make([]map[string]any, len(readings))
	for i, r := range readings {
		out[i] = map[string]any{"charge_id": r.ChargeID.String(), "reading_value": r.Value, "previous_value": r.Previous(), "consumption": r.Consumption}
	}
	return out
}
