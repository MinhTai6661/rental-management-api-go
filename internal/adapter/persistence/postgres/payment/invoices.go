package paymentpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// InvoiceReader is the read-only view of bills (with their contract and room) that payments need. A bill is never
// written here: the invoice context owns its status.
type InvoiceReader struct{ pool *pgxpool.Pool }

// NewInvoiceReader returns a reader on the pool.
func NewInvoiceReader(pool *pgxpool.Pool) *InvoiceReader { return &InvoiceReader{pool: pool} }

func factsOf(row sqlcgen.SearchInvoiceFactsForPaymentRow) domain.InvoiceFacts {
	return domain.InvoiceFacts{
		ID: row.ID, ContractID: row.ContractID, RoomID: row.RoomID, RoomName: row.RoomName, LandlordID: row.LandlordID,
		TenantID: row.TenantID, Number: row.InvoiceNumber, Name: row.Name, PaymentCode: row.PaymentCode,
		TotalVND: row.TotalAmountVnd, Status: domain.InvoiceStatus(row.Status), DueAt: row.DueAt,
	}
}

func (r *InvoiceReader) search(ctx context.Context, p sqlcgen.SearchInvoiceFactsForPaymentParams) ([]domain.InvoiceFacts, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchInvoiceFactsForPayment(ctx, p)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.InvoiceFacts, len(rows))
	for i, row := range rows {
		out[i] = factsOf(row)
	}
	return out, nil
}

func (r *InvoiceReader) one(ctx context.Context, p sqlcgen.SearchInvoiceFactsForPaymentParams) (domain.InvoiceFacts, error) {
	facts, err := r.search(ctx, p)
	if err != nil {
		return domain.InvoiceFacts{}, err
	}
	if len(facts) == 0 {
		return domain.InvoiceFacts{}, shared.ErrNotFound
	}
	return facts[0], nil
}

// Get returns a bill as the viewer sees it, or shared.ErrNotFound.
func (r *InvoiceReader) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.InvoiceFacts, error) {
	return r.one(ctx, sqlcgen.SearchInvoiceFactsForPaymentParams{Scoped: true, AsLandlord: v.AsLandlord, ViewerID: v.UserID, ID: &id})
}

// ByID returns a bill for the system.
func (r *InvoiceReader) ByID(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error) {
	return r.one(ctx, sqlcgen.SearchInvoiceFactsForPaymentParams{ID: &id})
}

// Lock returns a bill and locks it until the transaction ends.
func (r *InvoiceReader) Lock(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error) {
	if _, err := postgres.Queries(ctx, r.pool).LockInvoiceForPayment(ctx, id); err != nil {
		return domain.InvoiceFacts{}, postgres.Translate(err)
	}
	return r.ByID(ctx, id)
}

// SentOfRoom returns the sent bills of a landlord's live room of that name, oldest period first.
func (r *InvoiceReader) SentOfRoom(ctx context.Context, landlordID uuid.UUID, room string) ([]domain.InvoiceFacts, error) {
	sent := string(domain.InvoiceSent)
	return r.search(ctx, sqlcgen.SearchInvoiceFactsForPaymentParams{LandlordID: &landlordID, RoomName: &room, Status: &sent})
}
