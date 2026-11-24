package notification

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// Viewer is the person looking at an inbox and the role they act in (it decides which side of the inbox they see).
type Viewer struct {
	UserID uuid.UUID
	Role   string
}

// Page is one page of the inbox, a feed: it says whether more follow instead of counting them.
type Page struct {
	Items       []domain.Notification
	HasMore     bool
	UnreadCount int
}

// Inbox is what a person does with their own messages. Everything is scoped to the viewer.
type Inbox struct{ *core }

// NewInbox returns the use case.
func NewInbox(d Deps, o Options) *Inbox { return &Inbox{newCore(d, o)} }

// List returns a page of the viewer's messages, newest first, with how many are unread.
func (i *Inbox) List(ctx context.Context, v Viewer, unreadOnly bool, kind *domain.Kind, limit, offset int) (Page, error) {
	only, filtered := domain.VisibleTo(v.Role)
	rows, err := i.notifications.List(ctx, v.UserID, only, filtered, unreadOnly, kind, limit+1, offset)
	if err != nil {
		return Page{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	unread, err := i.notifications.UnreadCount(ctx, v.UserID, only, filtered)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: rows, HasMore: hasMore, UnreadCount: unread}, nil
}

// Latest returns the newest messages for the bell (limit is clamped to 1..20, default 8) and the unread count; it is
// bounded, so it needs no pages.
func (i *Inbox) Latest(ctx context.Context, v Viewer, limit int) (Page, error) {
	return i.List(ctx, v, false, nil, domain.ClampLatest(limit), 0)
}

// UnreadCount is how many messages the viewer has not read.
func (i *Inbox) UnreadCount(ctx context.Context, v Viewer) (int, error) {
	only, filtered := domain.VisibleTo(v.Role)
	return i.notifications.UnreadCount(ctx, v.UserID, only, filtered)
}

// MarkRead marks one of the viewer's messages as read (shared.ErrNotFound for anybody else's). Reading it again keeps
// the first read time.
func (i *Inbox) MarkRead(ctx context.Context, v Viewer, id uuid.UUID) (domain.Notification, error) {
	return i.notifications.MarkRead(ctx, v.UserID, id, i.clock.Now())
}

// MarkAllRead marks everything in the viewer's inbox of the role they act in as read and says how many it changed.
func (i *Inbox) MarkAllRead(ctx context.Context, v Viewer) (int, error) {
	only, filtered := domain.VisibleTo(v.Role)
	return i.notifications.MarkAllRead(ctx, v.UserID, only, filtered, i.clock.Now())
}

// Bulk reads or deletes the messages named, at most domain.MaxBulkIDs of them. Ids that are not the viewer's are
// ignored (they are not counted, and nothing says they exist). It returns how many messages it changed.
func (i *Inbox) Bulk(ctx context.Context, v Viewer, action domain.BulkAction, ids []uuid.UUID) (int, error) {
	ids = uniqueIDs(ids)
	if len(ids) > domain.MaxBulkIDs {
		return 0, domain.ErrTooManyIDs
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if action == domain.BulkDelete {
		return i.notifications.DeleteMany(ctx, v.UserID, ids)
	}
	return i.notifications.MarkManyRead(ctx, v.UserID, ids, i.clock.Now())
}

// Delete removes one of the viewer's messages (shared.ErrNotFound for anybody else's).
func (i *Inbox) Delete(ctx context.Context, v Viewer, id uuid.UUID) error {
	deleted, err := i.notifications.Delete(ctx, v.UserID, id)
	if err != nil {
		return err
	}
	if !deleted {
		return shared.ErrNotFound
	}
	return nil
}

// DeleteMany clears the viewer's inbox: what they read, or everything. It covers every role of the account, because
// clearing is the person's own housekeeping.
func (i *Inbox) DeleteMany(ctx context.Context, v Viewer, scope domain.DeleteScope) (int, error) {
	return i.notifications.DeleteByScope(ctx, v.UserID, scope == domain.DeleteRead)
}
