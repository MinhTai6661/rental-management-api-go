package manager

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
)

// The interfaces below are what the manager use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database. Repository
// lookups return shared.ErrNotFound when there is no such row.

// TxManager runs fn in one transaction; repository calls made with the ctx given to fn join it.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// The read models the repositories return live in the domain so adapters can build them without the use cases.
type (
	RoomInfo         = domain.RoomInfo
	Group            = domain.Group
	ManagedGroup     = domain.ManagedGroup
	AssignmentDetail = domain.AssignmentDetail
)

// InvitationRepository stores the invitations to manage rooms. Get and List are scoped to the landlord.
type InvitationRepository interface {
	// Insert stores the invitation with its rooms and capabilities and returns its id.
	Insert(ctx context.Context, inv domain.Invitation) (uuid.UUID, error)
	Get(ctx context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error)
	// List returns a page (open ones first) and how many match in all; a nil status is every status.
	List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error)
	// ForUpdate locks the invitation (with its rooms and capabilities) until the transaction ends.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Invitation, error)
	// Save writes the fields that change: status, link secret, end and answer.
	Save(ctx context.Context, inv domain.Invitation) error
	FindByTokenHash(ctx context.Context, hash string) (domain.Invitation, error)
	FindForRecipient(ctx context.Context, id, recipientID uuid.UUID) (domain.Invitation, error)
	// Offer returns an invitation as the invited person sees it.
	Offer(ctx context.Context, id uuid.UUID) (domain.InvitationOffer, error)
	// Inbox returns a page of the unexpired pending invitations addressed to the account, newest first, and how many
	// there are in all.
	Inbox(ctx context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error)
	// LockOpenOfRooms locks the waiting (unexpired) invitations that hold any of the rooms, with their rooms.
	LockOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, now time.Time) ([]domain.Invitation, error)
	// RemoveRooms takes the rooms out of the invitation.
	RemoveRooms(ctx context.Context, invitationID uuid.UUID, roomIDs []uuid.UUID) error
	// RevokeOpenOfRooms withdraws the waiting invitations of rooms that are gone and returns them.
	RevokeOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error)
}

// AssignmentRepository stores the hand-overs of rooms.
type AssignmentRepository interface {
	// Insert makes one active hand-over per room for the manager with the same settings and returns them.
	Insert(ctx context.Context, landlordID, managerID uuid.UUID, roomIDs []uuid.UUID, notify bool, capabilities []domain.Capability, at time.Time) ([]domain.Assignment, error)
	// ManagedRooms returns the rooms among roomIDs that already have an active manager, named.
	ManagedRooms(ctx context.Context, roomIDs []uuid.UUID) ([]RoomInfo, error)
	// ForUpdate locks the landlord's active hand-overs among the ids (others are not returned).
	ForUpdate(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.Assignment, error)
	// Get returns the landlord's active hand-over (shared.ErrNotFound otherwise).
	Get(ctx context.Context, landlordID, id uuid.UUID) (AssignmentDetail, error)
	// Save writes the notify switch and the capabilities.
	Save(ctx context.Context, a domain.Assignment) error
	// Revoke ends the hand-overs.
	Revoke(ctx context.Context, ids []uuid.UUID, at time.Time) error
	// List returns a page of the landlord's active hand-overs, newest first, and how many there are; a nil managerID is
	// every manager.
	List(ctx context.Context, landlordID uuid.UUID, managerID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error)
	Groups(ctx context.Context, landlordID uuid.UUID, limit, offset int) ([]Group, int, error)
	// BusyRoomIDs lists every room of the landlord that has a manager.
	BusyRoomIDs(ctx context.Context, landlordID uuid.UUID) ([]uuid.UUID, error)
	// ManagedList returns a page of the rooms handed to the manager (a nil landlordID is every landlord).
	ManagedList(ctx context.Context, managerID uuid.UUID, landlordID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error)
	ManagedGroups(ctx context.Context, managerID uuid.UUID, limit, offset int) ([]ManagedGroup, int, error)
	// Of returns the manager's active hand-over of the room, or shared.ErrNotFound.
	Of(ctx context.Context, managerID, roomID uuid.UUID) (domain.Assignment, error)
	// CountActive counts the manager's active hand-overs.
	CountActive(ctx context.Context, managerID uuid.UUID) (int, error)
	// ActiveOf lists the manager's active hand-overs.
	ActiveOf(ctx context.Context, managerID uuid.UUID) ([]domain.Assignment, error)
}

// RoomReader reads the rooms of a landlord (a read-only view of what the property context owns).
type RoomReader interface {
	// Lock returns the landlord's live rooms among the ids, locked until the transaction ends (a room that is not the
	// landlord's or is gone is not returned).
	Lock(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]RoomInfo, error)
	// Names returns the rooms with these ids, deleted ones too.
	Names(ctx context.Context, ids []uuid.UUID) ([]RoomInfo, error)
}

// Roles gives and takes the manager role of an account.
type Roles interface {
	// Grant adds the role (nothing happens when it is held).
	Grant(ctx context.Context, userID uuid.UUID) error
	// Revoke removes the role, and clears the role last used when it was this one.
	Revoke(ctx context.Context, userID uuid.UUID) error
}

// Invitees finds who an invitation is for and whether they may be mailed (the invitation context, through the
// composition root).
type Invitees interface {
	Resolve(ctx context.Context, landlordID uuid.UUID, recipientID *uuid.UUID, email *string) (invitation.Addressee, error)
	// Person returns the account with the id (the one answering).
	Person(ctx context.Context, id uuid.UUID) (invitation.Person, error)
	MayMail(ctx context.Context, email string) (bool, error)
}

// LinkTokens makes the secrets of invitation links.
type LinkTokens interface {
	New() (token, hash string, err error)
	Hash(token string) string
}

// Sealer encrypts the secret of a link so it can be shown again while the invitation waits.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// InvitationMail is the email that carries an invitation.
type InvitationMail struct {
	To       string
	Landlord string
	// Rooms are the names of the rooms offered.
	Rooms []string
	// URL is the link the person opens. An invitation sent to an account carries no secret in it.
	URL       string
	ExpiresAt time.Time
}

// InvitationMailer emails an invitation.
type InvitationMailer interface {
	SendInvitation(ctx context.Context, m InvitationMail) error
}

// NoInvitationMailer is the default: nothing is emailed.
type NoInvitationMailer struct{}

// SendInvitation does nothing.
func (NoInvitationMailer) SendInvitation(context.Context, InvitationMail) error { return nil }

// Notification is a message for one user.
type Notification struct {
	UserID uuid.UUID
	// Kind is one of the Notify* constants; the web translates it with Params.
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Notifier tells a user about something that happened.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoNotifier is the default: nobody is told.
type NoNotifier struct{}

// Notify does nothing.
func (NoNotifier) Notify(context.Context, Notification) error { return nil }

// AuditEvent is something worth keeping in the audit trail. The actor is the one who did it: the landlord for a
// hand-over, the person asked for an answer.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action  string
	ActorID uuid.UUID
	RoomID  uuid.UUID
	// OwnerID is the landlord whose data it is.
	OwnerID      uuid.UUID
	TargetUserID *uuid.UUID
	// InvitationID is the invitation the event is about (uuid.Nil when none).
	InvitationID uuid.UUID
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

// OfferWithdrawer withdraws the offers of rooms a manager wrote that still wait for the landlord (the contract
// context, through the composition root). It runs in the caller's transaction.
type OfferWithdrawer interface {
	WithdrawAwaiting(ctx context.Context, managerID uuid.UUID) error
}

// NoOffers is the default: there are no offers to withdraw.
type NoOffers struct{}

// WithdrawAwaiting does nothing.
func (NoOffers) WithdrawAwaiting(context.Context, uuid.UUID) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
