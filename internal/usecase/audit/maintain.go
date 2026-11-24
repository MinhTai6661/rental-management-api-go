package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	domain "rental-management/internal/domain/audit"
)

// RetentionOptions say how far ahead partitions are made and how long each tier is kept.
type RetentionOptions struct {
	// PartitionsAhead is how many months after the current one get a partition.
	PartitionsAhead int
	// ImportantMonths and RoutineDays are how long the two tiers are kept.
	ImportantMonths int
	RoutineDays     int
}

// Maintainer keeps the monthly partitions of the trail: it makes the coming months and drops the expired ones whole.
type Maintainer struct {
	repo  Repository
	clock Clock
	log   *slog.Logger
	opts  RetentionOptions
}

// NewMaintainer returns a maintainer.
func NewMaintainer(repo Repository, clock Clock, log *slog.Logger, opts RetentionOptions) *Maintainer {
	return &Maintainer{repo: repo, clock: clock, log: log, opts: opts}
}

// MaintenanceResult counts what a run did.
type MaintenanceResult struct {
	Created int
	Dropped int
}

// Run makes the missing partitions and drops the expired ones. A step that fails is logged and the run goes on with the
// rest; the failures come back together. It is safe to run any number of times.
func (m *Maintainer) Run(ctx context.Context) (MaintenanceResult, error) {
	now := m.clock.Now()
	existing, err := m.repo.Partitions(ctx)
	if err != nil {
		return MaintenanceResult{}, err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	var (
		result MaintenanceResult
		errs   []error
	)
	for _, tier := range []domain.Tier{domain.Important, domain.Routine} {
		table := domain.TableOf(tier)
		for _, month := range domain.MonthsAhead(now, m.opts.PartitionsAhead) {
			if have[domain.PartitionName(table, month)] {
				continue
			}
			if err := m.repo.CreatePartition(ctx, table, month); err != nil {
				// Typically rows of that month already sit in the default partition.
				m.log.Error("could not create an audit partition", "table", table, "month", month.Format("2006-01"), "error", err)
				errs = append(errs, fmt.Errorf("create %s: %w", domain.PartitionName(table, month), err))
				continue
			}
			result.Created++
		}
		cutoff := domain.Cutoff(tier, now, m.opts.ImportantMonths, m.opts.RoutineDays)
		for _, p := range existing {
			if p.Table != table || p.Month.IsZero() || !domain.Expired(p.Month, cutoff) {
				continue
			}
			if err := m.repo.DropPartition(ctx, p.Name); err != nil {
				m.log.Error("could not drop an audit partition", "name", p.Name, "error", err)
				errs = append(errs, fmt.Errorf("drop %s: %w", p.Name, err))
				continue
			}
			result.Dropped++
		}
	}
	return result, errors.Join(errs...)
}
