//go:build integration

package postgres

import (
	"context"
	"testing"

	"rental-management/internal/adapter/snapshot/locationdata"
)

func TestSeedLoadsTheSnapshotAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pg.NewDB(t)
	repo := NewLocationRepository(pool)

	snap, err := locationdata.Source{}.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		err := NewTxManager(pool).WithinTx(ctx, func(ctx context.Context) error {
			if err := repo.UpsertProvinces(ctx, snap.Provinces); err != nil {
				return err
			}
			return repo.UpsertWards(ctx, snap.Wards)
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
		var provinces, wards int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM provinces), (SELECT count(*) FROM wards)`).
			Scan(&provinces, &wards); err != nil {
			t.Fatal(err)
		}
		if provinces != len(snap.Provinces) || wards != len(snap.Wards) {
			t.Fatalf("after seed %d: %d provinces, %d wards; snapshot has %d and %d",
				i+1, provinces, wards, len(snap.Provinces), len(snap.Wards))
		}
	}

	items, err := repo.Provinces(ctx)
	if err != nil || len(items) != len(snap.Provinces) {
		t.Fatalf("Provinces: %d items, %v", len(items), err)
	}
	// Ho Chi Minh City is code 79 and has wards; a made-up code has no province.
	if ok, err := repo.ProvinceExists(ctx, 79); err != nil || !ok {
		t.Fatalf("ProvinceExists(79) = %v, %v", ok, err)
	}
	if ok, err := repo.ProvinceExists(ctx, 999999); err != nil || ok {
		t.Fatalf("ProvinceExists(999999) = %v, %v", ok, err)
	}
	wards, err := repo.WardsOfProvince(ctx, 79)
	if err != nil || len(wards) == 0 {
		t.Fatalf("WardsOfProvince(79): %d items, %v", len(wards), err)
	}
}
