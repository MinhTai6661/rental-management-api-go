// Package notificationpg is the database side of the notification use cases: the inbox repository. It returns domain
// types and translates driver errors (see postgres.Translate).
package notificationpg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
)

// NotificationRepository reads and writes the inbox.
type NotificationRepository struct{ pool *pgxpool.Pool }

// NewNotificationRepository returns a repository on the pool.
func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// Insert writes one message per user in one statement and returns the ids of the new rows.
func (r *NotificationRepository) Insert(ctx context.Context, userIDs []uuid.UUID, kind domain.Kind, params map[string]string, url string, audience domain.Audience) ([]uuid.UUID, error) {
	if params == nil {
		params = map[string]string{} // a nil map would be stored as JSON null, which the column refuses
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ids, err := postgres.Queries(ctx, r.pool).InsertNotifications(ctx, sqlcgen.InsertNotificationsParams{
		UserIds: userIDs, Kind: string(kind), Params: raw, Url: nonEmpty(url), AudienceRole: nonEmpty(string(audience)),
	})
	return ids, postgres.Translate(err)
}

// List returns the newest messages first.
func (r *NotificationRepository) List(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered, unreadOnly bool, kind *domain.Kind, limit, offset int) ([]domain.Notification, error) {
	p := sqlcgen.ListNotificationsParams{
		UserID: userID, Filtered: filtered, Audience: string(only), UnreadOnly: unreadOnly,
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the use case
	}
	if kind != nil {
		k := string(*kind)
		p.Kind = &k
	}
	rows, err := postgres.Queries(ctx, r.pool).ListNotifications(ctx, p)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Notification, 0, len(rows))
	for _, row := range rows {
		n, err := notificationOf(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// UnreadCount counts the unread messages of the side of the inbox being looked at.
func (r *NotificationRepository) UnreadCount(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered bool) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountUnreadNotifications(ctx, sqlcgen.CountUnreadNotificationsParams{
		UserID: userID, Filtered: filtered, Audience: string(only),
	})
	return int(n), postgres.Translate(err)
}

// MarkRead marks one of the user's messages as read, keeping the first read time; shared.ErrNotFound for anybody
// else's.
func (r *NotificationRepository) MarkRead(ctx context.Context, userID, id uuid.UUID, at time.Time) (domain.Notification, error) {
	row, err := postgres.Queries(ctx, r.pool).MarkNotificationRead(ctx, sqlcgen.MarkNotificationReadParams{ReadAt: &at, ID: id, UserID: userID})
	if err != nil {
		return domain.Notification{}, postgres.Translate(err)
	}
	return notificationOf(row)
}

// MarkAllRead marks the unread messages of the side of the inbox being looked at as read.
func (r *NotificationRepository) MarkAllRead(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered bool, at time.Time) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).MarkAllNotificationsRead(ctx, sqlcgen.MarkAllNotificationsReadParams{
		ReadAt: &at, UserID: userID, Filtered: filtered, Audience: string(only),
	})
	return int(n), postgres.Translate(err)
}

// MarkManyRead marks the user's unread messages among ids as read.
func (r *NotificationRepository) MarkManyRead(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, at time.Time) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).MarkNotificationsRead(ctx, sqlcgen.MarkNotificationsReadParams{ReadAt: &at, UserID: userID, Ids: ids})
	return int(n), postgres.Translate(err)
}

// Delete deletes one of the user's messages and says whether it was there.
func (r *NotificationRepository) Delete(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	n, err := postgres.Queries(ctx, r.pool).DeleteNotification(ctx, sqlcgen.DeleteNotificationParams{ID: id, UserID: userID})
	return n > 0, postgres.Translate(err)
}

// DeleteMany deletes the user's messages among ids.
func (r *NotificationRepository) DeleteMany(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).DeleteNotifications(ctx, sqlcgen.DeleteNotificationsParams{UserID: userID, Ids: ids})
	return int(n), postgres.Translate(err)
}

// DeleteByScope clears the user's inbox of every role, or only what was read.
func (r *NotificationRepository) DeleteByScope(ctx context.Context, userID uuid.UUID, readOnly bool) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).DeleteNotificationsOfUser(ctx, sqlcgen.DeleteNotificationsOfUserParams{UserID: userID, ReadOnly: readOnly})
	return int(n), postgres.Translate(err)
}

// PruneBefore deletes up to batch messages older than before.
func (r *NotificationRepository) PruneBefore(ctx context.Context, before time.Time, batch int) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).PruneNotifications(ctx, sqlcgen.PruneNotificationsParams{
		Before: before, BatchSize: int32(batch), //nolint:gosec // a small constant of the use case
	})
	return int(n), postgres.Translate(err)
}

// AdminIDs are the active accounts holding the admin role.
func (r *NotificationRepository) AdminIDs(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ListAdminUserIDs(ctx)
	return ids, postgres.Translate(err)
}

// HasKind says whether the user already has a message of the kind.
func (r *NotificationRepository) HasKind(ctx context.Context, userID uuid.UUID, kind domain.Kind) (bool, error) {
	has, err := postgres.Queries(ctx, r.pool).UserHasNotificationOfKind(ctx, sqlcgen.UserHasNotificationOfKindParams{UserID: userID, Kind: string(kind)})
	return has, postgres.Translate(err)
}

// nonEmpty is a pointer to s, or nil for "" (a column that stays NULL).
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
