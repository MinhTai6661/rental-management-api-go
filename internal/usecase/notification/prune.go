package notification

import (
	"context"
	"time"
)

// pruneBatch is how many messages one delete statement removes, so the prune never holds a long lock.
const pruneBatch = 5000

// Pruner deletes the messages older than the retention period.
type Pruner struct{ *core }

// NewPruner returns the use case.
func NewPruner(d Deps, o Options) *Pruner { return &Pruner{newCore(d, o)} }

// Prune deletes the messages older than RetentionDays, batch by batch, and returns how many it deleted. It is safe to
// run again and to run twice at once.
func (p *Pruner) Prune(ctx context.Context) (int, error) {
	before := p.clock.Now().Add(-time.Duration(p.opts.RetentionDays) * 24 * time.Hour)
	total := 0
	for {
		n, err := p.notifications.PruneBefore(ctx, before, pruneBatch)
		total += n
		if err != nil {
			return total, err
		}
		if n < pruneBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}
