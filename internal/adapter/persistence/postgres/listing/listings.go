// Package listingpg is the database side of the listing use cases: the listing repository, the read-only views of
// rooms that listings need and the search of find rooms. It returns domain types and translates driver errors (see
// postgres.Translate).
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

// ListingRepository reads and writes listings.
type ListingRepository struct{ pool *pgxpool.Pool }

// NewListingRepository returns a repository on the pool.
func NewListingRepository(pool *pgxpool.Pool) *ListingRepository {
	return &ListingRepository{pool: pool}
}

func statusPtr(s *domain.Status) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// Search returns a page of listings with their rooms and the people they name, and how many match in all.
func (r *ListingRepository) Search(ctx context.Context, f domain.Query) ([]domain.Detail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.SearchListings(ctx, sqlcgen.SearchListingsParams{
		LandlordID: f.LandlordID, Status: statusPtr(f.Status), ExcludeDrafts: f.ExcludeDrafts, SearchLike: f.SearchLike,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo, Sort: f.Sort,
		PageLimit: int32(f.Limit), PageOffset: int32(f.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountListings(ctx, sqlcgen.CountListingsParams{
		LandlordID: f.LandlordID, Status: statusPtr(f.Status), ExcludeDrafts: f.ExcludeDrafts, SearchLike: f.SearchLike,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Detail, len(rows))
	for i, row := range rows {
		out[i] = detailOf(row)
	}
	return out, int(total), nil
}

// Counts counts the listings per status.
func (r *ListingRepository) Counts(ctx context.Context, landlordID *uuid.UUID, excludeDrafts bool) (domain.StatusCounts, error) {
	rows, err := postgres.Queries(ctx, r.pool).CountListingsByStatus(ctx, sqlcgen.CountListingsByStatusParams{
		LandlordID: landlordID, ExcludeDrafts: excludeDrafts,
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := domain.StatusCounts{}
	for _, row := range rows {
		out[domain.Status(row.Status)] = int(row.Total)
	}
	return out, nil
}

// Get returns one listing in the landlord's scope (nil: every landlord's), or shared.ErrNotFound.
func (r *ListingRepository) Get(ctx context.Context, landlordID *uuid.UUID, id uuid.UUID) (domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchListings(ctx, sqlcgen.SearchListingsParams{
		LandlordID: landlordID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.Detail{}, shared.ErrNotFound
	}
	return detailOf(rows[0]), nil
}

// ByIDs returns the listings among ids.
func (r *ListingRepository) ByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.Listing, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListingsByIDs(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return listingsOf(rows), nil
}

// ForUpdate locks a listing until the transaction ends.
func (r *ListingRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Listing, error) {
	row, err := postgres.Queries(ctx, r.pool).GetListingForUpdate(ctx, id)
	if err != nil {
		return domain.Listing{}, postgres.Translate(err)
	}
	return listingOf(row), nil
}

// Insert stores a listing and returns its id.
func (r *ListingRepository) Insert(ctx context.Context, l domain.Listing) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertListing(ctx, sqlcgen.InsertListingParams{
		RoomID: l.RoomID, LandlordID: l.LandlordID, Title: l.Title, Description: l.Description, Status: string(l.Status),
		SubmittedAt: l.SubmittedAt, ReviewedByID: l.ReviewedByID, ReviewedAt: l.ReviewedAt, ReviewNote: l.ReviewNote,
		PublishedAt: l.PublishedAt, UnpublishedAt: l.UnpublishedAt,
	})
	return id, postgres.Translate(err)
}

// Save writes the fields of a listing that can change.
func (r *ListingRepository) Save(ctx context.Context, l domain.Listing) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateListing(ctx, sqlcgen.UpdateListingParams{
		ID: l.ID, Title: l.Title, Description: l.Description, Status: string(l.Status), SubmittedAt: l.SubmittedAt,
		ReviewedByID: l.ReviewedByID, ReviewedAt: l.ReviewedAt, ReviewNote: l.ReviewNote, PublishedAt: l.PublishedAt,
		UnpublishedAt: l.UnpublishedAt,
	}))
}

// Delete removes the listing.
func (r *ListingRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).DeleteListing(ctx, id))
}

// OpenOfRoom says whether the room has a waiting or live listing other than except.
func (r *ListingRepository) OpenOfRoom(ctx context.Context, roomID uuid.UUID, except *uuid.UUID) (bool, error) {
	ok, err := postgres.Queries(ctx, r.pool).OpenListingOfRoomExists(ctx, sqlcgen.OpenListingOfRoomExistsParams{RoomID: roomID, ExceptID: except})
	return ok, postgres.Translate(err)
}

// OpenRooms returns the rooms among roomIDs that have a waiting or live listing.
func (r *ListingRepository) OpenRooms(ctx context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).RoomsWithOpenListing(ctx, roomIDs)
	return ids, postgres.Translate(err)
}

// LockOpenOfRooms locks and returns the waiting and live listings of the rooms.
func (r *ListingRepository) LockOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]domain.Listing, error) {
	rows, err := postgres.Queries(ctx, r.pool).LockOpenListingsOfRooms(ctx, roomIDs)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return listingsOf(rows), nil
}

// CountOpen counts the landlord's waiting and live listings.
func (r *ListingRepository) CountOpen(ctx context.Context, landlordID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountOpenListingsOfLandlord(ctx, landlordID)
	return int(n), postgres.Translate(err)
}

// LockLandlord serialises the transactions that count a landlord's open listings against a plan limit.
func (r *ListingRepository) LockLandlord(ctx context.Context, landlordID uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).LockLandlordForListings(ctx, landlordID.String()))
}
