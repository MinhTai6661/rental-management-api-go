package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// BuildingRepository reads and writes buildings.
type BuildingRepository struct{ pool *pgxpool.Pool }

// NewBuildingRepository returns a repository on the pool.
func NewBuildingRepository(pool *pgxpool.Pool) *BuildingRepository {
	return &BuildingRepository{pool: pool}
}

// Search returns a page of the landlord's live buildings and how many match in all.
func (r *BuildingRepository) Search(ctx context.Context, landlordID uuid.UUID, f domain.BuildingQuery) ([]domain.BuildingSummary, int, error) {
	q := queries(ctx, r.pool)
	rows, err := q.SearchBuildings(ctx, sqlcgen.SearchBuildingsParams{
		LandlordID: landlordID, NameLike: f.NameLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, Sort: f.Sort,
		PageLimit: int32(f.Limit), PageOffset: int32(f.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, translate(err)
	}
	total, err := q.CountBuildings(ctx, sqlcgen.CountBuildingsParams{
		LandlordID: landlordID, NameLike: f.NameLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
	})
	if err != nil {
		return nil, 0, translate(err)
	}
	out := make([]domain.BuildingSummary, len(rows))
	for i, row := range rows {
		out[i] = buildingSummaryOf(row)
	}
	return out, int(total), nil
}

// Get returns one building as a list shows it, or shared.ErrNotFound.
func (r *BuildingRepository) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.BuildingSummary, error) {
	rows, err := queries(ctx, r.pool).SearchBuildings(ctx, sqlcgen.SearchBuildingsParams{LandlordID: landlordID, ID: &id, PageLimit: 1})
	if err != nil {
		return domain.BuildingSummary{}, translate(err)
	}
	if len(rows) == 0 {
		return domain.BuildingSummary{}, shared.ErrNotFound
	}
	return buildingSummaryOf(rows[0]), nil
}

// ForUpdate locks a live building of the landlord until the transaction ends.
func (r *BuildingRepository) ForUpdate(ctx context.Context, landlordID, id uuid.UUID) (domain.Building, error) {
	row, err := queries(ctx, r.pool).GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: id, LandlordID: landlordID})
	if err != nil {
		return domain.Building{}, translate(err)
	}
	return buildingOf(row), nil
}

// Insert stores a building at the end of the landlord's list.
func (r *BuildingRepository) Insert(ctx context.Context, landlordID uuid.UUID, d domain.BuildingData) (uuid.UUID, error) {
	q := queries(ctx, r.pool)
	last, err := q.MaxBuildingOrder(ctx, landlordID)
	if err != nil {
		return uuid.Nil, translate(err)
	}
	id, err := q.InsertBuilding(ctx, sqlcgen.InsertBuildingParams{
		LandlordID: landlordID, Name: d.Name, WardCode: d.WardCode, AddressDetail: d.AddressDetail,
		Description: d.Description, PriceVnd: d.PriceVND, AreaSquareMeters: d.Area,
		MinimumRentalPeriod: periodToDB(d.MinPeriod), RentalPeriodUnit: d.PeriodUnit, DisplayOrder: last + 1,
	})
	return id, translate(err)
}

// Update replaces the fields of a building.
func (r *BuildingRepository) Update(ctx context.Context, landlordID, id uuid.UUID, d domain.BuildingData) error {
	return translate(queries(ctx, r.pool).UpdateBuilding(ctx, sqlcgen.UpdateBuildingParams{
		ID: id, LandlordID: landlordID, Name: d.Name, WardCode: d.WardCode, AddressDetail: d.AddressDetail,
		Description: d.Description, PriceVnd: d.PriceVND, AreaSquareMeters: d.Area,
		MinimumRentalPeriod: periodToDB(d.MinPeriod), RentalPeriodUnit: d.PeriodUnit,
	}))
}

// NameTaken says whether another live building of the landlord has the name, in any case.
func (r *BuildingRepository) NameTaken(ctx context.Context, landlordID uuid.UUID, name string, except *uuid.UUID) (bool, error) {
	taken, err := queries(ctx, r.pool).BuildingNameTaken(ctx, sqlcgen.BuildingNameTakenParams{LandlordID: landlordID, Name: name, ExceptID: except})
	return taken, translate(err)
}

// CountLive counts the landlord's live buildings.
func (r *BuildingRepository) CountLive(ctx context.Context, landlordID uuid.UUID) (int, error) {
	n, err := queries(ctx, r.pool).CountLiveBuildings(ctx, landlordID)
	return int(n), translate(err)
}

// CountRoomsFollowingPrice counts the live rooms that take their rent from the building.
func (r *BuildingRepository) CountRoomsFollowingPrice(ctx context.Context, id uuid.UUID) (int, error) {
	n, err := queries(ctx, r.pool).CountRoomsFollowingPrice(ctx, &id)
	return int(n), translate(err)
}

// PushToRooms copies what changed in a building into the rooms that follow it and returns the rooms it touched.
func (r *BuildingRepository) PushToRooms(ctx context.Context, id uuid.UUID, after domain.BuildingDefaults, address bool, groups map[domain.Group]bool) ([]uuid.UUID, error) {
	q := queries(ctx, r.pool)
	var reached []uuid.UUID
	collect := func(ids []uuid.UUID, err error) error {
		reached = append(reached, ids...)
		return translate(err)
	}
	if address {
		if err := collect(q.PushBuildingAddress(ctx, sqlcgen.PushBuildingAddressParams{WardCode: after.WardCode, AddressDetail: after.AddressDetail, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	if groups[domain.GroupPrice] && after.PriceVND != nil {
		if err := collect(q.PushBuildingPrice(ctx, sqlcgen.PushBuildingPriceParams{PriceVnd: *after.PriceVND, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	if groups[domain.GroupArea] {
		if err := collect(q.PushBuildingArea(ctx, sqlcgen.PushBuildingAreaParams{AreaSquareMeters: after.Area, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	if groups[domain.GroupRentalPeriod] && after.MinPeriod != nil && after.PeriodUnit != nil {
		err := collect(q.PushBuildingRentalPeriod(ctx, sqlcgen.PushBuildingRentalPeriodParams{
			MinimumRentalPeriod: *periodToDB(after.MinPeriod), RentalPeriodUnit: *after.PeriodUnit, BuildingID: &id,
		}))
		if err != nil {
			return nil, err
		}
	}
	if groups[domain.GroupDescription] {
		if err := collect(q.PushBuildingDescription(ctx, sqlcgen.PushBuildingDescriptionParams{Description: after.Description, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	return reached, nil
}

// ToDelete returns the buildings among ids that are the landlord's, with their room counts.
func (r *BuildingRepository) ToDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.BuildingToDelete, error) {
	rows, err := queries(ctx, r.pool).ListBuildingsToDelete(ctx, sqlcgen.ListBuildingsToDeleteParams{LandlordID: landlordID, Ids: ids})
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.BuildingToDelete, len(rows))
	for i, row := range rows {
		out[i] = domain.BuildingToDelete{ID: row.ID, Name: row.Name, RoomsCount: int(row.RoomsCount)}
	}
	return out, nil
}

// SoftDelete marks the buildings deleted.
func (r *BuildingRepository) SoftDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return translate(queries(ctx, r.pool).SoftDeleteBuildings(ctx, sqlcgen.SoftDeleteBuildingsParams{LandlordID: landlordID, Ids: ids}))
}

// Order lists the landlord's buildings in their stored order.
func (r *BuildingRepository) Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error) {
	rows, err := queries(ctx, r.pool).ListBuildingOrder(ctx, landlordID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.OrderedID, len(rows))
	for i, row := range rows {
		out[i] = domain.OrderedID{ID: row.ID, Order: int(row.DisplayOrder)}
	}
	return out, nil
}

// SetOrder stores the new positions.
func (r *BuildingRepository) SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error {
	params := sqlcgen.SetBuildingOrderParams{LandlordID: landlordID}
	for _, m := range moves {
		params.Ids = append(params.Ids, m.ID)
		params.Orders = append(params.Orders, int32(m.Order)) //nolint:gosec // a list position
	}
	return translate(queries(ctx, r.pool).SetBuildingOrder(ctx, params))
}

// AnyEver says whether the landlord ever had a building, deleted ones included.
func (r *BuildingRepository) AnyEver(ctx context.Context, landlordID uuid.UUID) (bool, error) {
	ever, err := queries(ctx, r.pool).AnyBuildingEver(ctx, landlordID)
	return ever, translate(err)
}

// WardExists tells whether there is a ward with the code.
func (r *BuildingRepository) WardExists(ctx context.Context, code int32) (bool, error) {
	ok, err := queries(ctx, r.pool).WardExists(ctx, code)
	return ok, translate(err)
}

// FirstWardCode returns the first seeded ward, or shared.ErrNotFound when there is none.
func (r *BuildingRepository) FirstWardCode(ctx context.Context) (int32, error) {
	code, err := queries(ctx, r.pool).PropertyFirstWardCode(ctx)
	return code, translate(err)
}

// LockLandlord serialises the transactions that count a landlord's things against a plan limit.
func (r *BuildingRepository) LockLandlord(ctx context.Context, landlordID uuid.UUID) error {
	return translate(queries(ctx, r.pool).LockLandlord(ctx, landlordID.String()))
}
