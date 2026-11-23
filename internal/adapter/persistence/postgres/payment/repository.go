// Package paymentpg is the database side of the payment use cases: the payment and payment method repositories and the
// read-only view of bills that payments need. It returns domain types and translates driver errors (see
// postgres.Translate).
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

// PaymentRepository reads and writes payments.
type PaymentRepository struct{ pool *pgxpool.Pool }

// NewPaymentRepository returns a repository on the pool.
func NewPaymentRepository(pool *pgxpool.Pool) *PaymentRepository {
	return &PaymentRepository{pool: pool}
}

// Get returns a payment as the viewer sees it, or shared.ErrNotFound.
func (r *PaymentRepository) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchPayments(ctx, sqlcgen.SearchPaymentsParams{
		Scoped: true, AsLandlord: v.AsLandlord, ViewerID: v.UserID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.Detail{}, shared.ErrNotFound
	}
	return detailOf(rows[0]), nil
}

// ForUpdate returns a payment and locks it until the transaction ends.
func (r *PaymentRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Payment, error) {
	row, err := postgres.Queries(ctx, r.pool).LockPayment(ctx, id)
	if err != nil {
		return domain.Payment{}, postgres.Translate(err)
	}
	return paymentOf(row), nil
}

// ProofKeys returns the storage keys of the proof pictures of a payment.
func (r *PaymentRepository) ProofKeys(ctx context.Context, id uuid.UUID) ([]string, error) {
	keys, err := postgres.Queries(ctx, r.pool).GetPaymentProofKeys(ctx, id)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return keys, nil
}

// ListOfInvoice returns a page of the payments of a bill, oldest first, and how many there are.
func (r *PaymentRepository) ListOfInvoice(ctx context.Context, invoiceID uuid.UUID, limit, offset int) ([]domain.Detail, int, error) {
	return r.list(ctx, sqlcgen.SearchPaymentsParams{InvoiceID: &invoiceID, OldestFirst: true}, limit, offset)
}

// ListOfLandlord returns a page of the payments on the bills of a landlord's rooms, newest first.
func (r *PaymentRepository) ListOfLandlord(ctx context.Context, landlordID uuid.UUID, status *domain.Status, limit, offset int) ([]domain.Detail, int, error) {
	p := sqlcgen.SearchPaymentsParams{Scoped: true, AsLandlord: true, ViewerID: landlordID}
	if status != nil {
		s := string(*status)
		p.Status = &s
	}
	return r.list(ctx, p, limit, offset)
}

func (r *PaymentRepository) list(ctx context.Context, p sqlcgen.SearchPaymentsParams, limit, offset int) ([]domain.Detail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	p.PageLimit, p.PageOffset = int32(limit), int32(offset) //nolint:gosec // bounded by the use case
	rows, err := q.SearchPayments(ctx, p)
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountPayments(ctx, sqlcgen.CountPaymentsParams{
		Scoped: p.Scoped, AsLandlord: p.AsLandlord, ViewerID: p.ViewerID, InvoiceID: p.InvoiceID, Status: p.Status,
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

// Insert stores a payment and returns its id.
func (r *PaymentRepository) Insert(ctx context.Context, p domain.Payment) (uuid.UUID, error) {
	keys := p.ProofKeys
	if keys == nil {
		keys = []string{}
	}
	id, err := postgres.Queries(ctx, r.pool).InsertPayment(ctx, sqlcgen.InsertPaymentParams{
		InvoiceID: p.InvoiceID, PayerID: p.PayerID, Method: p.Method, AmountVnd: p.AmountVND, Status: string(p.Status), Note: p.Note,
		ProofKeys: keys, ExternalReference: p.ExternalReference, ReviewedByID: p.ReviewedByID, ReviewedAt: p.ReviewedAt,
		ReviewNote: p.ReviewNote, RecordedByID: p.RecordedByID, PaidAt: p.PaidAt,
	})
	return id, postgres.Translate(err)
}

// SaveReview writes the status, who reviewed it and when, the note and the paid time.
func (r *PaymentRepository) SaveReview(ctx context.Context, p domain.Payment) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdatePaymentReview(ctx, sqlcgen.UpdatePaymentReviewParams{
		ID: p.ID, Status: string(p.Status), ReviewedByID: p.ReviewedByID, ReviewedAt: p.ReviewedAt, ReviewNote: p.ReviewNote, PaidAt: p.PaidAt,
	}))
}

// Delete removes a payment for good.
func (r *PaymentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).DeletePayment(ctx, id))
}

// Totals returns the confirmed and the pending money of each bill (a bill with no such payment is left out).
func (r *PaymentRepository) Totals(ctx context.Context, invoiceIDs []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	rows, err := postgres.Queries(ctx, r.pool).SumPaymentsOfInvoices(ctx, invoiceIDs)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]domain.Totals, len(rows))
	for _, row := range rows {
		out[row.InvoiceID] = domain.Totals{ConfirmedVND: row.ConfirmedVnd, PendingVND: row.PendingVnd}
	}
	return out, nil
}

// VoidConfirmed marks the confirmed payments of a bill voided.
func (r *PaymentRepository) VoidConfirmed(ctx context.Context, invoiceID uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).VoidConfirmedPaymentsOfInvoice(ctx, invoiceID))
}

// DeleteOfInvoice removes every payment of a bill and returns the keys of their proof files.
func (r *PaymentRepository) DeleteOfInvoice(ctx context.Context, invoiceID uuid.UUID) ([]string, error) {
	rows, err := postgres.Queries(ctx, r.pool).DeletePaymentsOfInvoice(ctx, invoiceID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	var keys []string
	for _, k := range rows {
		keys = append(keys, k...)
	}
	return keys, nil
}
