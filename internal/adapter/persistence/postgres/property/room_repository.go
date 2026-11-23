package propertypg

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// RoomRepository reads and writes rooms.
type RoomRepository struct{ pool *pgxpool.Pool }

// NewRoomRepository returns a repository on the pool.
func NewRoomRepository(pool *pgxpool.Pool) *RoomRepository { return &RoomRepository{pool: pool} }

// Search returns a page of the landlord's live rooms and how many match in all.
func (r *RoomRepository) Search(ctx context.Context, landlordID uuid.UUID, f domain.RoomQuery) ([]domain.RoomSummary, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.SearchRooms(ctx, sqlcgen.SearchRoomsParams{
		LandlordID: landlordID, NameLike: f.NameLike, Status: f.Status, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo, Sort: f.Sort,
		PageLimit: int32(f.Limit), PageOffset: int32(f.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountRooms(ctx, sqlcgen.CountRoomsParams{
		LandlordID: landlordID, NameLike: f.NameLike, Status: f.Status, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.RoomSummary, len(rows))
	for i, row := range rows {
		out[i] = roomSummaryOf(row)
	}
	return out, int(total), nil
}

// Get returns one room as a list shows it, or shared.ErrNotFound.
func (r *RoomRepository) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.RoomSummary, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchRooms(ctx, sqlcgen.SearchRoomsParams{LandlordID: landlordID, ID: &id, PageLimit: 1})
	if err != nil {
		return domain.RoomSummary{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.RoomSummary{}, shared.ErrNotFound
	}
	return roomSummaryOf(rows[0]), nil
}

// ForUpdate locks a live room of the landlord until the transaction ends.
func (r *RoomRepository) ForUpdate(ctx context.Context, landlordID, id uuid.UUID) (domain.Room, error) {
	row, err := postgres.Queries(ctx, r.pool).GetRoomForUpdate(ctx, sqlcgen.GetRoomForUpdateParams{ID: id, LandlordID: landlordID})
	if err != nil {
		return domain.Room{}, postgres.Translate(err)
	}
	return roomOf(row), nil
}

// ForUpdateMany locks the live rooms of the landlord among ids.
func (r *RoomRepository) ForUpdateMany(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.RoomRef, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRoomsForUpdate(ctx, sqlcgen.ListRoomsForUpdateParams{LandlordID: landlordID, Ids: ids})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomRef, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomRef{ID: row.ID, Name: row.Name, Status: domain.Status(row.Status)}
	}
	return out, nil
}

// Insert stores a room at the end of the landlord's list.
func (r *RoomRepository) Insert(ctx context.Context, landlordID uuid.UUID, d domain.RoomData) (uuid.UUID, error) {
	q := postgres.Queries(ctx, r.pool)
	last, err := q.MaxRoomOrder(ctx, landlordID)
	if err != nil {
		return uuid.Nil, postgres.Translate(err)
	}
	fl := flagsOf(d.Inherits)
	id, err := q.InsertRoom(ctx, sqlcgen.InsertRoomParams{
		LandlordID: landlordID, BuildingID: d.BuildingID, Name: d.Name, WardCode: d.WardCode,
		AddressDetail: d.AddressDetail, AreaSquareMeters: d.Area, PriceVnd: d.PriceVND,
		MinimumRentalPeriod: int16(d.MinPeriod), RentalPeriodUnit: d.PeriodUnit, //nolint:gosec // validated to 1..3650
		Description: d.Description, Status: string(d.Status), InheritsPrice: fl.price, InheritsArea: fl.area,
		InheritsRentalPeriod: fl.rentalPeriod, InheritsDescription: fl.description, InheritsCharges: fl.charges,
		InheritsMedia: fl.media, DisplayOrder: last + 1,
	})
	return id, postgres.Translate(err)
}

// InsertRun stores a run of rooms at the end of the list and returns their ids in the order of the names.
func (r *RoomRepository) InsertRun(ctx context.Context, landlordID uuid.UUID, run domain.RoomRun) ([]uuid.UUID, error) {
	q := postgres.Queries(ctx, r.pool)
	last, err := q.MaxRoomOrder(ctx, landlordID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	fl := flagsOf(run.Inherits)
	params := sqlcgen.InsertRoomRunParams{
		LandlordID: landlordID, BuildingID: run.BuildingID, WardCode: run.WardCode, AddressDetail: run.AddressDetail,
		AreaSquareMeters: run.Area, PriceVnd: run.PriceVND, MinimumRentalPeriod: int16(run.MinPeriod), //nolint:gosec // validated
		RentalPeriodUnit: run.PeriodUnit, Description: run.Description, Status: string(run.Status),
		InheritsPrice: fl.price, InheritsArea: fl.area, InheritsRentalPeriod: fl.rentalPeriod,
		InheritsDescription: fl.description, InheritsCharges: fl.charges, InheritsMedia: fl.media, Names: run.Names,
	}
	for i := range run.Names {
		params.Orders = append(params.Orders, last+int32(i)+1) //nolint:gosec // at most 100 names
	}
	rows, err := q.InsertRoomRun(ctx, params)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	slices.SortFunc(rows, func(a, b sqlcgen.InsertRoomRunRow) int { return int(a.DisplayOrder - b.DisplayOrder) })
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids, nil
}

// Update replaces the fields of a room.
func (r *RoomRepository) Update(ctx context.Context, landlordID, id uuid.UUID, d domain.RoomData) error {
	fl := flagsOf(d.Inherits)
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateRoom(ctx, sqlcgen.UpdateRoomParams{
		ID: id, LandlordID: landlordID, BuildingID: d.BuildingID, Name: d.Name, WardCode: d.WardCode,
		AddressDetail: d.AddressDetail, AreaSquareMeters: d.Area, PriceVnd: d.PriceVND,
		MinimumRentalPeriod: int16(d.MinPeriod), RentalPeriodUnit: d.PeriodUnit, //nolint:gosec // validated to 1..3650
		Description: d.Description, Status: string(d.Status), InheritsPrice: fl.price, InheritsArea: fl.area,
		InheritsRentalPeriod: fl.rentalPeriod, InheritsDescription: fl.description, InheritsCharges: fl.charges,
		InheritsMedia: fl.media,
	}))
}

// NameTaken says whether another live room of the building (or outside any building) has the name.
func (r *RoomRepository) NameTaken(ctx context.Context, landlordID uuid.UUID, buildingID *uuid.UUID, name string, except *uuid.UUID) (bool, error) {
	taken, err := postgres.Queries(ctx, r.pool).RoomNameTaken(ctx, sqlcgen.RoomNameTakenParams{LandlordID: landlordID, Name: name, BuildingID: buildingID, ExceptID: except})
	return taken, postgres.Translate(err)
}

// NamesTakenInBuilding returns the lower case names among names that live rooms of the building use.
func (r *RoomRepository) NamesTakenInBuilding(ctx context.Context, buildingID uuid.UUID, names []string) ([]string, error) {
	taken, err := postgres.Queries(ctx, r.pool).RoomNamesTakenInBuilding(ctx, sqlcgen.RoomNamesTakenInBuildingParams{BuildingID: &buildingID, Names: names})
	return taken, postgres.Translate(err)
}

// CountLive counts the landlord's live rooms.
func (r *RoomRepository) CountLive(ctx context.Context, landlordID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountLiveRooms(ctx, landlordID)
	return int(n), postgres.Translate(err)
}

// SetStatus moves the rooms to a status.
func (r *RoomRepository) SetStatus(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, status domain.Status) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SetRoomStatus(ctx, sqlcgen.SetRoomStatusParams{Status: string(status), LandlordID: landlordID, Ids: ids}))
}

// SoftDelete marks the rooms deleted.
func (r *RoomRepository) SoftDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SoftDeleteRooms(ctx, sqlcgen.SoftDeleteRoomsParams{LandlordID: landlordID, Ids: ids}))
}

// Order lists the landlord's rooms in their stored order.
func (r *RoomRepository) Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRoomOrder(ctx, landlordID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.OrderedID, len(rows))
	for i, row := range rows {
		out[i] = domain.OrderedID{ID: row.ID, Order: int(row.DisplayOrder)}
	}
	return out, nil
}

// SetOrder stores the new positions.
func (r *RoomRepository) SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error {
	params := sqlcgen.SetRoomOrderParams{LandlordID: landlordID}
	for _, m := range moves {
		params.Ids = append(params.Ids, m.ID)
		params.Orders = append(params.Orders, int32(m.Order)) //nolint:gosec // a list position
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).SetRoomOrder(ctx, params))
}

// FollowingCharges lists the live rooms that take their charges from the building.
func (r *RoomRepository) FollowingCharges(ctx context.Context, buildingID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ListRoomsFollowingCharges(ctx, &buildingID)
	return ids, postgres.Translate(err)
}

// CopySources lists the live rooms of a building in display order, with what a copy needs to know.
func (r *RoomRepository) CopySources(ctx context.Context, landlordID, buildingID uuid.UUID) ([]domain.RoomCopySource, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListRoomsOfBuildingForCopy(ctx, sqlcgen.ListRoomsOfBuildingForCopyParams{BuildingID: &buildingID, LandlordID: landlordID})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomCopySource, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomCopySource{ID: row.ID, InheritsCharges: row.InheritsCharges, InheritsMedia: row.InheritsMedia}
	}
	return out, nil
}

// Copy makes the copies of rooms inside another building, after the landlord's last room.
func (r *RoomRepository) Copy(ctx context.Context, landlordID, toBuilding uuid.UUID, pairs []domain.RoomCopyPair) error {
	q := postgres.Queries(ctx, r.pool)
	base, err := q.MaxRoomOrder(ctx, landlordID)
	if err != nil {
		return postgres.Translate(err)
	}
	params := sqlcgen.CopyRoomsParams{BuildingID: toBuilding, BaseOrder: base}
	for i, p := range pairs {
		params.DestIds = append(params.DestIds, p.Dest)
		params.SrcIds = append(params.SrcIds, p.Source)
		params.Positions = append(params.Positions, int32(i+1)) //nolint:gosec // at most the plan's room limit
	}
	return postgres.Translate(q.CopyRooms(ctx, params))
}
