// Package plan holds the use cases of service plans: the catalog, the subscriptions that put a user on a plan, the
// limits a plan sets and the daily expiry. It declares the ports it needs (ports.go) and imports no framework,
// driver or other context: the property and contract contexts read the limits through ports of their own, which
// cmd/rental-management adapts to the Limits use case here.
package plan

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx       TxManager
	Plans    PlanRepository
	Subs     SubscriptionRepository
	Accounts Accounts
	Usage    UsageReader
	Notifier Notifier
	Audit    AuditRecorder
	Clock    Clock
	Log      *slog.Logger
}

// core is what the use case groups share: the ports and the helpers every group needs.
type core struct {
	tx       TxManager
	plans    PlanRepository
	subs     SubscriptionRepository
	accounts Accounts
	usage    UsageReader
	notifier Notifier
	audit    AuditRecorder
	clock    Clock
	log      *slog.Logger
}

func newCore(d Deps) *core {
	c := &core{
		tx: d.Tx, plans: d.Plans, subs: d.Subs, accounts: d.Accounts, usage: d.Usage, notifier: d.Notifier,
		audit: d.Audit, clock: d.Clock, log: d.Log,
	}
	if c.notifier == nil {
		c.notifier = NoNotifier{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	return c
}

// lockAccount locks the account whose plans change and returns it (domain.ErrAccountNotFound when it is gone).
func (c *core) lockAccount(ctx context.Context, id uuid.UUID) (domain.Account, error) {
	a, err := c.accounts.Lock(ctx, id)
	if errors.Is(err, shared.ErrNotFound) {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return a, err
}

// choosePlan reads the plan a request names in its body (domain.ErrPlanUnknown when there is none).
func (c *core) choosePlan(ctx context.Context, id uuid.UUID) (domain.Plan, error) {
	p, err := c.plans.Get(ctx, id)
	if errors.Is(err, shared.ErrNotFound) {
		return domain.Plan{}, domain.ErrPlanUnknown
	}
	return p, err
}

// effective is the plan the account is on for the audience: its running subscription's plan, else the default of the
// audience. nil means no plan at all, and then nothing is limited.
func (c *core) effective(ctx context.Context, userID uuid.UUID, audience domain.Audience) (*domain.Plan, error) {
	var running, def *domain.Plan
	d, err := c.subs.Running(ctx, userID, audience, c.clock.Now())
	switch {
	case err == nil:
		running = &d.Plan
	case !errors.Is(err, shared.ErrNotFound):
		return nil, err
	}
	if running == nil {
		p, err := c.plans.Default(ctx, audience)
		switch {
		case err == nil:
			def = &p
		case !errors.Is(err, shared.ErrNotFound):
			return nil, err
		}
	}
	return domain.EffectivePlan(running, def), nil
}

// give puts the user on the plan right now, replacing whatever they had for its audience. The caller has locked the
// account and checked that the plan may be given.
// actor is who did it for the audit trail (uuid.Nil: the system); grantedBy is the admin who handed the plan out.
func (c *core) give(ctx context.Context, actor uuid.UUID, grantedBy *uuid.UUID, userID uuid.UUID, p domain.Plan, end *time.Time, action string) (domain.Detail, error) {
	now := c.clock.Now()
	aud := p.Audience
	if _, err := c.subs.EndOpen(ctx, userID, &aud, uuid.Nil, now); err != nil {
		return domain.Detail{}, err
	}
	sub := domain.Subscription{
		UserID: userID, PlanID: p.ID, Audience: p.Audience, Status: domain.StatusActive, StartsAt: now,
		EndsAt: domain.EndOf(p, now, end), CreatedAt: now,
	}
	if grantedBy != nil && *grantedBy != uuid.Nil {
		sub.GrantedByID = grantedBy
	}
	id, err := c.subs.Insert(ctx, sub)
	if err != nil {
		return domain.Detail{}, err
	}
	sub.ID = id
	meta := map[string]any{"plan": p.Code, "ends_at": timeMeta(sub.EndsAt)}
	if sub.GrantedByID != nil {
		meta["granted_by_id"] = sub.GrantedByID.String()
	}
	if err := c.audit.Record(ctx, AuditEvent{Action: action, ActorID: actor, TargetUserID: &userID, Meta: meta}); err != nil {
		return domain.Detail{}, err
	}
	return domain.Detail{Subscription: sub, Plan: p}, nil
}

// timeMeta writes an optional time for an audit event.
func timeMeta(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// tell sends a notification after the change is stored: a failure is logged and never undoes the change.
func (c *core) tell(ctx context.Context, n Notification) {
	if err := c.notifier.Notify(ctx, n); err != nil {
		c.log.Warn("notify about a subscription", "error", err, "kind", n.Kind)
	}
}
