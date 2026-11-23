package paymentpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/payment"
)

// MethodRepository reads and writes the payment methods of landlords. The settings arrive and leave encrypted: this
// side never sees them in the clear.
type MethodRepository struct{ pool *pgxpool.Pool }

// NewMethodRepository returns a repository on the pool.
func NewMethodRepository(pool *pgxpool.Pool) *MethodRepository { return &MethodRepository{pool: pool} }

// ListOfLandlord returns what the landlord saved.
func (r *MethodRepository) ListOfLandlord(ctx context.Context, landlordID uuid.UUID) ([]domain.StoredMethod, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListLandlordPaymentMethods(ctx, landlordID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.StoredMethod, len(rows))
	for i, row := range rows {
		out[i] = domain.StoredMethod{Method: row.Method, ConfigSealed: row.ConfigSealed, IsActive: row.IsActive}
	}
	return out, nil
}

// Get returns one saved method, or shared.ErrNotFound.
func (r *MethodRepository) Get(ctx context.Context, landlordID uuid.UUID, method string) (domain.StoredMethod, error) {
	row, err := postgres.Queries(ctx, r.pool).GetLandlordPaymentMethod(ctx, sqlcgen.GetLandlordPaymentMethodParams{LandlordID: landlordID, Method: method})
	if err != nil {
		return domain.StoredMethod{}, postgres.Translate(err)
	}
	return domain.StoredMethod{Method: row.Method, ConfigSealed: row.ConfigSealed, IsActive: row.IsActive}, nil
}

// Upsert saves a method and says whether it was new.
func (r *MethodRepository) Upsert(ctx context.Context, landlordID uuid.UUID, m domain.StoredMethod) (bool, error) {
	created, err := postgres.Queries(ctx, r.pool).UpsertLandlordPaymentMethod(ctx, sqlcgen.UpsertLandlordPaymentMethodParams{
		LandlordID: landlordID, Method: m.Method, ConfigSealed: m.ConfigSealed, IsActive: m.IsActive,
	})
	return created, postgres.Translate(err)
}

// Delete removes a saved method and says whether there was one.
func (r *MethodRepository) Delete(ctx context.Context, landlordID uuid.UUID, method string) (bool, error) {
	n, err := postgres.Queries(ctx, r.pool).DeleteLandlordPaymentMethod(ctx, sqlcgen.DeleteLandlordPaymentMethodParams{LandlordID: landlordID, Method: method})
	return n > 0, postgres.Translate(err)
}
