package admin

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
)

// The interfaces below are what the admin use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes. Repository lookups return
// shared.ErrNotFound when there is no such row.

// TxManager runs fn in one transaction; repository calls made with the ctx given to fn join it.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// UserRepository reads and changes the accounts the admin manages (the identity context owns the table).
type UserRepository interface {
	// List returns a page of accounts and the number of accounts that match.
	List(ctx context.Context, f domain.UserFilter, limit, offset int) ([]domain.Account, int, error)
	// Lock returns the account with its roles and locks it until the transaction ends.
	Lock(ctx context.Context, id uuid.UUID) (domain.Account, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Account, error)
	SoftDelete(ctx context.Context, id uuid.UUID, now time.Time) error
	// ReplaceRoles makes the account hold exactly these roles.
	ReplaceRoles(ctx context.Context, id uuid.UUID, roles []string, now time.Time) error
	AddRole(ctx context.Context, id uuid.UUID, role string, now time.Time) error
	// SetLastActiveRole stores the role the account acted in last (nil clears it).
	SetLastActiveRole(ctx context.Context, id uuid.UUID, role *string, now time.Time) error
	// RevokeSessions closes every open session of the account and returns their ids.
	RevokeSessions(ctx context.Context, id uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error)
	PasswordHash(ctx context.Context, id uuid.UUID) (*string, error)
}

// ApplicationRepository stores landlord applications.
type ApplicationRepository interface {
	Insert(ctx context.Context, a domain.Application) error
	Get(ctx context.Context, id uuid.UUID) (domain.Application, error)
	// Lock returns the application and locks it until the transaction ends.
	Lock(ctx context.Context, id uuid.UUID) (domain.Application, error)
	LatestOfUser(ctx context.Context, userID uuid.UUID) (domain.Application, error)
	HasOpen(ctx context.Context, userID uuid.UUID) (bool, error)
	// DeleteOpen removes the application of the user that waits for a review and reports whether there was one.
	DeleteOpen(ctx context.Context, userID uuid.UUID) (bool, error)
	List(ctx context.Context, q domain.ApplicationQuery, limit, offset int) ([]domain.Application, int, error)
	CountsByStatus(ctx context.Context) (map[domain.ApplicationStatus]int, error)
	// History returns the other applications of the same person, newest first.
	History(ctx context.Context, userID, except uuid.UUID) ([]domain.Application, error)
	Review(ctx context.Context, id uuid.UUID, status domain.ApplicationStatus, reviewer uuid.UUID, note string, now time.Time) error
}

// ProfileRepository stores the landlords' business profiles and measures what a landlord has.
type ProfileRepository interface {
	Get(ctx context.Context, userID uuid.UUID) (domain.Profile, error)
	Upsert(ctx context.Context, userID uuid.UUID, in domain.ProfileInput, now time.Time) error
	// InsertIfAbsent creates the profile unless the landlord has one.
	InsertIfAbsent(ctx context.Context, userID uuid.UUID, in domain.ProfileInput, now time.Time) error
	Impact(ctx context.Context, landlordID uuid.UUID) (domain.Impact, error)
}

// SettingRepository stores the overrides of the system settings.
type SettingRepository interface {
	Overrides(ctx context.Context) (map[string]float64, error)
	Set(ctx context.Context, key string, value float64, by uuid.UUID, now time.Time) error
	Reset(ctx context.Context, key string) error
}

// LegalRepository stores the published legal documents (never changed or deleted) and the terms an account accepted.
type LegalRepository interface {
	Insert(ctx context.Context, d domain.Document, now time.Time) error
	Latest(ctx context.Context, kind domain.LegalKind) (domain.Document, error)
	// LatestVersion is the version of the newest document of any kind (shared.ErrNotFound when none was published).
	LatestVersion(ctx context.Context) (string, error)
	Get(ctx context.Context, kind domain.LegalKind, id uuid.UUID) (domain.Document, error)
	List(ctx context.Context, kind domain.LegalKind, limit, offset int) ([]domain.Document, int, error)
	// After returns the documents published after the time, newest first.
	After(ctx context.Context, since time.Time, limit int) ([]domain.Document, error)
	NewestPerKind(ctx context.Context) ([]domain.Document, error)
	AcceptedAt(ctx context.Context, userID uuid.UUID) (*time.Time, error)
	SetAccepted(ctx context.Context, userID uuid.UUID, version string, now time.Time) error
}

// PersonalDataRepository reads and erases the data of one person.
type PersonalDataRepository interface {
	Facts(ctx context.Context, userID uuid.UUID) (domain.ErasureFacts, error)
	Export(ctx context.Context, userID uuid.UUID) (domain.Export, error)
	// Erase deletes or blanks everything personal in one go (the caller revokes the sessions and checks the facts).
	Erase(ctx context.Context, userID uuid.UUID, now time.Time) error
}

// FirstBuildings gives a new landlord their first building (the property context owns buildings).
type FirstBuildings interface {
	EnsureFirstBuilding(ctx context.Context, landlordID uuid.UUID, first FirstBuilding) error
}

// FirstBuilding is the building a new landlord starts with.
type FirstBuilding struct {
	Name          string
	WardCode      *int32
	AddressDetail string
}

// RevocationStore remembers which sessions were closed before their access tokens expire.
type RevocationStore interface {
	RevokeSessions(ctx context.Context, sids []string, ttl time.Duration) error
}

// PasswordVerifier checks a password against a stored hash (an error only for an unusable hash).
type PasswordVerifier interface {
	Verify(password, encoded string) (bool, error)
}

// Notification is a message about the admin's work: for one user, or for every admin.
type Notification struct {
	// UserID is the recipient; uuid.Nil when ToAdmins.
	UserID uuid.UUID
	// ToAdmins: every admin (not the super admins) gets it.
	ToAdmins bool
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

// Auditor writes the audit trail; Record runs in the transaction of ctx. TODO(audit): the audit context does not exist
// yet; until it does NoAudit is wired and nothing is recorded.
type Auditor interface {
	Record(ctx context.Context, e AuditEvent) error
	// ScrubPersonalData blanks the address, device and session recorded for the person in the trail (the one exception
	// to "the trail is only ever added to"; used when an account is erased).
	ScrubPersonalData(ctx context.Context, userID uuid.UUID) error
}

// SettingChange is one change of a system setting as the audit trail remembers it.
type SettingChange struct {
	At        time.Time
	ActorID   *uuid.UUID
	ActorName string
	// Meta is what the change recorded: {key, from, to}.
	Meta map[string]any
}

// SettingHistory reads the changes of the settings, newest first (the audit context keeps them).
type SettingHistory interface {
	Changes(ctx context.Context, page, perPage int) (changes []SettingChange, hasMore bool, err error)
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// ScrubPersonalData does nothing.
func (NoAudit) ScrubPersonalData(context.Context, uuid.UUID) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
