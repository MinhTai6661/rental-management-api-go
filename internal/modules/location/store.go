package location

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/modules/location/sqlcgen"
	"rental-management/internal/platform/db"
)

// Item is a province or a ward as the API lists it.
type Item struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}

// Store reads and writes the location tables.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

// NewStore returns a store on the pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: sqlcgen.New(pool)}
}

// Provinces lists every province in alphabetical order.
func (s *Store) Provinces(ctx context.Context) ([]Item, error) {
	rows, err := s.q.ListProvinces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provinces: %w", err)
	}
	items := make([]Item, len(rows))
	for i, r := range rows {
		items[i] = Item{Code: r.Code, Name: r.Name}
	}
	return items, nil
}

// Wards lists the wards of a province in alphabetical order. found is false when there is no such province.
func (s *Store) Wards(ctx context.Context, provinceCode int32) (items []Item, found bool, err error) {
	exists, err := s.q.ProvinceExists(ctx, provinceCode)
	if err != nil {
		return nil, false, fmt.Errorf("check province: %w", err)
	}
	if !exists {
		return nil, false, nil
	}
	rows, err := s.q.ListWardsOfProvince(ctx, provinceCode)
	if err != nil {
		return nil, false, fmt.Errorf("list wards: %w", err)
	}
	items = make([]Item, len(rows))
	for i, r := range rows {
		items[i] = Item{Code: r.Code, Name: r.Name}
	}
	return items, true, nil
}

// Seed makes the tables match the embedded snapshot: it inserts what is missing and updates what changed.
// It runs in one transaction and is safe to repeat.
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	snap, err := LoadSnapshot()
	if err != nil {
		return err
	}
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return seed(ctx, sqlcgen.New(tx), snap)
	})
}

func seed(ctx context.Context, q *sqlcgen.Queries, snap Snapshot) error {
	p := sqlcgen.UpsertProvincesParams{}
	for _, v := range snap.Provinces {
		p.Codes = append(p.Codes, int32(v.Code)) //nolint:gosec // codes are small numbers
		p.Names = append(p.Names, v.Name)
		p.AdministrativeLevels = append(p.AdministrativeLevels, v.AdministrativeLevel)
		p.Slugs = append(p.Slugs, v.Slug)
		p.AreaPhoneCodes = append(p.AreaPhoneCodes, int16(v.AreaPhoneCode)) //nolint:gosec // phone codes are at most 3 digits
	}
	if err := q.UpsertProvinces(ctx, p); err != nil {
		return fmt.Errorf("seed provinces: %w", err)
	}

	for _, batch := range wardBatches(snap.Wards, batchSize) {
		w := sqlcgen.UpsertWardsParams{}
		for _, v := range batch {
			w.Codes = append(w.Codes, int32(v.Code))                         //nolint:gosec // codes are small numbers
			w.ProvinceCodes = append(w.ProvinceCodes, int32(v.ProvinceCode)) //nolint:gosec // codes are small numbers
			w.Names = append(w.Names, v.Name)
			w.AdministrativeLevels = append(w.AdministrativeLevels, v.AdministrativeLevel)
			w.Slugs = append(w.Slugs, v.Slug)
		}
		if err := q.UpsertWards(ctx, w); err != nil {
			return fmt.Errorf("seed wards: %w", err)
		}
	}
	return nil
}
