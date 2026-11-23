// Package invoicepg is the database side of the invoice use cases: the invoice repository and the read-only views of
// contracts and room charges that billing needs. It returns domain types and translates driver errors (see
// postgres.Translate).
package invoicepg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// InvoiceRepository reads and writes bills with their lines and meter readings.
type InvoiceRepository struct{ pool *pgxpool.Pool }

// NewInvoiceRepository returns a repository on the pool.
func NewInvoiceRepository(pool *pgxpool.Pool) *InvoiceRepository {
	return &InvoiceRepository{pool: pool}
}

// Get returns a bill as the viewer sees it, or shared.ErrNotFound.
func (r *InvoiceRepository) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchInvoices(ctx, sqlcgen.SearchInvoicesParams{
		Scoped: true, AsLandlord: v.AsLandlord, ViewerID: v.UserID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return r.one(ctx, rows)
}

// ForUpdate is Get and locks the bill until the transaction ends.
func (r *InvoiceRepository) ForUpdate(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	if _, err := postgres.Queries(ctx, r.pool).LockInvoice(ctx, sqlcgen.LockInvoiceParams{ID: id, AsLandlord: v.AsLandlord, ViewerID: v.UserID}); err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return r.Get(ctx, v, id)
}

// FindByShareHash returns the bill whose link has that hash.
func (r *InvoiceRepository) FindByShareHash(ctx context.Context, hash string) (domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchInvoices(ctx, sqlcgen.SearchInvoicesParams{ShareHash: &hash, PageLimit: 1})
	if err != nil {
		return domain.Detail{}, postgres.Translate(err)
	}
	return r.one(ctx, rows)
}

func (r *InvoiceRepository) one(ctx context.Context, rows []sqlcgen.SearchInvoicesRow) (domain.Detail, error) {
	if len(rows) == 0 {
		return domain.Detail{}, shared.ErrNotFound
	}
	details, err := r.fill(ctx, []domain.Detail{detailOf(rows[0])})
	if err != nil {
		return domain.Detail{}, err
	}
	return details[0], nil
}

// List returns a page of the bills of a contract the viewer sees, newest period first, and how many there are.
func (r *InvoiceRepository) List(ctx context.Context, v domain.Viewer, contractID uuid.UUID, limit, offset int) ([]domain.Detail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.SearchInvoices(ctx, sqlcgen.SearchInvoicesParams{
		Scoped: true, AsLandlord: v.AsLandlord, ViewerID: v.UserID, ContractID: &contractID,
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the use case
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountInvoices(ctx, sqlcgen.CountInvoicesParams{AsLandlord: v.AsLandlord, ViewerID: v.UserID, ContractID: contractID})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	details := make([]domain.Detail, len(rows))
	for i, row := range rows {
		details[i] = detailOf(row)
	}
	details, err = r.fill(ctx, details)
	return details, int(total), err
}

// fill reads the lines and the readings of the bills, two queries for all of them.
func (r *InvoiceRepository) fill(ctx context.Context, details []domain.Detail) ([]domain.Detail, error) {
	if len(details) == 0 {
		return details, nil
	}
	ids := make([]uuid.UUID, len(details))
	at := make(map[uuid.UUID]int, len(details))
	for i, d := range details {
		ids[i], at[d.ID] = d.ID, i
		details[i].Items, details[i].Readings = []domain.Item{}, []domain.Reading{}
	}
	q := postgres.Queries(ctx, r.pool)
	items, err := q.ListItemsOfInvoices(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	for _, it := range items {
		i := at[it.InvoiceID]
		details[i].Items = append(details[i].Items, domain.Item{
			ID: it.ID, Kind: it.Kind, Description: it.Description, Quantity: it.Quantity, UnitPriceVND: it.UnitPriceVnd,
			AmountVND: it.AmountVnd, DisplayOrder: int(it.DisplayOrder),
		})
	}
	readings, err := q.ListReadingsOfInvoices(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	for _, m := range readings {
		i := at[m.InvoiceID]
		details[i].Readings = append(details[i].Readings,
			readingOf(m.ID, m.ChargeID, m.InvoiceID, m.ReadOn, m.ReadingValue, m.Consumption, m.UnitPriceVnd, m.ChargeName, m.ChargeUnit))
	}
	return details, nil
}

// Insert stores a bill with its lines and readings and returns its id.
func (r *InvoiceRepository) Insert(ctx context.Context, inv domain.Invoice) (uuid.UUID, error) {
	q := postgres.Queries(ctx, r.pool)
	id, err := q.InsertInvoice(ctx, sqlcgen.InsertInvoiceParams{
		ContractID: inv.ContractID, CreatedByID: inv.CreatedByID, InvoiceNumber: inv.Number, Name: inv.Name,
		PaymentLabel: inv.PaymentLabel, PaymentCode: inv.PaymentCode, PeriodStart: inv.PeriodStart, PeriodEnd: inv.PeriodEnd,
		TotalAmountVnd: inv.TotalVND, DueAt: inv.DueAt, Status: string(inv.Status), SentAt: inv.SentAt, SentVia: sourceToDB(inv.SentVia),
		SentByID: inv.SentByID, PaidAt: inv.PaidAt, Note: inv.Note, Delivery: string(inv.Delivery),
	})
	if err != nil {
		return uuid.Nil, postgres.Translate(err)
	}
	if err := r.writeItems(ctx, id, inv.Items); err != nil {
		return uuid.Nil, err
	}
	recorder := inv.CreatedByID
	if inv.SentByID != nil {
		recorder = inv.SentByID
	}
	return id, r.writeReadings(ctx, id, inv.Readings, recorder)
}

func (r *InvoiceRepository) writeItems(ctx context.Context, id uuid.UUID, items []domain.Item) error {
	if len(items) == 0 {
		return nil
	}
	p := sqlcgen.InsertInvoiceItemsParams{InvoiceID: id}
	for _, it := range items {
		p.Kinds = append(p.Kinds, it.Kind)
		p.Descriptions = append(p.Descriptions, it.Description)
		p.Quantities = append(p.Quantities, it.Quantity)
		p.UnitPrices = append(p.UnitPrices, it.UnitPriceVND)
		p.Amounts = append(p.Amounts, it.AmountVND)
		p.Orders = append(p.Orders, int16(it.DisplayOrder)) //nolint:gosec // a line number
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).InsertInvoiceItems(ctx, p))
}

// writeReadings stores the readings of the bill: a meter read again keeps its place, a meter no longer read goes.
func (r *InvoiceRepository) writeReadings(ctx context.Context, id uuid.UUID, readings []domain.Reading, recordedBy *uuid.UUID) error {
	q := postgres.Queries(ctx, r.pool)
	keep := make([]uuid.UUID, len(readings))
	for i, m := range readings {
		keep[i] = m.ChargeID
	}
	if err := q.DeleteMeterReadingsNotIn(ctx, sqlcgen.DeleteMeterReadingsNotInParams{InvoiceID: id, ChargeIds: keep}); err != nil {
		return postgres.Translate(err)
	}
	if len(readings) == 0 {
		return nil
	}
	p := sqlcgen.UpsertMeterReadingsParams{InvoiceID: id, RecordedByID: recordedBy}
	for _, m := range readings {
		p.ReadOn = m.ReadOn
		p.ChargeIds = append(p.ChargeIds, m.ChargeID)
		p.ReadingValues = append(p.ReadingValues, m.Value)
		p.Consumptions = append(p.Consumptions, m.Consumption)
		p.UnitPrices = append(p.UnitPrices, m.UnitPriceVND)
	}
	return postgres.Translate(q.UpsertMeterReadings(ctx, p))
}

// Replace writes what a correction changes: the content, the lines and the readings.
func (r *InvoiceRepository) Replace(ctx context.Context, inv domain.Invoice) error {
	q := postgres.Queries(ctx, r.pool)
	if err := q.UpdateInvoiceContent(ctx, sqlcgen.UpdateInvoiceContentParams{
		ID: inv.ID, Name: inv.Name, PaymentLabel: inv.PaymentLabel, PaymentCode: inv.PaymentCode, TotalAmountVnd: inv.TotalVND,
		Note: inv.Note, DueAt: inv.DueAt,
	}); err != nil {
		return postgres.Translate(err)
	}
	if err := q.DeleteInvoiceItems(ctx, inv.ID); err != nil {
		return postgres.Translate(err)
	}
	if err := r.writeItems(ctx, inv.ID, inv.Items); err != nil {
		return err
	}
	return r.writeReadings(ctx, inv.ID, inv.Readings, inv.CreatedByID)
}

// SaveState writes the status, the sending and payment times and the delivery.
func (r *InvoiceRepository) SaveState(ctx context.Context, inv domain.Invoice) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateInvoiceState(ctx, sqlcgen.UpdateInvoiceStateParams{
		ID: inv.ID, Status: string(inv.Status), SentAt: inv.SentAt, SentVia: sourceToDB(inv.SentVia), SentByID: inv.SentByID,
		PaidAt: inv.PaidAt, Delivery: string(inv.Delivery),
	}))
}

// SaveShare writes the link of the bill; an empty Share removes it.
func (r *InvoiceRepository) SaveShare(ctx context.Context, id uuid.UUID, s domain.Share) error {
	p := sqlcgen.UpdateInvoiceShareParams{ID: id}
	if s.TokenHash != "" {
		p.ShareTokenHash, p.ShareTokenSealed, p.ShareExpiresAt = &s.TokenHash, &s.Sealed, s.ExpiresAt
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpdateInvoiceShare(ctx, p))
}

// Delete removes the bill with its lines and readings for good.
func (r *InvoiceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).DeleteInvoice(ctx, id))
}

// BilledPeriods returns the periods of the contract that have a bill.
func (r *InvoiceRepository) BilledPeriods(ctx context.Context, contractID uuid.UUID) ([]domain.BilledPeriod, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListBilledPeriods(ctx, contractID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.BilledPeriod, len(rows))
	for i, row := range rows {
		out[i] = domain.BilledPeriod{InvoiceID: row.ID, PeriodStart: row.PeriodStart, Number: row.InvoiceNumber, Name: row.Name, SentAt: row.SentAt, SentVia: sourceOf(row.SentVia)}
	}
	return out, nil
}

// CountOfContract counts the bills of a contract.
func (r *InvoiceRepository) CountOfContract(ctx context.Context, contractID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountInvoicesOfContract(ctx, contractID)
	return int(n), postgres.Translate(err)
}

// MonthCounts counts the landlord's bills per month of their period start.
func (r *InvoiceRepository) MonthCounts(ctx context.Context, landlordID uuid.UUID, from, to time.Time) (map[string]int, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListInvoiceMonthCounts(ctx, sqlcgen.ListInvoiceMonthCountsParams{LandlordID: landlordID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.Month] = int(row.Total)
	}
	return out, nil
}

// NumberTaken says whether an invoice number is in use.
func (r *InvoiceRepository) NumberTaken(ctx context.Context, number string) (bool, error) {
	taken, err := postgres.Queries(ctx, r.pool).InvoiceNumberTaken(ctx, number)
	return taken, postgres.Translate(err)
}

// CodeTaken says whether a transfer text is in use by another bill.
func (r *InvoiceRepository) CodeTaken(ctx context.Context, code string, exceptID uuid.UUID) (bool, error) {
	taken, err := postgres.Queries(ctx, r.pool).PaymentCodeTaken(ctx, sqlcgen.PaymentCodeTakenParams{PaymentCode: code, ExceptID: exceptID})
	return taken, postgres.Translate(err)
}

// LastReadings returns the last stored index of each meter; a bill being corrected counts from what came before it.
func (r *InvoiceRepository) LastReadings(ctx context.Context, chargeIDs []uuid.UUID, editing *uuid.UUID) (map[uuid.UUID]float64, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListLastMeterReadings(ctx, sqlcgen.ListLastMeterReadingsParams{ChargeIds: chargeIDs, EditingID: editing})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]float64, len(rows))
	for _, row := range rows {
		out[row.ChargeID] = row.ReadingValue
	}
	return out, nil
}

// ReadingHistory returns a page of the meter readings of a room, newest first, and how many there are.
func (r *InvoiceRepository) ReadingHistory(ctx context.Context, roomID uuid.UUID, chargeID *uuid.UUID, limit, offset int) ([]domain.Reading, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListMeterReadingHistory(ctx, sqlcgen.ListMeterReadingHistoryParams{
		RoomID: &roomID, ChargeID: chargeID, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the caller
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountMeterReadingHistory(ctx, sqlcgen.CountMeterReadingHistoryParams{RoomID: &roomID, ChargeID: chargeID})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Reading, len(rows))
	for i, m := range rows {
		out[i] = readingOf(m.ID, m.ChargeID, m.InvoiceID, m.ReadOn, m.ReadingValue, m.Consumption, m.UnitPriceVnd, m.ChargeName, m.ChargeUnit)
	}
	return out, int(total), nil
}

// Due returns sent bills whose due date falls in one of the windows and that have not had that reminder yet.
func (r *InvoiceRepository) Due(ctx context.Context, soonFrom, soonTo, overdueFrom, overdueTo time.Time, after uuid.UUID, limit int) ([]domain.Detail, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListDueInvoices(ctx, sqlcgen.ListDueInvoicesParams{
		AfterID: after, SoonFrom: soonFrom, SoonTo: soonTo, OverdueFrom: overdueFrom, OverdueTo: overdueTo, PageLimit: int32(limit), //nolint:gosec // a batch size
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	details := make([]domain.Detail, len(rows))
	for i, row := range rows {
		details[i] = detailOf(sqlcgen.SearchInvoicesRow(row))
	}
	return details, nil
}

// MarkReminded records the reminder of each bill.
func (r *InvoiceRepository) MarkReminded(ctx context.Context, kind domain.Reminder, ids []uuid.UUID, on time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	q := postgres.Queries(ctx, r.pool)
	if kind == domain.ReminderOverdue {
		return postgres.Translate(q.MarkOverdueReminded(ctx, sqlcgen.MarkOverdueRemindedParams{RemindedOn: &on, Ids: ids}))
	}
	return postgres.Translate(q.MarkDueSoonReminded(ctx, sqlcgen.MarkDueSoonRemindedParams{RemindedOn: &on, Ids: ids}))
}
