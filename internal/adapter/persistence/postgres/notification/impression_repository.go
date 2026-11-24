package notificationpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
)

// ImpressionRepository stores what people did with popups.
type ImpressionRepository struct{ pool *pgxpool.Pool }

// NewImpressionRepository returns a repository on the pool.
func NewImpressionRepository(pool *pgxpool.Pool) *ImpressionRepository {
	return &ImpressionRepository{pool: pool}
}

// Seen returns what the person did with each of the popups; popups they never met are missing from the map.
func (r *ImpressionRepository) Seen(ctx context.Context, userID uuid.UUID, announcementIDs []uuid.UUID) (map[uuid.UUID]domain.Seen, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListAnnouncementImpressions(ctx, sqlcgen.ListAnnouncementImpressionsParams{UserID: userID, AnnouncementIds: announcementIDs})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[uuid.UUID]domain.Seen, len(rows))
	for _, row := range rows {
		out[row.AnnouncementID] = domain.Seen{
			Revision: int(row.Revision), SessionID: row.SessionID, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
			DismissedAt: row.DismissedAt, ClickedAt: row.ClickedAt, ViewCount: int(row.ViewCount), PeriodViewCount: int(row.PeriodViewCount),
			PeriodStartedAt: row.PeriodStartedAt,
		}
	}
	return out, nil
}

// Upsert writes the person's row for the popup.
func (r *ImpressionRepository) Upsert(ctx context.Context, announcementID, userID uuid.UUID, s domain.Seen) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).UpsertAnnouncementImpression(ctx, sqlcgen.UpsertAnnouncementImpressionParams{
		AnnouncementID: announcementID, UserID: userID, Revision: int32(s.Revision), ViewCount: int32(s.ViewCount), //nolint:gosec // small counters
		PeriodViewCount: int32(s.PeriodViewCount), PeriodStartedAt: s.PeriodStartedAt, SessionID: s.SessionID, //nolint:gosec // small counters
		FirstSeenAt: s.FirstSeenAt, LastSeenAt: s.LastSeenAt, DismissedAt: s.DismissedAt, ClickedAt: s.ClickedAt,
	}))
}
