package invoice

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// Invoices is the use cases of the bills of a landlord's contracts, and of a tenant's view of their own.
type Invoices struct{ *core }

// NewInvoices builds the invoice use cases.
func NewInvoices(d Deps, o Options) *Invoices { return &Invoices{newCore(d, o)} }

// MaxPageSize is the most bills a page of a list may hold.
const MaxPageSize = 50

// List returns a page of the bills of a contract the viewer sees, newest period first. A landlord sees every bill,
// a tenant only what was sent to their account. domain.ErrNotFound when the viewer does not see the contract.
func (s *Invoices) List(ctx context.Context, v domain.Viewer, contractID uuid.UUID, limit, offset int) (Page, error) {
	if _, err := s.contracts.Get(ctx, v, contractID); err != nil {
		return Page{}, notFound(err)
	}
	limit = min(max(limit, 1), MaxPageSize)
	details, total, err := s.invoices.List(ctx, v, contractID, limit, max(offset, 0))
	if err != nil {
		return Page{}, err
	}
	views, err := s.views(ctx, v, details)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: views, Total: total, HasMore: offset+len(details) < total}, nil
}

// Get returns one bill the viewer sees; domain.ErrNotFound for anybody else's.
func (s *Invoices) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (View, error) {
	d, err := s.invoices.Get(ctx, v, id)
	if err != nil {
		return View{}, notFound(err)
	}
	return s.view(ctx, v, d)
}

// Periods lists the started billing periods of a landlord's contract, each with its bill if any and the name and
// transfer words a new bill of it would get when the landlord writes none.
func (s *Invoices) Periods(ctx context.Context, landlordID, contractID uuid.UUID) ([]domain.PeriodRow, error) {
	f, err := s.contracts.Get(ctx, s.actor(ctx, landlordID), contractID)
	if err != nil {
		return nil, notFound(err)
	}
	periods := domain.PeriodsOf(f, s.today())
	if len(periods) == 0 {
		return []domain.PeriodRow{}, nil
	}
	billed, err := s.invoices.BilledPeriods(ctx, contractID)
	if err != nil {
		return nil, err
	}
	byStart := make(map[time.Time]domain.BilledPeriod, len(billed))
	for _, b := range billed {
		byStart[b.PeriodStart] = b
	}
	counts, err := s.invoices.MonthCounts(ctx, f.LandlordID, periods[0].Start, periods[len(periods)-1].Start)
	if err != nil {
		return nil, err
	}
	rows := make([]domain.PeriodRow, len(periods))
	for i, p := range periods {
		row := domain.PeriodRow{
			Period:              p,
			DefaultName:         domain.DefaultName(p.Start, f.RoomName),
			DefaultPaymentLabel: domain.DefaultLabel(int(p.Start.Month()), f.RoomName, 1+counts[domain.MonthKey(p.Start)]),
		}
		if b, ok := byStart[p.Start]; ok {
			row.Billed = &b
		}
		rows[i] = row
	}
	return rows, nil
}

// Preview shows what a new bill of the contract would look like, without saving anything.
func (s *Invoices) Preview(ctx context.Context, landlordID, contractID uuid.UUID, in PreviewInput) (domain.Preview, error) {
	f, err := s.contracts.Get(ctx, s.actor(ctx, landlordID), contractID)
	if err != nil {
		return domain.Preview{}, notFound(err)
	}
	if !f.Running() {
		return domain.Preview{}, domain.ErrContractNotRunning
	}
	billed, err := s.billedSet(ctx, contractID)
	if err != nil {
		return domain.Preview{}, err
	}
	period, err := domain.ResolvePeriod(f, domain.PeriodsOf(f, s.today()), in.PeriodStart, billed)
	if err != nil {
		return domain.Preview{}, err
	}
	items, total, _, err := s.compose(ctx, f, period, in.BillInput, true, nil)
	if err != nil {
		return domain.Preview{}, err
	}
	return domain.Preview{Period: period, Items: items, TotalVND: total}, nil
}

// MeterHistory returns the meter readings of one of the landlord's rooms, newest first (one meter when chargeID is
// set). domain.ErrNotFound for a room that is not theirs.
func (s *Invoices) MeterHistory(ctx context.Context, landlordID, roomID uuid.UUID, chargeID *uuid.UUID, limit, offset int) (ReadingPage, error) {
	if v := s.actor(ctx, landlordID); v.AsManager {
		if _, err := s.grantOn(ctx, v, roomID); err != nil {
			return ReadingPage{}, err
		}
	} else if _, err := s.charges.RoomOf(ctx, landlordID, roomID); err != nil {
		return ReadingPage{}, notFound(err)
	}
	items, total, err := s.invoices.ReadingHistory(ctx, roomID, chargeID, limit, offset)
	if err != nil {
		return ReadingPage{}, err
	}
	return ReadingPage{Items: items, Total: total}, nil
}
