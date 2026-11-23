package plan

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// Seeder writes the base plan catalog and puts the demo accounts on paid plans.
type Seeder struct{ *core }

// NewSeeder returns a seeder.
func NewSeeder(d Deps) *Seeder { return &Seeder{core: newCore(d)} }

// SeedPlans writes the base catalog by code: a plan that is missing is created, one that exists is written again
// with the base values (a later edit of another plan survives). It never takes a default flag away from a plan that
// has it. It runs in one transaction and is safe to repeat.
func (s *Seeder) SeedPlans(ctx context.Context) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		for _, base := range domain.Base() {
			current, err := s.plans.GetByCode(ctx, base.Code)
			switch {
			case errors.Is(err, shared.ErrNotFound):
				if base.IsDefault {
					if err := s.plans.ClearDefault(ctx, base.Audience, uuid.Nil); err != nil {
						return err
					}
				}
				if _, err := s.plans.Insert(ctx, base); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				base.ID, base.IsDefault = current.ID, current.IsDefault || base.IsDefault
				if base.IsDefault && !current.IsDefault {
					if err := s.plans.ClearDefault(ctx, base.Audience, base.ID); err != nil {
						return err
					}
				}
				if err := s.plans.Update(ctx, base); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SeedDemoSubscriptions puts the demo accounts on their plans (usersByEmail has the ids the identity seed made) and
// returns how many it changed. An account that is missing, a plan that is missing, and an account that already has a
// running plan on that side are left alone, so it is safe to repeat.
func (s *Seeder) SeedDemoSubscriptions(ctx context.Context, usersByEmail map[string]uuid.UUID) (int, error) {
	done := 0
	for _, demo := range domain.DemoAssignments() {
		userID, ok := usersByEmail[demo.Email]
		if !ok {
			continue
		}
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			p, err := s.plans.GetByCode(ctx, demo.PlanCode)
			if errors.Is(err, shared.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			account, err := s.lockAccount(ctx, userID)
			if err != nil {
				return err
			}
			if _, err := s.subs.Running(ctx, userID, p.Audience, s.clock.Now()); err == nil {
				return nil
			} else if !errors.Is(err, shared.ErrNotFound) {
				return err
			}
			if err := domain.CheckAssignable("", account.Roles, p.Audience, p.IsActive); err != nil {
				return err
			}
			if _, err := s.give(ctx, uuid.Nil, nil, userID, p, nil, AuditSubscriptionAssigned); err != nil {
				return err
			}
			done++
			return nil
		})
		if err != nil {
			return done, err
		}
	}
	return done, nil
}
