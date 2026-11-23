package plan

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// Limits tells what the plan of an account allows. The property and contract contexts reach it through their own
// PlanLimits ports (cmd/rental-management adapts them). It asks the database each time and caches nothing, and the
// calls made with the ctx of a transaction see that transaction.
type Limits struct{ *core }

// NewLimits returns the limits resolver.
func NewLimits(d Deps) *Limits { return &Limits{core: newCore(d)} }

// For returns the limits of the account's plan for the audience: its running subscription's plan, else the default
// plan. With no plan at all every limit is nil, so nothing is limited.
func (l *Limits) For(ctx context.Context, userID uuid.UUID, audience domain.Audience) (domain.Limits, error) {
	p, err := l.effective(ctx, userID, audience)
	if err != nil || p == nil {
		return domain.Limits{}, err
	}
	return p.Limits, nil
}

// HasFeature says whether the plan of the account for the audience switches the feature on; with no plan at all it
// is off (a feature, unlike a limit, is something a plan gives).
func (l *Limits) HasFeature(ctx context.Context, userID uuid.UUID, audience domain.Audience, f domain.Feature) (bool, error) {
	p, err := l.effective(ctx, userID, audience)
	if err != nil || p == nil {
		return false, err
	}
	return p.HasFeature(f), nil
}

// SearchPriority is how high the landlord's rooms rank in find rooms (greater first); 0 without a plan.
func (l *Limits) SearchPriority(ctx context.Context, landlordID uuid.UUID) (int, error) {
	p, err := l.effective(ctx, landlordID, domain.AudienceLandlord)
	if err != nil || p == nil {
		return 0, err
	}
	return p.SearchPriority, nil
}

// SearchPriorities is SearchPriority for several landlords at once (the search ranks a whole page of rooms): the
// priority of each one's running plan, else of the default plan, 0 when there is none. One query for the running
// plans and one for the default, however many landlords there are.
func (l *Limits) SearchPriorities(ctx context.Context, landlordIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(landlordIDs))
	if len(landlordIDs) == 0 {
		return out, nil
	}
	running, err := l.subs.RunningPriorities(ctx, landlordIDs, domain.AudienceLandlord, l.clock.Now())
	if err != nil {
		return nil, err
	}
	def := 0
	if p, err := l.plans.Default(ctx, domain.AudienceLandlord); err == nil {
		def = p.SearchPriority
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	for _, id := range landlordIDs {
		if v, ok := running[id]; ok {
			out[id] = v
		} else {
			out[id] = def
		}
	}
	return out, nil
}
