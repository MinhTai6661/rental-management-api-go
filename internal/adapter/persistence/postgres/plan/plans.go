package planpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/plan"
)

// PlanRepository reads and writes the plan catalog.
type PlanRepository struct{ pool *pgxpool.Pool }

// NewPlanRepository returns a repository on the pool.
func NewPlanRepository(pool *pgxpool.Pool) *PlanRepository { return &PlanRepository{pool: pool} }

func audienceArg(a *domain.Audience) *string {
	if a == nil {
		return nil
	}
	s := string(*a)
	return &s
}

// Search returns a page of plans and how many match in all.
func (r *PlanRepository) Search(ctx context.Context, q domain.PlanQuery) ([]domain.Plan, int, error) {
	queries := postgres.Queries(ctx, r.pool)
	rows, err := queries.SearchPlans(ctx, sqlcgen.SearchPlansParams{
		Audience: audienceArg(q.Audience), ActiveOnly: q.ActiveOnly,
		PageLimit: int32(q.Limit), PageOffset: int32(q.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := queries.CountPlans(ctx, sqlcgen.CountPlansParams{Audience: audienceArg(q.Audience), ActiveOnly: q.ActiveOnly})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Plan, len(rows))
	for i, row := range rows {
		out[i] = planOf(row)
	}
	return out, int(total), nil
}

// Get returns a plan by id.
func (r *PlanRepository) Get(ctx context.Context, id uuid.UUID) (domain.Plan, error) {
	row, err := postgres.Queries(ctx, r.pool).GetPlan(ctx, id)
	return planOf(row), postgres.Translate(err)
}

// GetByCode returns a plan by code.
func (r *PlanRepository) GetByCode(ctx context.Context, code string) (domain.Plan, error) {
	row, err := postgres.Queries(ctx, r.pool).GetPlanByCode(ctx, code)
	return planOf(row), postgres.Translate(err)
}

// Default returns the active default plan of the audience.
func (r *PlanRepository) Default(ctx context.Context, audience domain.Audience) (domain.Plan, error) {
	row, err := postgres.Queries(ctx, r.pool).GetDefaultPlan(ctx, string(audience))
	return planOf(row), postgres.Translate(err)
}

// Insert stores a plan and returns its id.
func (r *PlanRepository) Insert(ctx context.Context, p domain.Plan) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertPlan(ctx, sqlcgen.InsertPlanParams{
		Code: p.Code, Name: p.Name, Description: p.Description, Audience: string(p.Audience), PriceVnd: p.PriceVND,
		BillingInterval: string(p.Interval), MaxRooms: toInt32(p.Limits.MaxRooms), MaxBuildings: toInt32(p.Limits.MaxBuildings),
		MaxImagesPerRoom: toInt32(p.Limits.MaxImagesPerRoom), MaxActiveContracts: toInt32(p.Limits.MaxActiveContracts),
		MaxPendingRegistrations: toInt32(p.Limits.MaxPendingRegistrations), SearchPriority: int32(p.SearchPriority), //nolint:gosec // 0..65535
		Features: featureCodes(p.Features), IsDefault: p.IsDefault, IsActive: p.IsActive, DisplayOrder: int32(p.DisplayOrder), //nolint:gosec // 0..65535
	})
	return id, postgres.Translate(err)
}

// Update writes every field a super admin can change.
func (r *PlanRepository) Update(ctx context.Context, p domain.Plan) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdatePlan(ctx, sqlcgen.UpdatePlanParams{
		ID: p.ID, Code: p.Code, Name: p.Name, Description: p.Description, PriceVnd: p.PriceVND, BillingInterval: string(p.Interval),
		MaxRooms: toInt32(p.Limits.MaxRooms), MaxBuildings: toInt32(p.Limits.MaxBuildings),
		MaxImagesPerRoom: toInt32(p.Limits.MaxImagesPerRoom), MaxActiveContracts: toInt32(p.Limits.MaxActiveContracts),
		MaxPendingRegistrations: toInt32(p.Limits.MaxPendingRegistrations), SearchPriority: int32(p.SearchPriority), //nolint:gosec // 0..65535
		Features: featureCodes(p.Features), IsDefault: p.IsDefault, IsActive: p.IsActive, DisplayOrder: int32(p.DisplayOrder), //nolint:gosec // 0..65535
	}))
}

// ClearDefault takes the default flag off the plans of the audience but exceptID.
func (r *PlanRepository) ClearDefault(ctx context.Context, audience domain.Audience, exceptID uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).ClearDefaultPlan(ctx, sqlcgen.ClearDefaultPlanParams{
		Audience: string(audience), ExceptID: exceptID,
	}))
}
