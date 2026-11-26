// Package report holds the use cases of problem reports: a tenant reports on a room they rent (to its landlord) or on
// the app (to the admins), a landlord reports on the app, and the person a report goes to answers it. It declares the
// ports it needs (ports.go) and imports no framework, driver or other context.
package report

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
	"rental-management/internal/domain/shared"
)

// The kinds of notification the report use cases send; the web translates each with the notification's params.
const (
	// NotifyReceived tells the recipient about a new report.
	NotifyReceived = "report_received"
	// NotifyReplied tells the recipient the author wrote again.
	NotifyReplied = "report_replied"
	// NotifyAnswered tells the author the recipient answered or resolved the report.
	NotifyAnswered = "report_answered"
)

// The audit actions the report use cases record.
const (
	AuditCreated       = "report.created"
	AuditReplied       = "report.replied"
	AuditStatusChanged = "report.status_changed"
	AuditDeleted       = "report.deleted"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx      TxManager
	Reports ReportRepository
	Rooms   RoomReader
	// Notifier, Audit and Events default to doing nothing.
	Notifier Notifier
	Audit    AuditRecorder
	Events   ReportEvents
	Clock    Clock
	Log      *slog.Logger
}

// core is what the use case groups share.
type core struct{ Deps }

func newCore(d Deps) *core {
	c := &core{Deps: d}
	if c.Notifier == nil {
		c.Notifier = NoNotifier{}
	}
	if c.Audit == nil {
		c.Audit = NoAudit{}
	}
	if c.Events == nil {
		c.Events = NoEvents{}
	}
	if c.Clock == nil {
		c.Clock = ClockFunc(time.Now)
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	return c
}

// Reports are the use cases of writing, reading, answering and deleting reports.
type Reports struct{ *core }

// NewReports returns the use cases.
func NewReports(d Deps) *Reports { return &Reports{newCore(d)} }

// CreateInput is a report as the author wrote it.
type CreateInput struct {
	Kind    domain.Kind
	RoomID  *uuid.UUID
	Subject string
	Body    string
}

// Create stores a report and tells whoever it goes to.
func (u *Reports) Create(ctx context.Context, viewer domain.Viewer, in CreateInput) (domain.View, error) {
	if !domain.CanCreate(viewer.Role, in.Kind) {
		return domain.View{}, domain.ErrForbidden
	}
	var room *RoomRef
	if in.Kind == domain.KindRoom {
		if in.RoomID == nil {
			return domain.View{}, domain.ErrRoomRequired
		}
		found, err := u.Rooms.RentedBy(ctx, viewer.UserID, *in.RoomID)
		if errors.Is(err, shared.ErrNotFound) {
			return domain.View{}, domain.ErrRoomNotYours
		}
		if err != nil {
			return domain.View{}, err
		}
		room = &found
	}
	var id uuid.UUID
	err := u.Tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		id, err = u.Reports.Insert(ctx, domain.NewReport{
			ReporterID: viewer.UserID, Kind: in.Kind, RoomID: in.RoomID, Subject: in.Subject, Body: in.Body, At: u.Clock.Now(),
		})
		if err != nil {
			return err
		}
		event := AuditEvent{Action: AuditCreated, ActorID: viewer.UserID, ReportID: id, TargetUserID: &viewer.UserID, Meta: map[string]any{"type": string(in.Kind)}}
		if room != nil {
			event.RoomID, event.OwnerID = room.ID, room.LandlordID
		}
		return u.Audit.Record(ctx, event)
	})
	if err != nil {
		return domain.View{}, err
	}
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return domain.View{}, err
	}
	u.tellRecipient(ctx, view, NotifyReceived, map[string]string{
		"subject": view.Subject, "reporter": view.Reporter.Name, "room": view.RoomName, "report_id": id.String(),
	})
	return view, nil
}

// Get returns a report the viewer may see.
func (u *Reports) Get(ctx context.Context, viewer domain.Viewer, id uuid.UUID) (domain.View, error) {
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return domain.View{}, notFound(err)
	}
	if !domain.CanView(viewer, view.Report) {
		return domain.View{}, domain.ErrNotFound
	}
	return view, nil
}

// Mine lists the reports the viewer wrote (the ones they deleted are gone), newest first.
func (u *Reports) Mine(ctx context.Context, viewer domain.Viewer, f domain.Filter, limit, offset int) ([]domain.View, int, error) {
	return u.Reports.ListMine(ctx, viewer.UserID, f, limit, offset)
}

// Inbox lists the reports the viewer has to answer: the room reports of a landlord's rooms, the feature reports for an
// admin, everything for a super admin. A role that answers nothing gets an empty page.
func (u *Reports) Inbox(ctx context.Context, viewer domain.Viewer, f domain.Filter, limit, offset int) ([]domain.View, int, error) {
	scope := domain.InboxOf(viewer)
	if scope == (domain.InboxScope{}) {
		return []domain.View{}, 0, nil
	}
	return u.Reports.ListInbox(ctx, scope, f, limit, offset)
}

// Conversation is a page of the messages under a report, with the report (the side each author wrote from depends on it).
type Conversation struct {
	Report  domain.Report
	Replies []domain.Reply
	Total   int
}

// Replies lists the conversation under a report the viewer may see, newest message first.
func (u *Reports) Replies(ctx context.Context, viewer domain.Viewer, id uuid.UUID, limit, offset int) (Conversation, error) {
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return Conversation{}, notFound(err)
	}
	if !domain.CanView(viewer, view.Report) {
		return Conversation{}, domain.ErrNotFound
	}
	replies, total, err := u.Reports.ListReplies(ctx, id, limit, offset)
	return Conversation{Report: view.Report, Replies: replies, Total: total}, err
}

// Reply adds a message to the conversation. The other side is told: the author when the recipient writes, the
// recipient when the author does.
// Replied is what writing a message gives back: the report as it is now and the message that was stored.
type Replied struct {
	View  domain.View
	Reply domain.Reply
}

func (u *Reports) Reply(ctx context.Context, viewer domain.Viewer, id uuid.UUID, body string) (Replied, error) {
	var written domain.Reply
	err := u.Tx.WithinTx(ctx, func(ctx context.Context) error {
		report, err := u.Reports.Lock(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := domain.Authorize(viewer, report, domain.CanReply); err != nil {
			return err
		}
		if written, err = u.Reports.InsertReply(ctx, id, viewer.UserID, body, u.Clock.Now()); err != nil {
			return err
		}
		// The message itself stays out of the trail: only that it was written, and how long.
		return u.Audit.Record(ctx, u.auditOf(AuditReplied, viewer, report, nil, map[string]any{"length": len([]rune(body))}))
	})
	if err != nil {
		return Replied{}, err
	}
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return Replied{}, err
	}
	params := map[string]string{"subject": view.Subject, "author": written.Author.Name, "report_id": id.String()}
	if domain.IsAuthor(viewer, view.Report) {
		u.tellRecipient(ctx, view, NotifyReplied, params)
	} else {
		u.tellAuthor(ctx, view, params)
	}
	u.publish(ctx, ReportEvent{Kind: EventReply, Report: view.Report, Reply: &written})
	return Replied{View: view, Reply: written}, nil
}

// SetStatus sets where the report stands; only the recipient may. The author is told when it is resolved.
func (u *Reports) SetStatus(ctx context.Context, viewer domain.Viewer, id uuid.UUID, status domain.Status) (domain.View, error) {
	changed := false
	err := u.Tx.WithinTx(ctx, func(ctx context.Context) error {
		report, err := u.Reports.Lock(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := domain.Authorize(viewer, report, domain.CanSetStatus); err != nil {
			return err
		}
		if report.Status == status {
			return nil
		}
		changed = true
		if err := u.Reports.SetStatus(ctx, id, status, u.Clock.Now()); err != nil {
			return err
		}
		changes := map[string]any{"status": map[string]any{"from": string(report.Status), "to": string(status)}}
		return u.Audit.Record(ctx, u.auditOf(AuditStatusChanged, viewer, report, changes, nil))
	})
	if err != nil {
		return domain.View{}, err
	}
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return domain.View{}, err
	}
	if !changed {
		return view, nil
	}
	if status == domain.StatusResolved {
		u.tellAuthor(ctx, view, map[string]string{"subject": view.Subject, "author": "", "report_id": id.String()})
	}
	u.publish(ctx, ReportEvent{Kind: EventStatus, Report: view.Report})
	return view, nil
}

// Delete hides a report from its author; the recipient keeps it as read-only history.
func (u *Reports) Delete(ctx context.Context, viewer domain.Viewer, id uuid.UUID) error {
	err := u.Tx.WithinTx(ctx, func(ctx context.Context) error {
		report, err := u.Reports.Lock(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := domain.Authorize(viewer, report, domain.CanDelete); err != nil {
			return err
		}
		if err := u.Reports.SoftDelete(ctx, id, u.Clock.Now()); err != nil {
			return err
		}
		return u.Audit.Record(ctx, u.auditOf(AuditDeleted, viewer, report, nil, nil))
	})
	// Nobody is told live: the author's side is gone, and the recipient finds the report read only when it is next read.
	return err
}

// Typing tells the other side that the viewer is writing. Nothing is stored; the viewer must be allowed to write.
func (u *Reports) Typing(ctx context.Context, viewer domain.Viewer, id uuid.UUID) error {
	view, err := u.Reports.Get(ctx, id)
	if err != nil {
		return notFound(err)
	}
	if err := domain.Authorize(viewer, view.Report, domain.CanReply); err != nil {
		return err
	}
	u.publish(ctx, ReportEvent{Kind: EventTyping, Report: view.Report, UserID: viewer.UserID})
	return nil
}

func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// auditOf builds the audit event of an action on a report: the owner of the room is the owner of the data.
func (u *Reports) auditOf(action string, viewer domain.Viewer, r domain.Report, changes, meta map[string]any) AuditEvent {
	e := AuditEvent{Action: action, ActorID: viewer.UserID, ReportID: r.ID, TargetUserID: &r.ReporterID, Changes: changes, Meta: meta}
	if r.RoomID != nil {
		e.RoomID = *r.RoomID
	}
	if r.RoomLandlordID != nil {
		e.OwnerID = *r.RoomLandlordID
	}
	return e
}
