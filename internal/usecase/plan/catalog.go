package plan

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// planCodeConstraint is the unique constraint on plans.code.
const planCodeConstraint = "plans_code_key"

// Optional is a field of a partial update that tells apart "absent" (leave it) from "null" (clear it).
type Optional[T any] struct {
	Set   bool
	Value *T
}

// PlanInput is a new plan. Plans are never deleted: a plan that is no longer offered is switched off (IsActive).
type PlanInput struct {
	Code           string
	Name           string
	Description    *string
	Audience       domain.Audience
	PriceVND       int64
	Interval       domain.Interval
	Limits         domain.Limits
	SearchPriority int
	Features       []domain.Feature
	IsDefault      bool
	IsActive       bool
	DisplayOrder   int
}

// PlanPatch is a partial update of a plan: only what is set changes. The audience cannot change (subscriptions to the
// plan carry it).
type PlanPatch struct {
	Code                    *string
	Name                    *string
	Description             Optional[string]
	PriceVND                *int64
	Interval                *domain.Interval
	MaxRooms                Optional[int]
	MaxBuildings            Optional[int]
	MaxImagesPerRoom        Optional[int]
	MaxActiveContracts      Optional[int]
	MaxPendingRegistrations Optional[int]
	SearchPriority          *int
	Features                *[]domain.Feature
	IsDefault               *bool
	IsActive                *bool
	DisplayOrder            *int
}

// Plans is the catalog: what a user is offered, and what a super admin edits.
type Plans struct{ *core }

// NewPlans returns the catalog use cases.
func NewPlans(d Deps) *Plans { return &Plans{core: newCore(d)} }

// Available returns a page of the plans on offer to an audience (active ones, in display order).
func (p *Plans) Available(ctx context.Context, audience domain.Audience, limit, offset int) ([]domain.Plan, int, error) {
	return p.plans.Search(ctx, domain.PlanQuery{Audience: &audience, ActiveOnly: true, Limit: limit, Offset: offset})
}

// List returns a page of the whole catalog, inactive plans included (super admin).
func (p *Plans) List(ctx context.Context, limit, offset int) ([]domain.Plan, int, error) {
	return p.plans.Search(ctx, domain.PlanQuery{Limit: limit, Offset: offset})
}

// Create adds a plan. A plan flagged as default takes the flag from the default of its audience.
func (p *Plans) Create(ctx context.Context, actor uuid.UUID, in PlanInput) (domain.Plan, error) {
	if !domain.ValidCode(in.Code) {
		return domain.Plan{}, domain.ErrInvalidCode
	}
	plan := domain.Plan{
		Code: in.Code, Name: in.Name, Description: in.Description, Audience: in.Audience, PriceVND: in.PriceVND,
		Interval: in.Interval, Limits: in.Limits, SearchPriority: in.SearchPriority, Features: in.Features,
		IsDefault: in.IsDefault, IsActive: in.IsActive, DisplayOrder: in.DisplayOrder,
	}
	if plan.Features == nil {
		plan.Features = []domain.Feature{}
	}
	var created domain.Plan
	err := p.tx.WithinTx(ctx, func(ctx context.Context) error {
		if plan.IsDefault {
			if err := p.plans.ClearDefault(ctx, plan.Audience, uuid.Nil); err != nil {
				return err
			}
		}
		id, err := p.plans.Insert(ctx, plan)
		if err != nil {
			return codeTaken(err)
		}
		if created, err = p.plans.Get(ctx, id); err != nil {
			return err
		}
		return p.audit.Record(ctx, AuditEvent{
			Action: AuditPlanSaved, ActorID: actor,
			Meta: map[string]any{"plan": created.Code, "created": true, "price_vnd": created.PriceVND},
		})
	})
	return created, err
}

// Update changes a plan. Flagging it as default clears the flag on the others of its audience; switching it off
// leaves the subscriptions to it working.
func (p *Plans) Update(ctx context.Context, actor, id uuid.UUID, patch PlanPatch) (domain.Plan, error) {
	if patch.Code != nil && !domain.ValidCode(*patch.Code) {
		return domain.Plan{}, domain.ErrInvalidCode
	}
	var saved domain.Plan
	err := p.tx.WithinTx(ctx, func(ctx context.Context) error {
		current, err := p.plans.Get(ctx, id)
		if errors.Is(err, shared.ErrNotFound) {
			return domain.ErrPlanNotFound
		}
		if err != nil {
			return err
		}
		next := applyPatch(current, patch)
		if next.IsDefault && !current.IsDefault {
			if err := p.plans.ClearDefault(ctx, next.Audience, next.ID); err != nil {
				return err
			}
		}
		if err := p.plans.Update(ctx, next); err != nil {
			return codeTaken(err)
		}
		if saved, err = p.plans.Get(ctx, id); err != nil {
			return err
		}
		return p.audit.Record(ctx, AuditEvent{
			Action: AuditPlanSaved, ActorID: actor, Meta: map[string]any{"plan": saved.Code, "created": false},
		})
	})
	return saved, err
}

// applyPatch returns the plan with the fields of the patch written over it.
func applyPatch(p domain.Plan, patch PlanPatch) domain.Plan {
	if patch.Code != nil {
		p.Code = *patch.Code
	}
	if patch.Name != nil {
		p.Name = *patch.Name
	}
	if patch.Description.Set {
		p.Description = patch.Description.Value
	}
	if patch.PriceVND != nil {
		p.PriceVND = *patch.PriceVND
	}
	if patch.Interval != nil {
		p.Interval = *patch.Interval
	}
	setLimit(&p.Limits.MaxRooms, patch.MaxRooms)
	setLimit(&p.Limits.MaxBuildings, patch.MaxBuildings)
	setLimit(&p.Limits.MaxImagesPerRoom, patch.MaxImagesPerRoom)
	setLimit(&p.Limits.MaxActiveContracts, patch.MaxActiveContracts)
	setLimit(&p.Limits.MaxPendingRegistrations, patch.MaxPendingRegistrations)
	if patch.SearchPriority != nil {
		p.SearchPriority = *patch.SearchPriority
	}
	if patch.Features != nil {
		p.Features = *patch.Features
		if p.Features == nil {
			p.Features = []domain.Feature{}
		}
	}
	if patch.IsDefault != nil {
		p.IsDefault = *patch.IsDefault
	}
	if patch.IsActive != nil {
		p.IsActive = *patch.IsActive
	}
	if patch.DisplayOrder != nil {
		p.DisplayOrder = *patch.DisplayOrder
	}
	return p
}

// setLimit writes a limit the patch carries; null clears it (unlimited).
func setLimit(dst **int, o Optional[int]) {
	if o.Set {
		*dst = o.Value
	}
}

// codeTaken turns a unique violation on the code into the domain's error.
func codeTaken(err error) error {
	var ce *shared.ConstraintError
	if errors.As(err, &ce) && ce.Kind == shared.Unique && ce.Constraint == planCodeConstraint {
		return domain.ErrCodeTaken
	}
	return err
}
