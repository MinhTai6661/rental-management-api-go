package listingpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

// OfferRepository is the read-only search of find rooms: the rooms that are vacant and have an approved listing.
type OfferRepository struct{ pool *pgxpool.Pool }

// NewOfferRepository returns a repository on the pool.
func NewOfferRepository(pool *pgxpool.Pool) *OfferRepository { return &OfferRepository{pool: pool} }

// Search returns a page of offered rooms and how many match in all.
func (r *OfferRepository) Search(ctx context.Context, f domain.OfferQuery) ([]domain.Offer, int, error) {
	q := postgres.Queries(ctx, r.pool)
	ids := make([]uuid.UUID, len(f.Priorities))
	values := make([]int32, len(f.Priorities))
	for i, p := range f.Priorities {
		ids[i], values[i] = p.LandlordID, int32(p.Value) //nolint:gosec // a plan priority is at most 65535
	}
	rows, err := q.SearchOffers(ctx, sqlcgen.SearchOffersParams{
		LandlordID: f.LandlordID, HouseLike: f.HouseLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
		PriceFrom: f.PriceFrom, PriceTo: f.PriceTo, AreaFrom: f.AreaFrom, AreaTo: f.AreaTo, Sort: f.Sort,
		PriorityIds: ids, PriorityValues: values,
		PageLimit: int32(f.Limit), PageOffset: int32(f.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountOffers(ctx, sqlcgen.CountOffersParams{
		LandlordID: f.LandlordID, HouseLike: f.HouseLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
		PriceFrom: f.PriceFrom, PriceTo: f.PriceTo, AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Offer, len(rows))
	for i, row := range rows {
		out[i] = offerOf(row)
	}
	return out, int(total), nil
}

// Landlords returns the landlords with at least one offered room matching the filters of q.
func (r *OfferRepository) Landlords(ctx context.Context, f domain.OfferQuery) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ListOfferLandlords(ctx, sqlcgen.ListOfferLandlordsParams{
		LandlordID: f.LandlordID, HouseLike: f.HouseLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
		PriceFrom: f.PriceFrom, PriceTo: f.PriceTo, AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
	})
	return ids, postgres.Translate(err)
}

// Get returns an offered room, or shared.ErrNotFound when it is not offered.
func (r *OfferRepository) Get(ctx context.Context, id uuid.UUID) (domain.Offer, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchOffers(ctx, sqlcgen.SearchOffersParams{ID: &id, PageLimit: 1})
	if err != nil {
		return domain.Offer{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.Offer{}, shared.ErrNotFound
	}
	return offerOf(rows[0]), nil
}

// Landlord returns an active landlord as a guest may see them: the name, the houses with rooms on find rooms and how
// many rooms are on find rooms. Anybody else is shared.ErrNotFound.
func (r *OfferRepository) Landlord(ctx context.Context, id uuid.UUID) (domain.LandlordCard, error) {
	q := postgres.Queries(ctx, r.pool)
	row, err := q.GetOfferLandlord(ctx, id)
	if err != nil {
		return domain.LandlordCard{}, postgres.Translate(err)
	}
	houses, err := q.ListOfferedHousesOfLandlord(ctx, id)
	if err != nil {
		return domain.LandlordCard{}, postgres.Translate(err)
	}
	count, err := q.CountOffers(ctx, sqlcgen.CountOffersParams{LandlordID: &id})
	if err != nil {
		return domain.LandlordCard{}, postgres.Translate(err)
	}
	if houses == nil {
		houses = []string{}
	}
	return domain.LandlordCard{ID: row.ID, FullName: row.FullName, Houses: houses, VacantRooms: int(count)}, nil
}
