package adminpg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
)

// SettingRepository stores the overrides of the system settings (numbers kept as JSON).
type SettingRepository struct{ pool *pgxpool.Pool }

// NewSettingRepository returns a repository on the pool.
func NewSettingRepository(pool *pgxpool.Pool) *SettingRepository {
	return &SettingRepository{pool: pool}
}

// Overrides returns every setting that was changed, by key.
func (r *SettingRepository) Overrides(ctx context.Context) (map[string]float64, error) {
	rows, err := postgres.Queries(ctx, r.pool).AdminListAppSettings(ctx)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[string]float64, len(rows))
	for _, v := range rows {
		var n float64
		if err := json.Unmarshal(v.Value, &n); err != nil {
			continue // a value this code cannot read counts as not overridden
		}
		out[v.Key] = n
	}
	return out, nil
}

// Set overrides a setting.
func (r *SettingRepository) Set(ctx context.Context, key string, value float64, by uuid.UUID, now time.Time) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminUpsertAppSetting(ctx, sqlcgen.AdminUpsertAppSettingParams{
		Key: key, Value: raw, UpdatedByID: &by, Now: now,
	}))
}

// Reset puts a setting back to its default.
func (r *SettingRepository) Reset(ctx context.Context, key string) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminDeleteAppSetting(ctx, key))
}
