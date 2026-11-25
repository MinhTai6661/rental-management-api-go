package contract

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
)

// The interfaces below are what the contract use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.
// Repository lookups return shared.ErrNotFound when there is no such row; writes refused by a constraint come back
// as *shared.ConstraintError.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it, and so do the
// RoomStatusSetter and the other ports implemented over the same database.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ContractRepository stores contracts. The lists and Get are scoped to a viewer: a landlord sees the contracts on
// their own rooms, a tenant their own.
type ContractRepository interface {
	// Search returns a page of the contracts the viewer sees and how many match in all.
	Search(ctx context.Context, v domain.Viewer, q domain.Query) ([]domain.Detail, int, error)
	// Get returns one contract as a list shows it (shared.ErrNotFound when the viewer does not see it).
	Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error)
	// ForUpdate locks a live contract until the transaction ends. Lock its room first, to keep one lock order.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Contract, error)
	// Insert stores a contract and returns its id.
	Insert(ctx context.Context, c domain.Contract) (uuid.UUID, error)
	// Save writes the fields of a contract that can change.
	Save(ctx context.Context, c domain.Contract) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
	// Occupancy counts the running contracts of a room.
	Occupancy(ctx context.Context, roomID uuid.UUID) (domain.Occupancy, error)
	// RegistrationOpen says whether the tenant has a waiting or running contract on the room.
	RegistrationOpen(ctx context.Context, roomID, tenantID uuid.UUID) (bool, error)
	// RejectOtherPending rejects the waiting registrations of the room but one and returns their ids.
	RejectOtherPending(ctx context.Context, roomID, exceptID uuid.UUID) ([]uuid.UUID, error)
	// CloseBlank ends the running contracts of the rooms that no tenant holds (today, or their own earlier end
	// date) and returns them.
	CloseBlank(ctx context.Context, roomIDs []uuid.UUID, today time.Time) ([]domain.ClosedBlank, error)
	// TopOrder is the position a new contract of the landlord takes: one before the smallest in use.
	TopOrder(ctx context.Context, landlordID uuid.UUID) (int, error)
	// Order lists the contracts the landlord sorts, in their stored order, locked.
	Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error)
	SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error
	// CountRunning counts the landlord's running contracts that a tenant holds.
	CountRunning(ctx context.Context, landlordID uuid.UUID) (int, error)
	// CountPending counts the tenant's waiting registrations.
	CountPending(ctx context.Context, tenantID uuid.UUID) (int, error)
	// RunningTenantRooms returns the rooms among roomIDs on which a tenant holds a running contract.
	RunningTenantRooms(ctx context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error)
	// LockLandlord serialises the transactions that count a landlord's running contracts against a plan limit.
	LockLandlord(ctx context.Context, landlordID uuid.UUID) error
	// StatusCounts counts the tenant's contracts per status.
	StatusCounts(ctx context.Context, tenantID uuid.UUID) (domain.StatusCounts, error)
	// PaymentCounts counts the tenant's contracts in the statuses by their state of money (due, waiting for review,
	// settled), read from the bills and payments.
	PaymentCounts(ctx context.Context, tenantID uuid.UUID, statuses []domain.Status) (domain.PaymentCounts, error)
	// RentalExtras returns the costs and the pictures of a rented room for the renter's detail.
	RentalExtras(ctx context.Context, roomID uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error)
	// CountOfTenant counts the contracts of the tenant on the landlord's rooms.
	CountOfTenant(ctx context.Context, landlordID, tenantID uuid.UUID) (int, error)
}

// RoomReader tells the facts about rooms that contracts need (it is a read-only view of what the property context
// owns).
type RoomReader interface {
	// Lock returns the room, a deleted one included (Live tells), and locks it until the transaction ends
	// (shared.ErrNotFound when there is no such room).
	Lock(ctx context.Context, id uuid.UUID) (domain.RoomFacts, error)
}

// RoomStatusSetter changes the status of a room (rented when a contract runs on it, available when the last one is
// gone). The composition root implements it over the property context, so its rules and observers apply.
type RoomStatusSetter interface {
	SetStatus(ctx context.Context, landlordID, roomID uuid.UUID, status string) error
}

// Ports to what later phases own. Each has a default that changes nothing, so the context works on its own and a
// later one is wired in cmd/rental-management without touching this one.

// Notification is a message for one user about a contract.
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

// AuditEvent is something worth keeping in the audit trail.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action string
	// ActorID is who did it (uuid.Nil for the system).
	ActorID    uuid.UUID
	ContractID uuid.UUID
	// InvitationID is the invitation the event is about (uuid.Nil when it is about a contract).
	InvitationID uuid.UUID
	RoomID       uuid.UUID
	// OwnerID is the landlord whose data it is.
	OwnerID      uuid.UUID
	TargetUserID *uuid.UUID
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

// PlanLimits says how much a plan allows; nil means unlimited. The plan context implements it.
type PlanLimits interface {
	// MaxActiveContracts is the number of running contracts (with a tenant) a landlord may have.
	MaxActiveContracts(ctx context.Context, landlordID uuid.UUID) (*int, error)
	// MaxPendingRegistrations is the number of waiting registrations a tenant may have.
	MaxPendingRegistrations(ctx context.Context, tenantID uuid.UUID) (*int, error)
}

// UnlimitedPlans is the default: no plan limits anything.
type UnlimitedPlans struct{}

// MaxActiveContracts is unlimited.
func (UnlimitedPlans) MaxActiveContracts(context.Context, uuid.UUID) (*int, error) { return nil, nil }

// MaxPendingRegistrations is unlimited.
func (UnlimitedPlans) MaxPendingRegistrations(context.Context, uuid.UUID) (*int, error) {
	return nil, nil
}

// KeepRoomStatus is the default RoomStatusSetter: it changes nothing (the contracts work without the property
// context behind them, as in tests).
type KeepRoomStatus struct{}

// SetStatus does nothing.
func (KeepRoomStatus) SetStatus(context.Context, uuid.UUID, uuid.UUID, string) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }

// InvitationRepository stores the offers of rooms by link. Get and List are scoped to the landlord whose rooms they
// are (shared.ErrNotFound for anybody else's).
type InvitationRepository interface {
	Insert(ctx context.Context, inv domain.Invitation) (uuid.UUID, error)
	Get(ctx context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error)
	// List returns a page, open offers first, and how many match in all; a nil status is every status.
	List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error)
	// ForUpdate locks the invitation until the transaction ends. Lock its room first, to keep one lock order.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Invitation, error)
	// Save writes the fields that change: status, link secret (hash and sealed), end, answer and contract.
	Save(ctx context.Context, inv domain.Invitation) error
	// FindByTokenHash returns the invitation a link opens, whatever its status (shared.ErrNotFound when none).
	FindByTokenHash(ctx context.Context, hash string) (domain.Invitation, error)
	// FindForRecipient returns the invitation with that id when it is addressed to the account (shared.ErrNotFound
	// otherwise).
	FindForRecipient(ctx context.Context, id, recipientID uuid.UUID) (domain.Invitation, error)
	// Offer returns an invitation as the invited person sees it.
	Offer(ctx context.Context, id uuid.UUID) (domain.InvitationOffer, error)
	// Inbox returns a page of the unexpired pending invitations addressed to the account, newest first, and how many
	// there are in all.
	Inbox(ctx context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error)
	// GetAuthored returns an offer the manager wrote while they still manage its room (shared.ErrNotFound otherwise).
	GetAuthored(ctx context.Context, authorID, id uuid.UUID) (domain.InvitationDetail, error)
	// ListAuthored is List for the offers a manager wrote.
	ListAuthored(ctx context.Context, authorID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error)
	// RevokeAwaitingOfAuthor withdraws the offers the manager wrote that still wait for the landlord and returns them.
	RevokeAwaitingOfAuthor(ctx context.Context, authorID uuid.UUID, at time.Time) ([]domain.Invitation, error)
	// RevokeOpenOfRooms withdraws the waiting invitations of the rooms (wiping their secrets) and returns them.
	RevokeOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error)
}

// Invitees finds who an invitation is for and whether they may be mailed (the invitation context, through the
// composition root).
type Invitees interface {
	// Resolve works out the addressee from the account id and the address the landlord gave; the recipient errors of
	// the invitation domain say what does not fit.
	Resolve(ctx context.Context, landlordID uuid.UUID, recipientID *uuid.UUID, email *string) (invitation.Addressee, error)
	// Person returns the account with the id (the one answering).
	Person(ctx context.Context, id uuid.UUID) (invitation.Person, error)
	// MayMail says whether an invitation may be emailed to the address.
	MayMail(ctx context.Context, email string) (bool, error)
}

// LinkTokens makes the secrets of invitation links: the token goes into the link, the hash is what the database
// looks up.
type LinkTokens interface {
	New() (token, hash string, err error)
	Hash(token string) string
}

// Sealer encrypts the secret of a link so it can be shown again while the offer waits.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// InvitationMail is the email that carries an invitation.
type InvitationMail struct {
	To       string
	Landlord string
	Room     string
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

// Grant is what a manager was given on a room, as far as offers of the room go.
type Grant struct {
	// Propose: the manager may write an offer of the room.
	Propose bool
	// Create: the offer goes out without the landlord's approval.
	Create bool
}

// Managers tells what an account was given on a room (the manager context, through the composition root). The caller
// has already decided the account acts as a manager; a room nobody handed over has the zero Grant.
type Managers interface {
	Grant(ctx context.Context, userID, roomID uuid.UUID) (Grant, error)
}

// NoManagers is the default: nobody manages anything.
type NoManagers struct{}

// Grant is the zero Grant.
func (NoManagers) Grant(context.Context, uuid.UUID, uuid.UUID) (Grant, error) { return Grant{}, nil }
