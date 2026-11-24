package notificationpg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
)

// StatsRepository counts what people did with announcements: totals only, never who.
type StatsRepository struct{ pool *pgxpool.Pool }

// NewStatsRepository returns a repository on the pool.
func NewStatsRepository(pool *pgxpool.Pool) *StatsRepository { return &StatsRepository{pool: pool} }

// Messages counts the messages of the query's cohort.
func (r *StatsRepository) Messages(ctx context.Context, q domain.StatsQuery) (domain.Messages, error) {
	row, err := postgres.Queries(ctx, r.pool).StatsCohortMessages(ctx, sqlcgen.StatsCohortMessagesParams{FromAt: q.CohortFrom, ToAt: q.CohortTo, LandlordID: q.LandlordID})
	if err != nil {
		return domain.Messages{}, postgres.Translate(err)
	}
	return domain.Messages{Total: int(row.Total), WithNotification: int(row.WithNotification), WithPopup: int(row.WithPopup), Recipients: int(row.Recipients)}, nil
}

// Totals counts what recipients did, for the channels the query asks for. recipients is how many people the
// notifications went to (the rates are measured against it).
func (r *StatsRepository) Totals(ctx context.Context, q domain.StatsQuery, recipients int) (domain.Totals, error) {
	var totals domain.Totals
	queries := postgres.Queries(ctx, r.pool)
	if q.WantsNotification() {
		read, err := queries.StatsNotificationRead(ctx, sqlcgen.StatsNotificationReadParams{AnnouncementID: q.AnnouncementID, FromAt: q.CohortFrom, ToAt: q.CohortTo, LandlordID: q.LandlordID})
		if err != nil {
			return totals, postgres.Translate(err)
		}
		totals.Notification = &domain.NotificationTotals{Recipients: recipients, Read: int(read), ReadRate: domain.Percent(int(read), recipients)}
	}
	if q.WantsPopup() {
		row, err := queries.StatsPopupTotals(ctx, sqlcgen.StatsPopupTotalsParams{AnnouncementID: q.AnnouncementID, FromAt: q.CohortFrom, ToAt: q.CohortTo, LandlordID: q.LandlordID})
		if err != nil {
			return totals, postgres.Translate(err)
		}
		viewers, views, closed, clicked := int(row.Viewers), int(row.Views), int(row.Closed), int(row.Clicked)
		totals.Popup = &domain.PopupTotals{
			Viewers: viewers, Views: views, Closed: closed, Clicked: clicked, ViewsPerViewer: domain.PerViewer(views, viewers),
			CloseRate: domain.Percent(closed, viewers), ClickRate: domain.Percent(clicked, viewers),
		}
	}
	return totals, nil
}

// Series counts the events of each day from q.First to q.Last (one point per day, even without events).
func (r *StatsRepository) Series(ctx context.Context, q domain.StatsQuery) ([]domain.DayPoint, error) {
	rows, err := postgres.Queries(ctx, r.pool).StatsDailyEvents(ctx, sqlcgen.StatsDailyEventsParams{
		FirstDay: q.First, LastDay: q.Last, AnnouncementID: q.AnnouncementID, LandlordID: q.LandlordID,
		WithPopup: q.WantsPopup(), WithNotification: q.WantsNotification(), FromAt: q.First, ToAt: q.Last.AddDate(0, 0, 1),
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.DayPoint, len(rows))
	for i, row := range rows {
		out[i] = domain.DayPoint{Day: row.Day, Viewers: int(row.Viewers), Closed: int(row.Closed), Clicked: int(row.Clicked), Read: int(row.Read)}
	}
	return out, nil
}
