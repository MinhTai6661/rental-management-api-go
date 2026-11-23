package location

import (
	"context"

	domain "rental-management/internal/domain/location"
)

// The interfaces below are what the location use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// LocationRepository reads and writes the province and ward tables. Listings come back in alphabetical order.
type LocationRepository interface {
	Provinces(ctx context.Context) ([]domain.Division, error)
	ProvinceExists(ctx context.Context, code int32) (bool, error)
	WardsOfProvince(ctx context.Context, provinceCode int32) ([]domain.Division, error)
	// UpsertProvinces inserts the provinces that are missing and updates the ones that changed.
	UpsertProvinces(ctx context.Context, provinces []domain.Province) error
	// UpsertWards does the same for wards; the provinces they belong to must exist.
	UpsertWards(ctx context.Context, wards []domain.Ward) error
}

// SnapshotSource provides the reference data a seed loads. It returns data that already passed Snapshot.Validate.
type SnapshotSource interface {
	Load() (domain.Snapshot, error)
}
