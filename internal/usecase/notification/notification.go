// Package notification holds the use cases of the in-app inbox: the other contexts send messages through Deliveries
// (each one adapts its own notifier port to it in cmd/rental-management), and a person reads, marks and clears their
// own inbox through Inbox. It declares the ports it needs (ports.go) and imports no framework, driver or other context.
package notification

import (
	"io"
	"log/slog"
	"time"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx            TxManager
	Notifications NotificationRepository
	Announcements AnnouncementRepository
	Impressions   ImpressionRepository
	Images        ImageRepository
	Stats         StatsRepository
	// Files and Imager keep the pictures of popups; nil when the process has no storage (the commands).
	Files  FileStore
	Imager ImageProcessor
	Pusher Pusher
	Audit  AuditRecorder
	Clock  Clock
	Log    *slog.Logger
}

// Options are the settings of the use cases.
type Options struct {
	// RetentionDays is how long a message is kept before the daily prune deletes it (default 90).
	RetentionDays int
	// ImageMaxPlatform and ImageMaxLandlord are how many pictures a popup may have (defaults 10 and 3).
	ImageMaxPlatform, ImageMaxLandlord int
	// MaxDimension and MaxUploadBytes are the picture limits, named in the errors about a refused picture.
	MaxDimension, MaxUploadBytes int
}

// core is what the use case groups share.
type core struct {
	tx            TxManager
	notifications NotificationRepository
	announcements AnnouncementRepository
	impressions   ImpressionRepository
	images        ImageRepository
	stats         StatsRepository
	files         FileStore
	imager        ImageProcessor
	pusher        Pusher
	audit         AuditRecorder
	clock         Clock
	log           *slog.Logger
	opts          Options
}

func newCore(d Deps, o Options) *core {
	if o.RetentionDays <= 0 {
		o.RetentionDays = 90
	}
	if o.ImageMaxPlatform <= 0 {
		o.ImageMaxPlatform = 10
	}
	if o.ImageMaxLandlord <= 0 {
		o.ImageMaxLandlord = 3
	}
	c := &core{tx: d.Tx, notifications: d.Notifications, announcements: d.Announcements, impressions: d.Impressions, images: d.Images, stats: d.Stats, files: d.Files, imager: d.Imager, pusher: d.Pusher, audit: d.Audit, clock: d.Clock, log: d.Log, opts: o}
	if c.pusher == nil {
		c.pusher = NoPush{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return c
}
