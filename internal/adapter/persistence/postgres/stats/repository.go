// Package statspg is the database side of the statistics: it counts and sums the tables of the other contexts with
// queries of its own (the figures are GROUP BY work done in SQL) and hands the use cases plain facts. It only reads.
package statspg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/stats"
)

// Repository reads the figures.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository returns a repository on the pool.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// startOf is the first instant of a day in the zone people read dates in; the days of a range are midnight UTC.
func startOf(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, domain.Zone)
}

// instants turns the days [from, to] into the instants [from 00:00, to + 1 day 00:00) of the zone.
func instants(from, to time.Time) (time.Time, time.Time) {
	return startOf(from), startOf(to.AddDate(0, 0, 1))
}

// Rooms lists the rooms of the scope that were still there on from.
func (r *Repository) Rooms(ctx context.Context, scope domain.Scope, from time.Time) ([]domain.RoomFact, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsRooms(ctx, sqlcgen.StatsRoomsParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromAt: startOf(from)})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomFact, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomFact{ID: row.ID, BuildingID: row.BuildingID, BuildingName: row.BuildingName, Name: row.Name, Status: row.Status, CreatedOn: domain.Day(row.CreatedAt)}
		if row.DeletedAt != nil {
			d := domain.Day(*row.DeletedAt)
			out[i].DeletedOn = &d
		}
	}
	return out, nil
}

// RentedContracts lists the contracts that began and lasted into [from, to].
func (r *Repository) RentedContracts(ctx context.Context, scope domain.Scope, from, to time.Time) ([]domain.RentedContract, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsRentedSpans(ctx, sqlcgen.StatsRentedSpansParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RentedContract, len(rows))
	for i, row := range rows {
		out[i] = domain.RentedContract{RoomID: row.RoomID, StartsOn: dayOf(row.StartsOn), EndsOn: dayPtr(row.EndsOn), Ended: row.Status == "ended"}
	}
	return out, nil
}

// dayOf reads a date column as a day (midnight UTC), whatever zone the driver gave it in.
func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func dayPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := dayOf(*t)
	return &d
}

// ContractCounts counts the contracts of the standing rooms.
func (r *Repository) ContractCounts(ctx context.Context, scope domain.Scope, today, soon time.Time) (domain.ContractCounts, error) {
	row, err := postgres.Queries(ctx, r.pool).StatsContractCounts(ctx, sqlcgen.StatsContractCountsParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, Today: today, Soon: soon})
	if err != nil {
		return domain.ContractCounts{}, postgres.Translate(err)
	}
	return domain.ContractCounts{Active: int(row.Active), Pending: int(row.Pending), ExpiringSoon: int(row.ExpiringSoon)}, nil
}

// BilledByMonth sums the invoices sent or paid by the month their period starts.
func (r *Repository) BilledByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsBilledByMonth(ctx, sqlcgen.StatsBilledByMonthParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Month] = row.AmountVnd
	}
	return out, nil
}

// CollectedByMonth sums the confirmed payments by the month they were paid.
func (r *Repository) CollectedByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error) {
	fromAt, beforeAt := instants(from, to)
	rows, err := postgres.Queries(ctx, r.pool).StatsCollectedByMonth(ctx, sqlcgen.StatsCollectedByMonthParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromAt: fromAt, BeforeAt: beforeAt})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Month] = row.AmountVnd
	}
	return out, nil
}

// OutstandingByMonth sums what the sent invoices of each month still lack.
func (r *Repository) OutstandingByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsOutstandingByMonth(ctx, sqlcgen.StatsOutstandingByMonthParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Month] = row.AmountVnd
	}
	return out, nil
}

// Owed sums what is outstanding now and the part past its due date.
func (r *Repository) Owed(ctx context.Context, scope domain.Scope, now time.Time) (domain.Owed, error) {
	row, err := postgres.Queries(ctx, r.pool).StatsOwed(ctx, sqlcgen.StatsOwedParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, Now: now})
	if err != nil {
		return domain.Owed{}, postgres.Translate(err)
	}
	return domain.Owed{OutstandingVND: row.OutstandingVnd, OverdueVND: row.OverdueVnd, OverdueCount: int(row.OverdueCount)}, nil
}

// BilledByRoom sums the billed money of every room over [from, to].
func (r *Repository) BilledByRoom(ctx context.Context, scope domain.Scope, from, to time.Time) (map[uuid.UUID]int64, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsBilledByRoom(ctx, sqlcgen.StatsBilledByRoomParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		out[row.RoomID] = row.AmountVnd
	}
	return out, nil
}

// CollectedByRoom sums the collected money of every room over [from, to].
func (r *Repository) CollectedByRoom(ctx context.Context, scope domain.Scope, from, to time.Time) (map[uuid.UUID]int64, error) {
	fromAt, beforeAt := instants(from, to)
	rows, err := postgres.Queries(ctx, r.pool).StatsCollectedByRoom(ctx, sqlcgen.StatsCollectedByRoomParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromAt: fromAt, BeforeAt: beforeAt})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		out[row.RoomID] = row.AmountVnd
	}
	return out, nil
}

// Utilities sums the meter readings of electricity and water by month and kind.
func (r *Repository) Utilities(ctx context.Context, scope domain.Scope, from, to time.Time) ([]domain.UtilityUse, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsUtilitiesByMonth(ctx, sqlcgen.StatsUtilitiesByMonthParams{LandlordID: scope.LandlordID, BuildingID: scope.BuildingID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.UtilityUse, len(rows))
	for i, row := range rows {
		out[i] = domain.UtilityUse{Month: row.Month, Kind: domain.UtilityKind(row.Kind), Consumption: row.Consumption, CostVND: row.CostVnd}
	}
	return out, nil
}
