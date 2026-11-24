package notification

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
)

// NotificationRepository stores the inbox. Every read and write that belongs to one person takes their id, so
// somebody else's message is "not found" (shared.ErrNotFound) and never touched. The side of the inbox a person looks at
// travels as (only, filtered): when filtered, only the messages for that audience and the ones for every audience show.
type NotificationRepository interface {
	// Insert writes one message per user in one statement and returns the ids of the new rows.
	Insert(ctx context.Context, userIDs []uuid.UUID, kind domain.Kind, params map[string]string, url string, audience domain.Audience) ([]uuid.UUID, error)
	// List returns the newest messages first; the caller asks for one more than it shows, to know whether more follow.
	List(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered, unreadOnly bool, kind *domain.Kind, limit, offset int) ([]domain.Notification, error)
	UnreadCount(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered bool) (int, error)
	// MarkRead keeps the first read time of a message that was already read.
	MarkRead(ctx context.Context, userID, id uuid.UUID, at time.Time) (domain.Notification, error)
	MarkAllRead(ctx context.Context, userID uuid.UUID, only domain.Audience, filtered bool, at time.Time) (int, error)
	MarkManyRead(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, at time.Time) (int, error)
	Delete(ctx context.Context, userID, id uuid.UUID) (bool, error)
	DeleteMany(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (int, error)
	// DeleteByScope clears the person's whole inbox (all roles), or only what they read.
	DeleteByScope(ctx context.Context, userID uuid.UUID, readOnly bool) (int, error)
	// PruneBefore deletes up to batch messages older than before and says how many it deleted.
	PruneBefore(ctx context.Context, before time.Time, batch int) (int, error)
	// AdminIDs are the active accounts holding the admin role (super admins are not included).
	AdminIDs(ctx context.Context) ([]uuid.UUID, error)
	// HasKind says whether the user already has a message of the kind (the demo seed asks).
	HasKind(ctx context.Context, userID uuid.UUID, kind domain.Kind) (bool, error)
}

// TxManager runs a function in one transaction; the transaction travels in the context.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// AnnouncementRepository stores announcements. A message that does not exist is shared.ErrNotFound.
type AnnouncementRepository interface {
	Insert(ctx context.Context, a domain.Announcement) (uuid.UUID, error)
	// Update writes the content, the revision and the sender role of the message (not its dates, count or status).
	Update(ctx context.Context, a domain.Announcement) error
	Get(ctx context.Context, id uuid.UUID) (domain.AnnouncementDetail, error)
	// ForUpdate loads the message and locks its row until the transaction ends.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Announcement, error)
	// List returns a page of messages with their read counts and popup statistics, and how many there are in all.
	List(ctx context.Context, q domain.AnnouncementQuery, now time.Time) ([]domain.AnnouncementDetail, int, error)
	// RecipientIDs are the people a sender picked one by one.
	RecipientIDs(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error)
	ReplaceRecipients(ctx context.Context, id uuid.UUID, userIDs []uuid.UUID) error
	// SetPublished sets (or, with nil, clears) the time the message was put out.
	SetPublished(ctx context.Context, id uuid.UUID, at *time.Time) error
	// SetRecalled marks the message recalled and deletes its notifications; it returns how many it deleted.
	SetRecalled(ctx context.Context, id uuid.UUID, at time.Time) (int, error)
	SetRecipientCount(ctx context.Context, id uuid.UUID, n int) error
	// ClaimDelivery marks the notifications of the message as sent when they are due and not sent yet; false means
	// somebody else did, or it is not due. Concurrent callers get exactly one true.
	ClaimDelivery(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
	// DeliverNotifications writes one notification per person of the audience (once each) and returns their ids.
	DeliverNotifications(ctx context.Context, id uuid.UUID, f domain.AudienceFilter, params map[string]string, audience domain.Audience) ([]uuid.UUID, error)
	// Due returns up to limit published messages whose notifications are due.
	Due(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
	CountAudience(ctx context.Context, f domain.AudienceFilter) (int, error)
	// FilterPeople returns the ones of ids that belong to the audience.
	FilterPeople(ctx context.Context, f domain.AudienceFilter, ids []uuid.UUID) ([]uuid.UUID, error)
	// People searches the running tenants of a landlord by name or email.
	People(ctx context.Context, landlordID uuid.UUID, search string, limit, offset int) ([]domain.Person, int, error)
	WelcomeExists(ctx context.Context) (bool, error)
	// Find returns the message without locking it or reading what happened to it.
	Find(ctx context.Context, id uuid.UUID) (domain.Announcement, error)
	// PopupCandidates returns up to limit popups that are out at now, most important first, with their pictures and
	// who wrote them.
	PopupCandidates(ctx context.Context, now time.Time, limit int) ([]domain.AnnouncementDetail, error)
}

// ImpressionRepository stores what people did with popups: one row per person and popup.
type ImpressionRepository interface {
	// Seen returns what the person did with each popup; the ones they never met are missing.
	Seen(ctx context.Context, userID uuid.UUID, announcementIDs []uuid.UUID) (map[uuid.UUID]domain.Seen, error)
	Upsert(ctx context.Context, announcementID, userID uuid.UUID, s domain.Seen) error
}

// ImageRepository stores the pictures of popups.
type ImageRepository interface {
	// List returns the pictures of a popup in order.
	List(ctx context.Context, announcementID uuid.UUID) ([]domain.Image, error)
	Insert(ctx context.Context, announcementID uuid.UUID, img domain.NewImage, order int) (domain.Image, error)
	// Update sets the position and the link ("" for none) of a picture; shared.ErrNotFound when it is not that popup's.
	Update(ctx context.Context, announcementID, imageID uuid.UUID, order int, link string) error
	Delete(ctx context.Context, announcementID, imageID uuid.UUID) error
}

// StatsRepository counts what people did with announcements; totals only, never who.
type StatsRepository interface {
	// Messages counts the messages of the cohort of the query.
	Messages(ctx context.Context, q domain.StatsQuery) (domain.Messages, error)
	// Totals counts what recipients did, for the channels the query asks for; recipients is how many people the
	// notifications went to.
	Totals(ctx context.Context, q domain.StatsQuery, recipients int) (domain.Totals, error)
	// Series counts the events of each day from q.First to q.Last.
	Series(ctx context.Context, q domain.StatsQuery) ([]domain.DayPoint, error)
}

// FileInfo describes a stored file.
type FileInfo struct {
	Size        int64
	ContentType string
}

// FileStore keeps the picture files. A key is written once and never overwritten.
type FileStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open returns the content of key (domain.ErrFileNotFound when there is none).
	Open(ctx context.Context, key string) (io.ReadCloser, FileInfo, error)
	// Delete removes the files; keys that do not exist are not an error.
	Delete(ctx context.Context, keys ...string) error
}

// ImageProcessor verifies an uploaded picture and makes what to store. A refusal is one of domain.ErrImageType,
// ErrImageCorrupt, ErrImageDimensions and ErrImageTooLarge.
type ImageProcessor interface {
	Process(ctx context.Context, data []byte) (domain.ProcessedImage, error)
}

// Ports to what later batches own. Each has a default that changes nothing, so the context works on its own.

// Event is what a live connection is told: the same three fields a stored message has.
type Event struct {
	Kind     domain.Kind
	Params   map[string]string
	URL      string
	Audience domain.Audience
}

// Pusher tells the people who are online right now. It is an accelerator: the row in the inbox is the record, so a
// failure here never fails a delivery.
type Pusher interface {
	Push(ctx context.Context, userIDs []uuid.UUID, e Event) error
}

// NoPush is the default: nobody is pushed to.
type NoPush struct{}

// Push does nothing.
func (NoPush) Push(context.Context, []uuid.UUID, Event) error { return nil }

// AuditEvent is something worth keeping in the audit trail.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action         string
	ActorID        uuid.UUID
	AnnouncementID uuid.UUID
	// OwnerID is the landlord whose message it is, nil for the platform's.
	OwnerID *uuid.UUID
	Meta    map[string]any
}

// AuditRecorder writes the audit trail; it runs in the transaction of ctx.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// Clock tells the time; tests fix it.
type Clock interface{ Now() time.Time }

// ClockFunc adapts a function (time.Now) to Clock.
type ClockFunc func() time.Time

// Now returns f().
func (f ClockFunc) Now() time.Time { return f() }
