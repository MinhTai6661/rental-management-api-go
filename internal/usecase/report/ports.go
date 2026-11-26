package report

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
)

// The interfaces below are what the report use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database. Repository
// lookups return shared.ErrNotFound when there is no such row.

// TxManager runs fn in one transaction; repository calls made with the ctx given to fn join it.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ReportRepository stores reports and the conversations under them.
type ReportRepository interface {
	// Insert stores a report (status open) and returns its id.
	Insert(ctx context.Context, r domain.NewReport) (uuid.UUID, error)
	// Get returns the report as a reader sees it; a deleted report is returned too (see Report.Deleted).
	Get(ctx context.Context, id uuid.UUID) (domain.View, error)
	// Lock returns the report and holds its row until the transaction ends, so a reply and a delete cannot cross.
	Lock(ctx context.Context, id uuid.UUID) (domain.Report, error)
	SetStatus(ctx context.Context, id uuid.UUID, status domain.Status, at time.Time) error
	// SoftDelete hides the report from its author.
	SoftDelete(ctx context.Context, id uuid.UUID, at time.Time) error
	// ListMine returns a page of the author's reports that are not deleted, newest first, and how many match.
	ListMine(ctx context.Context, authorID uuid.UUID, f domain.Filter, limit, offset int) ([]domain.View, int, error)
	// ListInbox returns a page of the reports in the scope (deleted ones too), newest first, and how many match.
	ListInbox(ctx context.Context, scope domain.InboxScope, f domain.Filter, limit, offset int) ([]domain.View, int, error)
	// InsertReply stores a message and returns it with its author.
	InsertReply(ctx context.Context, reportID, authorID uuid.UUID, body string, at time.Time) (domain.Reply, error)
	// ListReplies returns a page of the conversation, newest first, and how many messages there are.
	ListReplies(ctx context.Context, reportID uuid.UUID, limit, offset int) ([]domain.Reply, int, error)
}

// RoomRef is a room as the report rules know it.
type RoomRef = domain.RoomRef

// RoomReader reads the rooms a tenant rented (a read-only view of what the contract and property contexts own).
type RoomReader interface {
	// RentedBy returns the room when the tenant rents it or rented it (a running or ended contract), else shared.ErrNotFound.
	RentedBy(ctx context.Context, tenantID, roomID uuid.UUID) (RoomRef, error)
	// FirstRentedFrom returns a room of the landlord the tenant rents or rented (the demo seed uses it), else shared.ErrNotFound.
	FirstRentedFrom(ctx context.Context, tenantID, landlordID uuid.UUID) (RoomRef, error)
}

// Notification is a message for one user, or for every admin.
type Notification struct {
	UserID   uuid.UUID
	ToAdmins bool
	// Kind is one of the Notify* constants; the web translates it with Params.
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Notifier tells people about something that happened.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoNotifier is the default: nobody is told.
type NoNotifier struct{}

// Notify does nothing.
func (NoNotifier) Notify(context.Context, Notification) error { return nil }

// AuditEvent is something worth keeping in the audit trail. The actor is the one who did it.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action  string
	ActorID uuid.UUID
	// RoomID and OwnerID are the room of a room report and its landlord (uuid.Nil when none).
	RoomID       uuid.UUID
	OwnerID      uuid.UUID
	TargetUserID *uuid.UUID
	ReportID     uuid.UUID
	Changes      map[string]any
	Meta         map[string]any
}

// AuditRecorder writes the audit trail; it runs in the transaction of ctx.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// EventKind says what a live event is about.
type EventKind string

// The kinds of live event.
const (
	// EventReply: a message was added to the conversation.
	EventReply EventKind = "reply"
	// EventStatus: the recipient changed where the report stands.
	EventStatus EventKind = "status"
	// EventTyping: somebody is writing a message (never stored).
	EventTyping EventKind = "typing"
)

// ReportEvent is what the people who have a report open are told as it happens. Reply is set for EventReply; UserID is
// who is typing for EventTyping (the other party is the one who hears of it).
type ReportEvent struct {
	Kind   EventKind
	Report domain.Report
	Reply  *domain.Reply
	UserID uuid.UUID
}

// ReportEvents tells the open conversations of a report (the adapter fans it out to every API instance). It is an
// accelerator: the stored report is the record, so a failure here never fails the action.
type ReportEvents interface {
	Publish(ctx context.Context, e ReportEvent) error
}

// NoEvents is the default: nobody is told live.
type NoEvents struct{}

// Publish does nothing.
func (NoEvents) Publish(context.Context, ReportEvent) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
