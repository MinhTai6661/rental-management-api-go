package locationpg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/location"
)

// wardBatchSize is how many wards one INSERT carries.
const wardBatchSize = 1000

// LocationRepository reads and writes the provinces and wards.
type LocationRepository struct{ pool *pgxpool.Pool }

// NewLocationRepository returns a repository on the pool.
func NewLocationRepository(pool *pgxpool.Pool) *LocationRepository {
	return &LocationRepository{pool: pool}
}

// Provinces lists every province in alphabetical order.
func (r *LocationRepository) Provinces(ctx context.Context) ([]domain.Division, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListProvinces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provinces: %w", postgres.Translate(err))
	}
	items := make([]domain.Division, len(rows))
	for i, row := range rows {
		items[i] = domain.Division{Code: row.Code, Name: row.Name}
	}
	return items, nil
}

// ProvinceExists tells whether there is a province with the code.
func (r *LocationRepository) ProvinceExists(ctx context.Context, code int32) (bool, error) {
	exists, err := postgres.Queries(ctx, r.pool).ProvinceExists(ctx, code)
	if err != nil {
		return false, fmt.Errorf("check province: %w", postgres.Translate(err))
	}
	return exists, nil
}

// WardsOfProvince lists the wards of a province in alphabetical order (an empty list when it has none).
func (r *LocationRepository) WardsOfProvince(ctx context.Context, provinceCode int32) ([]domain.Division, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListWardsOfProvince(ctx, provinceCode)
	if err != nil {
		return nil, fmt.Errorf("list wards: %w", postgres.Translate(err))
	}
	items := make([]domain.Division, len(rows))
	for i, row := range rows {
		items[i] = domain.Division{Code: row.Code, Name: row.Name}
	}
	return items, nil
}

// UpsertProvinces inserts the provinces that are missing and updates the ones that changed.
func (r *LocationRepository) UpsertProvinces(ctx context.Context, provinces []domain.Province) error {
	p := sqlcgen.UpsertProvincesParams{}
	for _, v := range provinces {
		p.Codes = append(p.Codes, v.Code)
		p.Names = append(p.Names, v.Name)
		p.AdministrativeLevels = append(p.AdministrativeLevels, v.AdministrativeLevel)
		p.Slugs = append(p.Slugs, v.Slug)
		p.AreaPhoneCodes = append(p.AreaPhoneCodes, v.AreaPhoneCode)
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpsertProvinces(ctx, p))
}

// UpsertWards inserts the wards that are missing and updates the ones that changed, wardBatchSize at a time.
func (r *LocationRepository) UpsertWards(ctx context.Context, wards []domain.Ward) error {
	q := postgres.Queries(ctx, r.pool)
	for _, batch := range wardBatches(wards, wardBatchSize) {
		w := sqlcgen.UpsertWardsParams{}
		for _, v := range batch {
			w.Codes = append(w.Codes, v.Code)
			w.ProvinceCodes = append(w.ProvinceCodes, v.ProvinceCode)
			w.Names = append(w.Names, v.Name)
			w.AdministrativeLevels = append(w.AdministrativeLevels, v.AdministrativeLevel)
			w.Slugs = append(w.Slugs, v.Slug)
		}
		if err := q.UpsertWards(ctx, w); err != nil {
			return postgres.Translate(err)
		}
	}
	return nil
}

// wardBatches splits the wards into slices of at most size, keeping their order.
func wardBatches(wards []domain.Ward, size int) [][]domain.Ward {
	var batches [][]domain.Ward
	for len(wards) > size {
		batches = append(batches, wards[:size])
		wards = wards[size:]
	}
	if len(wards) > 0 {
		batches = append(batches, wards)
	}
	return batches
}
