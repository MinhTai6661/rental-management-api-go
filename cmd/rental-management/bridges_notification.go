package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	"rental-management/internal/adapter/filestore"
	"rental-management/internal/adapter/imageproc"
	"rental-management/internal/adapter/persistence/postgres"
	notificationpg "rental-management/internal/adapter/persistence/postgres/notification"
	contractuc "rental-management/internal/usecase/contract"
	invoiceuc "rental-management/internal/usecase/invoice"
	listinguc "rental-management/internal/usecase/listing"
	notificationuc "rental-management/internal/usecase/notification"
	paymentuc "rental-management/internal/usecase/payment"
	planuc "rental-management/internal/usecase/plan"
	"rental-management/pkg/imaging"
	"rental-management/pkg/storage"
)

// Every context declares its own Notifier port and none imports another, so the notification use cases reach them
// through five small adapters here: each maps the context's Notification to the one Message of the inbox. A kind a
// context sends that the catalog does not know is refused by Deliveries and only logged by the context (the unit test
// of this file lists every Notify* constant, so a new kind cannot be forgotten).

// messenger is what the adapters need of the notification use cases; *notificationuc.Deliveries is one, and the test
// of this file fakes it.
type messenger interface {
	Send(ctx context.Context, m notificationuc.Message) error
	SendToAdmins(ctx context.Context, m notificationuc.Message) error
}

// messageOf builds the message for one user.
func messageOf(user uuid.UUID, kind string, params map[string]string, url, audience string) notificationuc.Message {
	return notificationuc.Message{UserIDs: []uuid.UUID{user}, Kind: kind, Params: params, URL: url, Audience: audience}
}

// sendTo sends to one user, or to every admin.
func sendTo(ctx context.Context, m messenger, toAdmins bool, msg notificationuc.Message) error {
	if toAdmins {
		return m.SendToAdmins(ctx, msg)
	}
	return m.Send(ctx, msg)
}

// contractNotifier is the contract context's Notifier.
type contractNotifier struct{ m messenger }

// Notify sends a contract message to its user.
func (b contractNotifier) Notify(ctx context.Context, n contractuc.Notification) error {
	return b.m.Send(ctx, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// planNotifier is the plan context's Notifier.
type planNotifier struct{ m messenger }

// Notify sends a subscription message to its user, or to every admin.
func (b planNotifier) Notify(ctx context.Context, n planuc.Notification) error {
	return sendTo(ctx, b.m, n.ToAdmins, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// listingNotifier is the listing context's Notifier.
type listingNotifier struct{ m messenger }

// Notify sends a listing message to its user, or to every admin.
func (b listingNotifier) Notify(ctx context.Context, n listinguc.Notification) error {
	return sendTo(ctx, b.m, n.ToAdmins, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// invoiceNotifier is the invoice context's Notifier.
type invoiceNotifier struct{ m messenger }

// Notify sends a bill message to its user.
func (b invoiceNotifier) Notify(ctx context.Context, n invoiceuc.Notification) error {
	return b.m.Send(ctx, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// paymentNotifier is the payment context's Notifier.
type paymentNotifier struct{ m messenger }

// Notify sends a payment message to its user.
func (b paymentNotifier) Notify(ctx context.Context, n paymentuc.Notification) error {
	return b.m.Send(ctx, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// notifications is the use cases of the in-app inbox, on Postgres.
type notifications struct {
	deliveries *notificationuc.Deliveries
	inbox      *notificationuc.Inbox
	pruner     *notificationuc.Pruner
	seeder     *notificationuc.Seeder
	// announcements are the messages of admins and landlords; they write into the same inbox.
	announcements *notificationuc.Announcements
	popups        *notificationuc.Popups
	images        *notificationuc.Images
	stats         *notificationuc.Statistics
}

// newNotifications builds the notification use cases. It is built first: every other context gets its Notifier from the
// deliveries (a nil *Deliveries, as the seed passes, leaves a context's Notifier at its no-op default).
//
// files is the byte store of the popup pictures; the commands that never touch pictures pass nil. pusher tells the
// people who are online (nil: nobody).
func newNotifications(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, files storage.Storage, pusher notificationuc.Pusher) notifications {
	deps := notificationuc.Deps{
		Tx: postgres.NewTxManager(pool), Notifications: notificationpg.NewNotificationRepository(pool),
		Announcements: notificationpg.NewAnnouncementRepository(pool), Impressions: notificationpg.NewImpressionRepository(pool),
		Images: notificationpg.NewImageRepository(pool), Stats: notificationpg.NewStatsRepository(pool), Log: log, Pusher: pusher,
	}
	if files != nil {
		deps.Files = announcementFiles{inner: filestore.New(files)}
		deps.Imager = announcementImages{inner: imageproc.New(imaging.NewProcessor(imaging.Limits{
			MaxBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension, ThumbMaxSize: cfg.MediaThumbnailMaxSize,
		}, cfg.ImageProcessingConcurrency))}
	}
	opts := notificationuc.Options{
		RetentionDays: cfg.NotificationRetentionDays, ImageMaxPlatform: cfg.AnnouncementImageMaxPlatform, ImageMaxLandlord: cfg.AnnouncementImageMaxLandlord,
		MaxDimension: cfg.MediaMaxDimension, MaxUploadBytes: cfg.MediaMaxUploadBytes,
	}
	return notifications{
		popups: notificationuc.NewPopups(deps, opts), images: notificationuc.NewImages(deps, opts), stats: notificationuc.NewStatistics(deps, opts),
		deliveries: notificationuc.NewDeliveries(deps, opts), inbox: notificationuc.NewInbox(deps, opts),
		pruner: notificationuc.NewPruner(deps, opts), seeder: notificationuc.NewSeeder(deps, opts),
		announcements: notificationuc.NewAnnouncements(deps, opts),
	}
}

var (
	_ contractuc.Notifier = contractNotifier{}
	_ planuc.Notifier     = planNotifier{}
	_ listinguc.Notifier  = listingNotifier{}
	_ invoiceuc.Notifier  = invoiceNotifier{}
	_ paymentuc.Notifier  = paymentNotifier{}
	_ messenger           = (*notificationuc.Deliveries)(nil)
)
