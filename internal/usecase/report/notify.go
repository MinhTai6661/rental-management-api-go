package report

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
)

// reportURL is the page of the web app that opens a report.
func reportURL(id uuid.UUID) string { return "/reports?open=" + id.String() }

// tellRecipient sends a message to whoever the report goes to. Delivery is after the commit; a failure is logged and
// never undoes what was done.
func (u *Reports) tellRecipient(ctx context.Context, v domain.View, kind string, params map[string]string) {
	to, ok := domain.RecipientOf(v.Report)
	if !ok {
		return
	}
	u.notify(ctx, Notification{UserID: to.UserID, ToAdmins: to.Admins, Kind: kind, Params: params, URL: reportURL(v.ID)})
}

// tellAuthor sends a message to the author of the report.
func (u *Reports) tellAuthor(ctx context.Context, v domain.View, params map[string]string) {
	u.notify(ctx, Notification{UserID: v.ReporterID, Kind: NotifyAnswered, Params: params, URL: reportURL(v.ID)})
}

func (u *Reports) notify(ctx context.Context, n Notification) {
	if err := u.Notifier.Notify(ctx, n); err != nil {
		u.Log.Warn("notify about a report", "error", err, "kind", n.Kind)
	}
}

// publish tells the open conversations of the report. It is best effort.
func (u *Reports) publish(ctx context.Context, e ReportEvent) {
	if err := u.Events.Publish(ctx, e); err != nil {
		u.Log.Warn("publish a report event", "error", err, "kind", e.Kind)
	}
}
