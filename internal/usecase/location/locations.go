// Package location holds the use cases of the administrative divisions: listing them and seeding them.
package location

import (
	"context"

	domain "rental-management/internal/domain/location"
)

// Locations lists the provinces and the wards in them.
type Locations struct{ repo LocationRepository }

// NewLocations returns the use cases on the repository.
func NewLocations(repo LocationRepository) *Locations { return &Locations{repo: repo} }

// Provinces lists every province in alphabetical order.
func (l *Locations) Provinces(ctx context.Context) ([]domain.Division, error) {
	return l.repo.Provinces(ctx)
}

// Wards lists the wards of a province in alphabetical order, or fails with domain.ErrProvinceNotFound.
func (l *Locations) Wards(ctx context.Context, provinceCode int32) ([]domain.Division, error) {
	exists, err := l.repo.ProvinceExists(ctx, provinceCode)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrProvinceNotFound
	}
	return l.repo.WardsOfProvince(ctx, provinceCode)
}
