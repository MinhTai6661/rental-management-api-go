package planpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/plan"
)

// SubscriptionRepository reads and writes subscriptions.
type SubscriptionRepository struct{ pool *pgxpool.Pool }

// NewSubscriptionRepository returns a repository on the pool.
func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{pool: pool}
}

// Insert stores a subscription and returns its id.
func (r *SubscriptionRepository) Insert(ctx context.Context, s domain.Subscription) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertSubscription(ctx, sqlcgen.InsertSubscriptionParams{
		UserID: s.UserID, PlanID: s.PlanID, Audience: string(s.Audience), Status: string(s.Status), StartsAt: s.StartsAt,
		EndsAt: s.EndsAt, GrantedByID: s.GrantedByID,
	})
	return id, postgres.Translate(err)
}

// Get returns a subscription with its plan and owner.
func (r *SubscriptionRepository) Get(ctx context.Context, id uuid.UUID) (domain.Detail, error) {
	row, err := postgres.Queries(ctx, r.pool).GetSubscriptionDetail(ctx, id)
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return detailOf(row.Subscription, row.Plan, row.UserFullName, row.UserEmail), nil
}

// Lock returns a subscription and locks it until the transaction ends.
func (r *SubscriptionRepository) Lock(ctx context.Context, id uuid.UUID) (domain.Subscription, error) {
	row, err := postgres.Queries(ctx, r.pool).LockSubscription(ctx, id)
	if err != nil {
		return domain.Subscription{}, postgres.Translate(err)
	}
	return domain.Subscription{
		ID: row.ID, UserID: row.UserID, PlanID: row.PlanID, Audience: domain.Audience(row.Audience), Status: domain.Status(row.Status),
		StartsAt: row.StartsAt, EndsAt: row.EndsAt, CanceledAt: row.CanceledAt, GrantedByID: row.GrantedByID, CreatedAt: row.CreatedAt,
	}, nil
}

// Running returns the subscription that counts at now, with its plan.
func (r *SubscriptionRepository) Running(ctx context.Context, userID uuid.UUID, audience domain.Audience, now time.Time) (domain.Detail, error) {
	row, err := postgres.Queries(ctx, r.pool).GetRunningSubscription(ctx, sqlcgen.GetRunningSubscriptionParams{
		UserID: userID, Audience: string(audience), Now: now,
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return detailOf(row.Subscription, row.Plan, "", nil), nil
}

// RunningPriorities returns the search priority of the plan each user is running at now on the audience's side.
func (r *SubscriptionRepository) RunningPriorities(ctx context.Context, userIDs []uuid.UUID, audience domain.Audience, now time.Time) (map[uuid.UUID]int, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRunningPriorities(ctx, sqlcgen.ListRunningPrioritiesParams{
		UserIds: userIDs, Audience: string(audience), Now: now,
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]int, len(rows))
	for _, row := range rows {
		out[row.UserID] = int(row.SearchPriority)
	}
	return out, nil
}

// Pending returns the user's request waiting for an admin, with its plan.
func (r *SubscriptionRepository) Pending(ctx context.Context, userID uuid.UUID, audience domain.Audience) (domain.Detail, error) {
	row, err := postgres.Queries(ctx, r.pool).GetPendingSubscription(ctx, sqlcgen.GetPendingSubscriptionParams{
		UserID: userID, Audience: string(audience),
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return detailOf(row.Subscription, row.Plan, "", nil), nil
}

// CancelPending cancels the pending requests of the user for the audience.
func (r *SubscriptionRepository) CancelPending(ctx context.Context, userID uuid.UUID, audience domain.Audience, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).CancelPendingSubscriptions(ctx, sqlcgen.CancelPendingSubscriptionsParams{
		Now: &now, UserID: userID, Audience: string(audience),
	}))
}

// EndOpen cancels the active and pending subscriptions of the user but exceptID and returns how many it ended.
func (r *SubscriptionRepository) EndOpen(ctx context.Context, userID uuid.UUID, audience *domain.Audience, exceptID uuid.UUID, now time.Time) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).EndOpenSubscriptions(ctx, sqlcgen.EndOpenSubscriptionsParams{
		Now: &now, UserID: userID, ExceptID: exceptID, Audience: audienceArg(audience),
	})
	return int(n), postgres.Translate(err)
}

// Activate turns a pending subscription into the running one.
func (r *SubscriptionRepository) Activate(ctx context.Context, id uuid.UUID, startsAt time.Time, endsAt *time.Time, grantedBy uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).ActivateSubscription(ctx, sqlcgen.ActivateSubscriptionParams{
		ID: id, StartsAt: startsAt, EndsAt: endsAt, GrantedByID: &grantedBy,
	}))
}

// Search returns a page of subscriptions, newest first, and how many match in all.
func (r *SubscriptionRepository) Search(ctx context.Context, q domain.SubscriptionQuery) ([]domain.Detail, int, error) {
	queries := postgres.Queries(ctx, r.pool)
	var status *string
	if q.Status != nil {
		s := string(*q.Status)
		status = &s
	}
	rows, err := queries.SearchSubscriptions(ctx, sqlcgen.SearchSubscriptionsParams{
		Status: status, Audience: audienceArg(q.Audience), UserID: q.UserID,
		PageLimit: int32(q.Limit), PageOffset: int32(q.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := queries.CountSubscriptions(ctx, sqlcgen.CountSubscriptionsParams{Status: status, Audience: audienceArg(q.Audience), UserID: q.UserID})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Detail, len(rows))
	for i, row := range rows {
		out[i] = detailOf(row.Subscription, row.Plan, row.UserFullName, row.UserEmail)
	}
	return out, int(total), nil
}

// ExpireDue marks up to limit active subscriptions past their end as expired and returns their ids.
func (r *SubscriptionRepository) ExpireDue(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ExpireDueSubscriptions(ctx, sqlcgen.ExpireDueSubscriptionsParams{
		Now: &now, BatchSize: int32(limit), //nolint:gosec // a small batch size
	})
	return ids, postgres.Translate(err)
}
