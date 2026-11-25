// Package contractpg is the database side of the contract use cases: the contract repository and the read-only view
// of rooms that contracts need. It returns domain types and translates driver errors (see postgres.Translate).
package contractpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// ContractRepository reads and writes contracts.
type ContractRepository struct{ pool *pgxpool.Pool }

// NewContractRepository returns a repository on the pool.
func NewContractRepository(pool *pgxpool.Pool) *ContractRepository {
	return &ContractRepository{pool: pool}
}

// Search returns a page of the contracts the viewer sees and how many match in all.
func (r *ContractRepository) Search(ctx context.Context, v domain.Viewer, f domain.Query) ([]domain.Detail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	statuses := make([]string, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	if len(statuses) == 0 {
		statuses = nil
	}
	cancellation := cancellationStatusToDB(f.CancellationStatus)
	rows, err := q.SearchContracts(ctx, sqlcgen.SearchContractsParams{
		AsLandlord: v.AsLandlord, AsManager: v.AsManager, ViewerID: v.UserID, Statuses: statuses, RoomID: f.RoomID, BuildingID: f.BuildingID,
		NoBuilding: f.NoBuilding, CancellationStatus: cancellation, SearchLike: f.SearchLike, Sort: f.Sort,
		PaymentState: paymentStateToDB(f.PaymentState), HasUnpaidInvoice: f.HasUnpaidInvoice,
		PageLimit: int32(f.Limit), PageOffset: int32(f.Offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountContracts(ctx, sqlcgen.CountContractsParams{
		AsLandlord: v.AsLandlord, AsManager: v.AsManager, ViewerID: v.UserID, Statuses: statuses, RoomID: f.RoomID, BuildingID: f.BuildingID,
		NoBuilding: f.NoBuilding, CancellationStatus: cancellation, SearchLike: f.SearchLike,
		PaymentState: paymentStateToDB(f.PaymentState), HasUnpaidInvoice: f.HasUnpaidInvoice,
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

// Get returns one contract the viewer sees, or shared.ErrNotFound.
func (r *ContractRepository) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchContracts(ctx, sqlcgen.SearchContractsParams{
		AsLandlord: v.AsLandlord, AsManager: v.AsManager, ViewerID: v.UserID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.Detail{}, shared.ErrNotFound
	}
	return detailOf(rows[0]), nil
}

// ForUpdate locks a live contract until the transaction ends.
func (r *ContractRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Contract, error) {
	row, err := postgres.Queries(ctx, r.pool).GetContractForUpdate(ctx, id)
	if err != nil {
		return domain.Contract{}, postgres.Translate(err)
	}
	return contractOfLock(row), nil
}

// Insert stores a contract and returns its id.
func (r *ContractRepository) Insert(ctx context.Context, c domain.Contract) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertContract(ctx, sqlcgen.InsertContractParams{
		RoomID: c.RoomID, LandlordID: c.LandlordID, TenantID: c.TenantID, TenantName: c.TenantName, TenantPhone: c.TenantPhone,
		Status: string(c.Status), PriceVnd: c.PriceVND, DepositVnd: c.DepositVND, OccupantCount: int16(c.OccupantCount), //nolint:gosec // validated to 1..50
		StartsOn: c.StartsOn, EndsOn: c.EndsOn, Notes: c.Notes, ConfirmedAt: c.ConfirmedAt, DisplayOrder: int32(c.DisplayOrder), //nolint:gosec // a list position
		CancellationStatus: cancellationStatusToDB(c.Cancellation.Status), CancellationRequestedAt: c.Cancellation.RequestedAt,
		CancellationReason: c.Cancellation.Reason, CancellationResponseNote: c.Cancellation.ResponseNote,
	})
	return id, postgres.Translate(err)
}

// Save writes the fields of a contract that can change.
func (r *ContractRepository) Save(ctx context.Context, c domain.Contract) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateContract(ctx, sqlcgen.UpdateContractParams{
		ID: c.ID, TenantName: c.TenantName, TenantPhone: c.TenantPhone, Status: string(c.Status), PriceVnd: c.PriceVND,
		DepositVnd: c.DepositVND, OccupantCount: int16(c.OccupantCount), StartsOn: c.StartsOn, EndsOn: c.EndsOn, //nolint:gosec // validated to 1..50
		Notes: c.Notes, CancellationStatus: cancellationStatusToDB(c.Cancellation.Status),
		CancellationRequestedAt: c.Cancellation.RequestedAt, CancellationReason: c.Cancellation.Reason,
		CancellationResponseNote: c.Cancellation.ResponseNote,
	}))
}

// SoftDelete marks the contract deleted.
func (r *ContractRepository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SoftDeleteContract(ctx, id))
}

// Occupancy counts the running contracts of a room.
func (r *ContractRepository) Occupancy(ctx context.Context, roomID uuid.UUID) (domain.Occupancy, error) {
	row, err := postgres.Queries(ctx, r.pool).CountActiveContractsOfRoom(ctx, roomID)
	return domain.Occupancy{Running: int(row.Total), WithTenant: int(row.WithTenant)}, postgres.Translate(err)
}

// RegistrationOpen says whether the tenant has a waiting or running contract on the room.
func (r *ContractRepository) RegistrationOpen(ctx context.Context, roomID, tenantID uuid.UUID) (bool, error) {
	open, err := postgres.Queries(ctx, r.pool).OpenRegistrationExists(ctx, sqlcgen.OpenRegistrationExistsParams{RoomID: roomID, TenantID: &tenantID})
	return open, postgres.Translate(err)
}

// RejectOtherPending rejects the waiting registrations of the room but one and returns their ids.
func (r *ContractRepository) RejectOtherPending(ctx context.Context, roomID, exceptID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).RejectOtherPendingContracts(ctx, sqlcgen.RejectOtherPendingContractsParams{RoomID: roomID, ExceptID: exceptID})
	return ids, postgres.Translate(err)
}

// CloseBlank ends the running contracts of the rooms that no tenant holds.
func (r *ContractRepository) CloseBlank(ctx context.Context, roomIDs []uuid.UUID, today time.Time) ([]domain.ClosedBlank, error) {
	rows, err := postgres.Queries(ctx, r.pool).CloseBlankContracts(ctx, sqlcgen.CloseBlankContractsParams{Today: today, RoomIds: roomIDs})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.ClosedBlank, len(rows))
	for i, row := range rows {
		out[i] = domain.ClosedBlank{ContractID: row.ID, RoomID: row.RoomID, LandlordID: row.LandlordID}
	}
	return out, nil
}

// TopOrder is the position a new contract of the landlord takes: one before the smallest in use.
func (r *ContractRepository) TopOrder(ctx context.Context, landlordID uuid.UUID) (int, error) {
	smallest, err := postgres.Queries(ctx, r.pool).MinContractOrder(ctx, landlordID)
	return int(smallest) - 1, postgres.Translate(err)
}

// Order lists the contracts the landlord sorts, in their stored order, locked.
func (r *ContractRepository) Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListContractOrder(ctx, landlordID)
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
func (r *ContractRepository) SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error {
	params := sqlcgen.SetContractOrderParams{LandlordID: landlordID}
	for _, m := range moves {
		params.Ids = append(params.Ids, m.ID)
		params.Orders = append(params.Orders, int32(m.Order)) //nolint:gosec // a list position
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).SetContractOrder(ctx, params))
}

// CountRunning counts the landlord's running contracts that a tenant holds.
func (r *ContractRepository) CountRunning(ctx context.Context, landlordID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountRunningContractsOfLandlord(ctx, landlordID)
	return int(n), postgres.Translate(err)
}

// CountPending counts the tenant's waiting registrations.
func (r *ContractRepository) CountPending(ctx context.Context, tenantID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountPendingRegistrationsOfTenant(ctx, &tenantID)
	return int(n), postgres.Translate(err)
}

// RunningTenantRooms returns the rooms among roomIDs on which a tenant holds a running contract.
func (r *ContractRepository) RunningTenantRooms(ctx context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ListRoomsWithRunningTenantContract(ctx, roomIDs)
	return ids, postgres.Translate(err)
}

// LockLandlord serialises the transactions that count a landlord's running contracts against a plan limit.
func (r *ContractRepository) LockLandlord(ctx context.Context, landlordID uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).LockLandlordContracts(ctx, landlordID.String()))
}

// StatusCounts counts the tenant's contracts per status.
func (r *ContractRepository) StatusCounts(ctx context.Context, tenantID uuid.UUID) (domain.StatusCounts, error) {
	rows, err := postgres.Queries(ctx, r.pool).CountTenantContractsByStatus(ctx, &tenantID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := domain.StatusCounts{}
	for _, row := range rows {
		out[domain.Status(row.Status)] = int(row.Total)
	}
	return out, nil
}

// RentalExtras returns the costs and the pictures of a rented room for the renter's detail.
func (r *ContractRepository) RentalExtras(ctx context.Context, roomID uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error) {
	q := postgres.Queries(ctx, r.pool)
	charges, err := q.ListChargesOfRoomForRental(ctx, &roomID)
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	pictures, err := q.ListPicturesOfRoomForRental(ctx, roomID)
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	lines := make([]domain.ChargeLine, len(charges))
	for i, c := range charges {
		lines[i] = domain.ChargeLine{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note}
	}
	pics := make([]domain.Picture, len(pictures))
	for i, p := range pictures {
		pics[i] = domain.Picture{ID: p.ID, Kind: p.Kind, FileKey: p.FileKey, ThumbKey: p.ThumbKey, Width: int(p.Width), Height: int(p.Height)}
	}
	return lines, pics, nil
}

// CountOfTenant counts the contracts of the tenant on the landlord's rooms.
func (r *ContractRepository) CountOfTenant(ctx context.Context, landlordID, tenantID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountContractsOfTenantOnLandlord(ctx, sqlcgen.CountContractsOfTenantOnLandlordParams{TenantID: &tenantID, LandlordID: landlordID})
	return int(n), postgres.Translate(err)
}

func paymentStateToDB(s *domain.PaymentState) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// PaymentCounts counts the tenant's contracts in the statuses by their state of money.
func (r *ContractRepository) PaymentCounts(ctx context.Context, tenantID uuid.UUID, statuses []domain.Status) (domain.PaymentCounts, error) {
	codes := make([]string, len(statuses))
	for i, s := range statuses {
		codes[i] = string(s)
	}
	row, err := postgres.Queries(ctx, r.pool).CountTenantContractsByPaymentState(ctx, sqlcgen.CountTenantContractsByPaymentStateParams{
		TenantID: &tenantID, Statuses: codes,
	})
	if err != nil {
		return domain.PaymentCounts{}, postgres.Translate(err)
	}
	return domain.PaymentCounts{All: int(row.Total), Due: int(row.Due), Review: int(row.Review), Settled: int(row.Settled)}, nil
}
