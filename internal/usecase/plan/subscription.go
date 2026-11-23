package plan

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// expireBatch is how many lapsed subscriptions one transaction of the expiry marks.
const expireBatch = 200

// Mine is a user's own plan: what they are on, the running subscription, a paid request waiting for an admin, and
// what they have used of the plan's limits.
type Mine struct {
	// Plan is nil when there is no plan at all (nothing seeded).
	Plan         *domain.Plan
	Subscription *domain.Detail
	Pending      *domain.Detail
	// Usage counts what the plan limits: rooms, buildings and active_contracts for a landlord,
	// pending_registrations for everybody else.
	Usage map[string]int
}

// Subscriptions puts users on plans, ends their plans and lets admins hand plans out.
type Subscriptions struct{ *core }

// NewSubscriptions returns the subscription use cases.
func NewSubscriptions(d Deps) *Subscriptions { return &Subscriptions{core: newCore(d)} }

// Mine returns the user's plan for the side they act on (role).
func (s *Subscriptions) Mine(ctx context.Context, userID uuid.UUID, role string) (Mine, error) {
	audience := domain.AudienceOfRole(role)
	out := Mine{Usage: map[string]int{}}
	var err error
	if out.Plan, err = s.effective(ctx, userID, audience); err != nil {
		return Mine{}, err
	}
	if out.Subscription, err = optionalDetail(s.subs.Running(ctx, userID, audience, s.clock.Now())); err != nil {
		return Mine{}, err
	}
	if out.Pending, err = optionalDetail(s.subs.Pending(ctx, userID, audience)); err != nil {
		return Mine{}, err
	}
	if audience == domain.AudienceLandlord {
		u, err := s.usage.Landlord(ctx, userID)
		if err != nil {
			return Mine{}, err
		}
		out.Usage["rooms"], out.Usage["buildings"], out.Usage["active_contracts"] = u.Rooms, u.Buildings, u.ActiveContracts
		return out, nil
	}
	if out.Usage["pending_registrations"], err = s.usage.PendingRegistrations(ctx, userID); err != nil {
		return Mine{}, err
	}
	return out, nil
}

// optionalDetail turns "no such subscription" into nil.
func optionalDetail(d domain.Detail, err error) (*domain.Detail, error) {
	switch {
	case err == nil:
		return &d, nil
	case errors.Is(err, shared.ErrNotFound):
		return nil, nil
	}
	return nil, err
}

// Subscribe is a user asking for a plan. A free plan takes effect at once; a paid one becomes a pending request
// until an admin activates it once the payment arrived (there is no payment gateway). role is the role the request
// acts in: a user acting as a tenant cannot take a landlord plan.
func (s *Subscriptions) Subscribe(ctx context.Context, userID uuid.UUID, role string, planID uuid.UUID) (domain.Detail, error) {
	var out domain.Detail
	var userName string
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		account, err := s.lockAccount(ctx, userID)
		if err != nil {
			return err
		}
		userName = account.Name
		p, err := s.choosePlan(ctx, planID)
		if err != nil {
			return err
		}
		if err := domain.CheckAssignable(role, account.Roles, p.Audience, p.IsActive); err != nil {
			return err
		}
		current, err := s.effective(ctx, userID, p.Audience)
		if err != nil {
			return err
		}
		if current != nil && current.ID == p.ID {
			return domain.ErrAlreadyOnPlan
		}
		if p.IsFree() {
			out, err = s.give(ctx, userID, nil, userID, p, nil, AuditSubscriptionAssigned)
			return err
		}
		out, err = s.request(ctx, userID, p)
		return err
	})
	if err != nil {
		return domain.Detail{}, err
	}
	if out.Status == domain.StatusPending {
		s.tell(ctx, Notification{
			ToAdmins: true, Kind: NotifySubscriptionRequested, URL: "/admin/users",
			Params: map[string]string{"user": userName, "plan": out.Plan.Name},
		})
	}
	return out, nil
}

// request stores a paid plan as a pending request, replacing an earlier one.
func (s *Subscriptions) request(ctx context.Context, userID uuid.UUID, p domain.Plan) (domain.Detail, error) {
	now := s.clock.Now()
	if err := s.subs.CancelPending(ctx, userID, p.Audience, now); err != nil {
		return domain.Detail{}, err
	}
	sub := domain.Subscription{
		UserID: userID, PlanID: p.ID, Audience: p.Audience, Status: domain.StatusPending, StartsAt: now, CreatedAt: now,
	}
	id, err := s.subs.Insert(ctx, sub)
	if err != nil {
		return domain.Detail{}, err
	}
	sub.ID = id
	err = s.audit.Record(ctx, AuditEvent{
		Action: AuditSubscriptionRequested, ActorID: userID, TargetUserID: &userID,
		Meta: map[string]any{"plan": p.Code, "price_vnd": p.PriceVND},
	})
	return domain.Detail{Subscription: sub, Plan: p}, err
}

// Cancel ends the user's running plan and open request for the side they act on: they fall back to the default plan.
func (s *Subscriptions) Cancel(ctx context.Context, userID uuid.UUID, role string) error {
	audience := domain.AudienceOfRole(role)
	return s.end(ctx, userID, userID, &audience)
}

// Revoke ends every running plan and open request of the user (an admin's action).
func (s *Subscriptions) Revoke(ctx context.Context, actor, userID uuid.UUID) error {
	return s.end(ctx, actor, userID, nil)
}

// end cancels the open subscriptions of the user for one audience, or for all when none is given.
func (s *Subscriptions) end(ctx context.Context, actor, userID uuid.UUID, audience *domain.Audience) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockAccount(ctx, userID); err != nil {
			return err
		}
		ended, err := s.subs.EndOpen(ctx, userID, audience, uuid.Nil, s.clock.Now())
		if err != nil || ended == 0 {
			return err
		}
		which := "all"
		if audience != nil {
			which = string(*audience)
		}
		return s.audit.Record(ctx, AuditEvent{
			Action: AuditSubscriptionCanceled, ActorID: actor, TargetUserID: &userID,
			Meta: map[string]any{"audience": which, "ended": ended},
		})
	})
}

// Assign puts a user on a plan right now (an admin's action), replacing what they had. A paid plan lasts one billing
// interval unless endsAt is given.
func (s *Subscriptions) Assign(ctx context.Context, actor, userID, planID uuid.UUID, endsAt *time.Time) (domain.Detail, error) {
	if err := domain.CheckEnd(s.clock.Now(), endsAt); err != nil {
		return domain.Detail{}, err
	}
	var out domain.Detail
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		account, err := s.lockAccount(ctx, userID)
		if err != nil {
			return err
		}
		p, err := s.choosePlan(ctx, planID)
		if err != nil {
			return err
		}
		// Nobody acts for the account, so only the role it holds decides.
		if err := domain.CheckAssignable("", account.Roles, p.Audience, p.IsActive); err != nil {
			return err
		}
		out, err = s.give(ctx, actor, &actor, userID, p, endsAt, AuditSubscriptionAssigned)
		return err
	})
	return out, err
}

// Activate turns a pending request into the user's running subscription once the payment arrived (an admin's action).
func (s *Subscriptions) Activate(ctx context.Context, actor, id uuid.UUID) (domain.Detail, error) {
	// The account is locked before the subscription (as everywhere else), so read the owner first.
	first, err := s.subs.Get(ctx, id)
	if errors.Is(err, shared.ErrNotFound) {
		return domain.Detail{}, domain.ErrSubscriptionNotFound
	}
	if err != nil {
		return domain.Detail{}, err
	}
	var out domain.Detail
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockAccount(ctx, first.UserID); err != nil {
			return err
		}
		sub, err := s.subs.Lock(ctx, id)
		if errors.Is(err, shared.ErrNotFound) {
			return domain.ErrSubscriptionNotFound
		}
		if err != nil {
			return err
		}
		if sub.Status != domain.StatusPending {
			return domain.ErrNotPending
		}
		now := s.clock.Now()
		if _, err := s.subs.EndOpen(ctx, sub.UserID, &sub.Audience, sub.ID, now); err != nil {
			return err
		}
		end := domain.EndOf(first.Plan, now, nil)
		if err := s.subs.Activate(ctx, sub.ID, now, end, actor); err != nil {
			return err
		}
		if out, err = s.subs.Get(ctx, id); err != nil {
			return err
		}
		return s.audit.Record(ctx, AuditEvent{
			Action: AuditSubscriptionActivated, ActorID: actor, TargetUserID: &sub.UserID,
			Meta: map[string]any{"plan": first.Plan.Code, "ends_at": timeMeta(end), "granted_by_id": actor.String()},
		})
	})
	if err != nil {
		return domain.Detail{}, err
	}
	s.tell(ctx, Notification{
		UserID: out.UserID, Kind: NotifySubscriptionActivated, URL: "/plans", Audience: string(out.Audience),
		Params: map[string]string{"plan": out.Plan.Name},
	})
	return out, nil
}

// Search returns a page of subscriptions, newest first (an admin's list).
func (s *Subscriptions) Search(ctx context.Context, q domain.SubscriptionQuery) ([]domain.Detail, int, error) {
	return s.subs.Search(ctx, q)
}

// ExpireDue marks the active subscriptions that ran past their end as expired and returns how many it did. Those
// users fall back to the default plan (a lapsed subscription is ignored even before this runs). It is safe to run
// again and to run twice at once.
func (s *Subscriptions) ExpireDue(ctx context.Context) (int, error) {
	now, total := s.clock.Now(), 0
	for {
		var ids []uuid.UUID
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			var err error
			if ids, err = s.subs.ExpireDue(ctx, now, expireBatch); err != nil || len(ids) == 0 {
				return err
			}
			list := make([]string, len(ids))
			for i, id := range ids {
				list[i] = id.String()
			}
			return s.audit.Record(ctx, AuditEvent{
				Action: AuditSubscriptionsExpired, Meta: map[string]any{"count": len(ids), "subscription_ids": list},
			})
		})
		if err != nil {
			return total, err
		}
		total += len(ids)
		if len(ids) < expireBatch {
			return total, nil
		}
	}
}
