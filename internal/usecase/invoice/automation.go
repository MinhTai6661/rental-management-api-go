package invoice

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// batch is how many contracts or bills the daily commands read at a time.
const batch = 100

// Automation is the use cases the daily commands run: sending the bills of plans with automatic invoicing, and
// reminding people of bills about to fall due.
type Automation struct {
	*core
	creator *Invoices
}

// NewAutomation builds the daily use cases.
func NewAutomation(d Deps, o Options) *Automation {
	return &Automation{core: newCore(d, o), creator: NewInvoices(d, o)}
}

// SendAutomatic sends the bill of the current period of every running contract whose landlord's plan has automatic
// invoicing and that has not been billed yet. Meters (electricity, water) cannot be known here, so the bill carries
// the rent and the fixed charges only. A bill for a contract nobody with an account holds is passed on by link. It
// is safe to run again, and to run twice at once: a period that has a bill is left alone. It returns how many bills
// it sent.
func (a *Automation) SendAutomatic(ctx context.Context) (int, error) {
	sent := 0
	enabled := map[uuid.UUID]bool{}
	after := uuid.Nil
	for {
		contracts, err := a.contracts.Billable(ctx, after, batch)
		if err != nil {
			return sent, err
		}
		for _, f := range contracts {
			after = f.ID
			on, known := enabled[f.LandlordID]
			if !known {
				if on, err = a.plans.AutoInvoicing(ctx, f.LandlordID); err != nil {
					return sent, err
				}
				enabled[f.LandlordID] = on
			}
			if !on {
				continue
			}
			ok, err := a.sendCurrent(ctx, f.ID)
			if err != nil {
				// One contract that cannot be billed must not stop the others.
				a.log.Error("send automatic invoice", "error", err, "contract_id", f.ID)
				continue
			}
			if ok {
				sent++
			}
		}
		if len(contracts) < batch {
			return sent, nil
		}
	}
}

// sendCurrent bills the current period of the contract if it has no bill yet; it says whether it sent one.
func (a *Automation) sendCurrent(ctx context.Context, contractID uuid.UUID) (bool, error) {
	var d domain.Detail
	sent := false
	err := a.tx.WithinTx(ctx, func(ctx context.Context) error {
		f, err := a.contracts.Lock(ctx, contractID)
		if err != nil {
			return notFound(err)
		}
		if !f.Running() {
			return nil
		}
		periods := domain.PeriodsOf(f, a.today())
		if len(periods) == 0 {
			return nil
		}
		period := periods[len(periods)-1]
		billed, err := a.billedSet(ctx, f.ID)
		if err != nil || billed[period.Start] {
			return err
		}
		items, total, _, err := a.compose(ctx, f, period, BillInput{}, false, nil)
		if err != nil {
			return err
		}
		source := domain.SourceAutomatic
		inv, err := a.creator.newInvoice(ctx, f, period, items, total, nil, newBill{source: &source})
		if err != nil {
			return err
		}
		id, err := a.creator.store(ctx, f, inv)
		if err != nil {
			return err
		}
		inv.ID = id
		if err := a.record(ctx, AuditCreated, uuid.Nil, inv, f, map[string]any{
			"number": inv.Number, "total_amount_vnd": total, "period_start": period.Start.Format(domain.DateLayout), "via": "automatic",
		}); err != nil {
			return err
		}
		d = domain.Detail{Invoice: inv, Contract: f}
		// Passed on by link: the link is made now, so it is there to copy.
		if !inv.Delivery.ReachesAccount() {
			if _, err := a.creator.linkFor(ctx, &d, uuid.Nil); err != nil {
				return err
			}
		}
		if err := a.record(ctx, AuditSent, uuid.Nil, d.Invoice, f, map[string]any{"via": "automatic"}); err != nil {
			return err
		}
		sent = true
		return nil
	})
	if errors.Is(err, domain.ErrPeriodBilled) {
		return false, nil // another run billed it first
	}
	if err != nil || !sent {
		return false, err
	}
	a.mail(ctx, d)
	a.notifyTenant(ctx, d, NotifyInvoiceSent)
	return true, nil
}

// SendReminders tells people about bills about to fall due: the tenant ReminderDays before the due date, and the day
// after it both the tenant and the landlord. A bill whose remaining amount is already covered by payments (even ones
// still waiting for review) is left alone, and every bill gets each reminder once, so the command is safe to run
// again the same day. It returns how many notices it sent.
func (a *Automation) SendReminders(ctx context.Context) (int, error) {
	today := a.today()
	soon := today.AddDate(0, 0, a.opts.ReminderDays)
	overdue := today.AddDate(0, 0, -1)
	soonFrom, soonTo := domain.StartOfDay(soon, a.opts.Location), domain.EndOfDay(soon, a.opts.Location)
	overdueFrom, overdueTo := domain.StartOfDay(overdue, a.opts.Location), domain.EndOfDay(overdue, a.opts.Location)
	sent := 0
	after := uuid.Nil
	for {
		bills, err := a.invoices.Due(ctx, soonFrom, soonTo, overdueFrom, overdueTo, after, batch)
		if err != nil {
			return sent, err
		}
		ids := make([]uuid.UUID, len(bills))
		for i, b := range bills {
			ids[i], after = b.ID, b.ID
		}
		totals, err := a.totalsOf(ctx, ids)
		if err != nil {
			return sent, err
		}
		var soonIDs, overdueIDs []uuid.UUID
		for _, b := range bills {
			// Every bill is marked, also the ones nothing is left to pay on, so they are not looked at again.
			kind := domain.ReminderFor(domain.Today(b.DueAt, a.opts.Location), today, a.opts.ReminderDays)
			switch kind {
			case domain.ReminderDueSoon:
				soonIDs = append(soonIDs, b.ID)
			case domain.ReminderOverdue:
				overdueIDs = append(overdueIDs, b.ID)
			default:
				continue
			}
			if domain.SummaryOf(b.TotalVND, totals[b.ID]).RemainingVND <= 0 {
				continue
			}
			if kind == domain.ReminderOverdue {
				if a.notifyTenant(ctx, b, NotifyInvoiceOverdue) {
					sent++
				}
				a.notify(ctx, a.landlordNotice(b))
				sent++
				continue
			}
			if a.notifyTenant(ctx, b, NotifyInvoiceDueSoon) {
				sent++
			}
		}
		if err := a.invoices.MarkReminded(ctx, domain.ReminderDueSoon, soonIDs, today); err != nil {
			return sent, err
		}
		if err := a.invoices.MarkReminded(ctx, domain.ReminderOverdue, overdueIDs, today); err != nil {
			return sent, err
		}
		if len(bills) < batch {
			return sent, nil
		}
	}
}

// landlordNotice tells the landlord that a bill is overdue.
func (a *Automation) landlordNotice(d domain.Detail) Notification {
	params := a.params(d)
	params["tenant"] = d.Contract.Recipient()
	if params["tenant"] == "" {
		params["tenant"] = d.Contract.RoomName
	}
	return Notification{
		UserID: d.Contract.LandlordID, Kind: NotifyTenantInvoiceOverdue, Params: params,
		URL: "/contracts/" + d.ContractID.String(), Audience: AudienceLandlord,
	}
}
