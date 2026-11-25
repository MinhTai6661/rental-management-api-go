package invoicepg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// ContractReader is the read-only view of contracts and their rooms that billing needs.
type ContractReader struct{ pool *pgxpool.Pool }

// NewContractReader returns a reader on the pool.
func NewContractReader(pool *pgxpool.Pool) *ContractReader { return &ContractReader{pool: pool} }

func factsOf(row sqlcgen.SearchContractFactsForBillingRow) domain.ContractFacts {
	return domain.ContractFacts{
		ID: row.ID, RoomID: row.RoomID, RoomName: row.RoomName, LandlordID: row.LandlordID, LandlordName: row.LandlordFullName,
		TenantID: row.TenantID, TenantName: row.TenantName, TenantPhone: row.TenantPhone, TenantAccountName: row.TenantFullName,
		TenantEmail: row.TenantEmail, Status: row.Status, PriceVND: row.PriceVnd, OccupantCount: int(row.OccupantCount),
		StartsOn: row.StartsOn, EndsOn: row.EndsOn, BilledByDay: row.RoomRentalPeriodUnit == "day",
	}
}

func (r *ContractReader) byID(ctx context.Context, p sqlcgen.SearchContractFactsForBillingParams) (domain.ContractFacts, error) {
	p.PageLimit = 1
	rows, err := postgres.Queries(ctx, r.pool).SearchContractFactsForBilling(ctx, p)
	if err != nil {
		return domain.ContractFacts{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.ContractFacts{}, shared.ErrNotFound
	}
	return factsOf(rows[0]), nil
}

// Get returns the contract as the viewer sees it, or shared.ErrNotFound.
func (r *ContractReader) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.ContractFacts, error) {
	return r.byID(ctx, sqlcgen.SearchContractFactsForBillingParams{Scoped: true, AsLandlord: v.AsLandlord, AsManager: v.AsManager, ViewerID: v.UserID, ID: &id})
}

// Lock returns a contract and locks it until the transaction ends.
func (r *ContractReader) Lock(ctx context.Context, id uuid.UUID) (domain.ContractFacts, error) {
	if _, err := postgres.Queries(ctx, r.pool).LockContractForBilling(ctx, id); err != nil {
		return domain.ContractFacts{}, postgres.Translate(err)
	}
	return r.byID(ctx, sqlcgen.SearchContractFactsForBillingParams{ID: &id})
}

// Billable returns running contracts after the id (keyset), at most limit.
func (r *ContractReader) Billable(ctx context.Context, after uuid.UUID, limit int) ([]domain.ContractFacts, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchContractFactsForBilling(ctx, sqlcgen.SearchContractFactsForBillingParams{
		RunningOnly: true, AfterID: after, PageLimit: int32(limit), //nolint:gosec // a batch size
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.ContractFacts, len(rows))
	for i, row := range rows {
		out[i] = factsOf(row)
	}
	return out, nil
}

// FindByRoomName returns the latest contract of the landlord's live room of that name (any case).
func (r *ContractReader) FindByRoomName(ctx context.Context, landlordID uuid.UUID, room string) (domain.ContractFacts, error) {
	id, err := postgres.Queries(ctx, r.pool).FindContractIDByRoomName(ctx, sqlcgen.FindContractIDByRoomNameParams{LandlordID: landlordID, Name: room})
	if err != nil {
		return domain.ContractFacts{}, postgres.Translate(err)
	}
	return r.byID(ctx, sqlcgen.SearchContractFactsForBillingParams{ID: &id})
}

// ChargeReader is the read-only view of the charges of rooms that billing needs.
type ChargeReader struct{ pool *pgxpool.Pool }

// NewChargeReader returns a reader on the pool.
func NewChargeReader(pool *pgxpool.Pool) *ChargeReader { return &ChargeReader{pool: pool} }

// ChargesOf returns the live charges of a room in their order.
func (r *ChargeReader) ChargesOf(ctx context.Context, roomID uuid.UUID) ([]domain.Charge, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListChargesOfRoomForBilling(ctx, &roomID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Charge, len(rows))
	for i, row := range rows {
		out[i] = domain.Charge{ID: row.ID, Name: row.Name, Kind: row.Kind, Unit: row.Unit, AmountVND: row.AmountVnd}
	}
	return out, nil
}

// RoomOf returns the name of a live room of the landlord, or shared.ErrNotFound.
func (r *ChargeReader) RoomOf(ctx context.Context, landlordID, roomID uuid.UUID) (string, error) {
	name, err := postgres.Queries(ctx, r.pool).GetRoomNameOfLandlord(ctx, sqlcgen.GetRoomNameOfLandlordParams{ID: roomID, LandlordID: landlordID})
	return name, postgres.Translate(err)
}
