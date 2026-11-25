package invoice

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// editable loads a landlord's bill for a correction and refuses one that is locked: a bill that is paid, or that
// somebody paid or is paying, cannot change under a tenant who already sent money for it.
func (s *Invoices) editable(ctx context.Context, landlordID, id uuid.UUID, lock bool) (domain.Detail, error) {
	get := s.invoices.Get
	if lock {
		get = s.invoices.ForUpdate
	}
	d, err := get(ctx, s.actor(ctx, landlordID), id)
	if err != nil {
		return domain.Detail{}, notFound(err)
	}
	totals, err := s.totalsOf(ctx, []uuid.UUID{id})
	if err != nil {
		return domain.Detail{}, err
	}
	if domain.Locked(d.Status, totals[id]) {
		return domain.Detail{}, domain.ErrLocked
	}
	return d, nil
}

// PreviewUpdate shows what a corrected bill would look like, without saving it.
func (s *Invoices) PreviewUpdate(ctx context.Context, landlordID, id uuid.UUID, in BillInput) (domain.Preview, error) {
	d, err := s.editable(ctx, landlordID, id, false)
	if err != nil {
		return domain.Preview{}, err
	}
	period, err := s.periodOf(d)
	if err != nil {
		return domain.Preview{}, err
	}
	items, total, _, err := s.compose(ctx, d.Contract, period, in, true, &id)
	if err != nil {
		return domain.Preview{}, err
	}
	return domain.Preview{Period: period, Items: items, TotalVND: total}, nil
}

// periodOf is the billing period an existing bill covers.
func (s *Invoices) periodOf(d domain.Detail) (domain.Period, error) {
	start := d.PeriodStart
	return domain.ResolvePeriod(d.Contract, domain.PeriodsOf(d.Contract, s.today()), &start, nil)
}

// Update corrects an unpaid bill the way it was made: rent, meter readings and extra lines are filled in again and the
// lines and the total are rebuilt. The period stays what it was. The bill is counted from the readings that came
// before it. domain.ErrLocked when it is paid or somebody paid it.
func (s *Invoices) Update(ctx context.Context, landlordID, id uuid.UUID, in UpdateInput) (View, error) {
	var d domain.Detail
	for attempt := 0; ; attempt++ {
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			var err error
			if d, err = s.editable(ctx, landlordID, id, true); err != nil {
				return err
			}
			period, err := s.periodOf(d)
			if err != nil {
				return err
			}
			items, total, readings, err := s.compose(ctx, d.Contract, period, in.BillInput, true, &id)
			if err != nil {
				return err
			}
			d.Items, d.Readings, d.TotalVND = items, readings, total
			for i := range d.Readings {
				d.Readings[i].ReadOn = s.today()
			}
			if in.DueOn != nil {
				d.DueAt = s.dueAt(in.DueOn)
			}
			if in.Note.Set {
				d.Note = in.Note.Value
			}
			if name := trimmed(in.Name); name != "" {
				d.Name = name
			}
			if label := trimmed(in.PaymentLabel); label != "" {
				d.PaymentLabel = label
				if d.PaymentCode, err = s.codeFor(ctx, d.PaymentLabel, d.PaymentCode, id); err != nil {
					return err
				}
			}
			if err := s.invoices.Replace(ctx, d.Invoice); err != nil {
				return err
			}
			return s.record(ctx, AuditUpdated, landlordID, d.Invoice, d.Contract, map[string]any{
				"lines": len(items), "status": string(d.Status), "total_amount_vnd": total,
			})
		})
		if errors.Is(err, errClash) && attempt+1 < attempts {
			continue
		}
		if err != nil {
			return View{}, err
		}
		break
	}
	// What the person is told comes after the correction is stored.
	fresh, err := s.invoices.Get(ctx, s.actor(ctx, landlordID), id)
	if err != nil {
		return View{}, notFound(err)
	}
	s.tellLandlord(ctx, s.actor(ctx, landlordID), fresh, NotifyManagerBillEdited)
	// A draft is emailed when it is sent, not before.
	if fresh.Status == domain.StatusSent {
		if in.ResendEmail {
			s.mail(ctx, fresh)
		}
		s.notifyTenant(ctx, fresh, NotifyInvoiceUpdated)
	}
	return s.view(ctx, s.actor(ctx, landlordID), fresh)
}

// Delete removes a bill made by mistake, with the meter readings it recorded, so the period can be billed again and
// the next bill starts from the right index. domain.ErrLocked when it is paid or somebody paid it.
func (s *Invoices) Delete(ctx context.Context, landlordID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.editable(ctx, landlordID, id, true)
		if err != nil {
			return err
		}
		// A manager removes only a draft they wrote themselves.
		if v := s.actor(ctx, landlordID); v.AsManager && !domain.ManagerAbilities(v, d.Status, domain.Totals{}, d.CreatedByID, false).Delete {
			return domain.ErrForbidden
		}
		// Only rejected payments can be left: the ledger takes them away with the bill.
		if err := s.ledger.Purge(ctx, id); err != nil {
			return err
		}
		if err := s.record(ctx, AuditDeleted, landlordID, d.Invoice, d.Contract, map[string]any{
			"number": d.Number, "total_amount_vnd": d.TotalVND, "status": string(d.Status),
		}); err != nil {
			return err
		}
		return s.invoices.Delete(ctx, id)
	})
}
