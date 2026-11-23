package property

import (
	"context"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Buildings is the use cases of a landlord's buildings.
type Buildings struct{ *core }

// NewBuildings builds the building use cases.
func NewBuildings(d Deps, o Options) *Buildings { return &Buildings{newCore(d, o)} }

// List returns a page of the landlord's buildings and the total.
func (b *Buildings) List(ctx context.Context, landlordID uuid.UUID, f BuildingFilter, limit, offset int) ([]domain.BuildingSummary, int, error) {
	var nameLike *string
	if name := strings.ToLower(strings.TrimSpace(f.Name)); name != "" {
		like := domain.EscapeLike(name)
		nameLike = &like
	}
	return b.buildings.Search(ctx, landlordID, domain.BuildingQuery{
		NameLike: nameLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, Sort: f.Sort, Limit: limit, Offset: offset,
	})
}

// Get returns one building with its charges and pictures; domain.ErrNotFound when it is not the landlord's.
func (b *Buildings) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.BuildingDetail, error) {
	summary, err := b.buildings.Get(ctx, landlordID, id)
	if err != nil {
		return domain.BuildingDetail{}, notFound(err)
	}
	charges, err := b.charges.OfBuilding(ctx, id)
	if err != nil {
		return domain.BuildingDetail{}, err
	}
	media, err := b.media.OfOwner(ctx, domain.BuildingMedia(id))
	if err != nil {
		return domain.BuildingDetail{}, err
	}
	return domain.BuildingDetail{BuildingSummary: summary, Charges: charges, Media: media}, nil
}
