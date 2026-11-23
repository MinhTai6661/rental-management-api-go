package plan

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
)

// The interfaces below are what the plan use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.
// Repository lookups return shared.ErrNotFound when there is no such row; writes refused by a constraint come back
// as *shared.ConstraintError.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// PlanRepository stores the catalog.
type PlanRepository interface {
	// Search returns a page of plans (by audience, display order and price) and how many match in all.
	Search(ctx context.Context, q domain.PlanQuery) ([]domain.Plan, int, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Plan, error)
	GetByCode(ctx context.Context, code string) (domain.Plan, error)
	// Default returns the active default plan of the audience (shared.ErrNotFound when there is none).
	Default(ctx context.Context, audience domain.Audience) (domain.Plan, error)
	Insert(ctx context.Context, p domain.Plan) (uuid.UUID, error)
	// Update writes every field a super admin can change.
	Update(ctx context.Context, p domain.Plan) error
	// ClearDefault takes the default flag off the plans of the audience but exceptID (uuid.Nil: none is spared).
	ClearDefault(ctx context.Context, audience domain.Audience, exceptID uuid.UUID) error
}

// SubscriptionRepository stores subscriptions.
type SubscriptionRepository interface {
	Insert(ctx context.Context, s domain.Subscription) (uuid.UUID, error)
	// Get returns a subscription with its plan and owner.
	Get(ctx context.Context, id uuid.UUID) (domain.Detail, error)
	// Lock returns a subscription and locks it until the transaction ends.
	Lock(ctx context.Context, id uuid.UUID) (domain.Subscription, error)
	// Running returns the subscription that counts at now (active, started, not lapsed), with its plan.
	Running(ctx context.Context, userID uuid.UUID, audience domain.Audience, now time.Time) (domain.Detail, error)
	// RunningPriorities returns the search priority of the plan each user is running at now on the audience's side
	// (users with no running subscription are left out).
	RunningPriorities(ctx context.Context, userIDs []uuid.UUID, audience domain.Audience, now time.Time) (map[uuid.UUID]int, error)
	// Pending returns the user's request waiting for an admin, with its plan.
	Pending(ctx context.Context, userID uuid.UUID, audience domain.Audience) (domain.Detail, error)
	// CancelPending cancels the pending requests of the user for the audience.
	CancelPending(ctx context.Context, userID uuid.UUID, audience domain.Audience, now time.Time) error
	// EndOpen cancels the active and pending subscriptions of the user (of one audience, or of every one when nil)
	// but exceptID (uuid.Nil: none is spared) and returns how many it ended.
	EndOpen(ctx context.Context, userID uuid.UUID, audience *domain.Audience, exceptID uuid.UUID, now time.Time) (int, error)
	// Activate turns a pending subscription into the running one.
	Activate(ctx context.Context, id uuid.UUID, startsAt time.Time, endsAt *time.Time, grantedBy uuid.UUID) error
	Search(ctx context.Context, q domain.SubscriptionQuery) ([]domain.Detail, int, error)
	// ExpireDue marks up to limit active subscriptions past their end as expired and returns their ids.
	ExpireDue(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
}

// Accounts is a read-only view of the users, which the identity context owns.
type Accounts interface {
	// Lock returns the account and locks it until the transaction ends, so two changes of one account's plans run
	// one at a time (shared.ErrNotFound when there is no such account).
	Lock(ctx context.Context, id uuid.UUID) (domain.Account, error)
}

// UsageReader counts what an account has used of its plan's limits (a read-only view of what the property and
// contract contexts own).
type UsageReader interface {
	Landlord(ctx context.Context, landlordID uuid.UUID) (domain.LandlordUsage, error)
	PendingRegistrations(ctx context.Context, tenantID uuid.UUID) (int, error)
}

// Ports to what later phases own. Each has a default that changes nothing, so the context works on its own and a
// later one is wired in cmd/rental-management without touching this one.

// Notification is a message about a subscription: for one user, or for every admin.
type Notification struct {
	// UserID is the recipient; uuid.Nil when ToAdmins.
	UserID uuid.UUID
	// ToAdmins: every admin (not the super admins) gets it.
	ToAdmins bool
	// Kind is one of the Notify* constants; the web translates it with Params.
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Notifier tells users about something that happened.
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
	ActorID      uuid.UUID
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

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
