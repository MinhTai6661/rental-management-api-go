// Package admin holds the use cases of the platform's staff work: managing accounts and their roles, answering
// landlord applications, the system settings, the legal documents and the data subject's rights. It declares the ports
// it needs (ports.go) and knows no framework, driver or other context.
package admin

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// The kinds of notification the admin use cases send; the web translates each with the notification's params.
const (
	NotifyApplicationReceived = "landlord_application_received"
	NotifyApplicationApproved = "landlord_application_approved"
	NotifyApplicationRejected = "landlord_application_rejected"
	NotifyLandlordRevoked     = "landlord_revoked"
)

// The audit actions the admin use cases record.
const (
	AuditUserDeleted          = "user.deleted"
	AuditRolesChanged         = "user.roles_changed"
	AuditApplicationSubmitted = "landlord_application.submitted"
	AuditApplicationWithdrawn = "landlord_application.withdrawn"
	AuditApplicationApproved  = "landlord_application.approved"
	AuditApplicationRejected  = "landlord_application.rejected"
	AuditLandlordRevoked      = "landlord.revoked"
	AuditLandlordProfileSaved = "landlord.profile_updated"
	AuditBuildingCreated      = "building.created"
	AuditSettingChanged       = "setting.changed"
	AuditLegalPublished       = "legal.published"
	AuditTermsAccepted        = "user.terms_accepted"
	AuditPersonalDataExported = "user.data_exported"
	AuditUserErased           = "user.erased"
)

// Session close reasons stored with a closed session.
const (
	reasonAccountDeleted = "account_deleted"
	reasonAccountErased  = "account_erased"
)

// Actor is who does it: the signed in account and the role it acts in.
type Actor struct {
	ID   uuid.UUID
	Role string
}

// roles are the roles the actor holds for the rules of who may manage whom: a staff account holds one staff role.
func (a Actor) roles() []string { return []string{a.Role} }

// Deps are the collaborators of the admin use cases. The ports with a default (Notifier, Auditor, Revocations) may be
// left nil.
type Deps struct {
	Tx           TxManager
	Users        UserRepository
	Applications ApplicationRepository
	Profiles     ProfileRepository
	Settings     SettingRepository
	Legal        LegalRepository
	Personal     PersonalDataRepository
	Buildings    FirstBuildings
	Hasher       PasswordVerifier
	Revocations  RevocationStore
	Notifier     Notifier
	Audit        Auditor
	// History reads the changes of the settings from the audit trail (nil: no history).
	History SettingHistory
	Clock   Clock
	Log     *slog.Logger
}

// Options are the settings of the admin use cases.
type Options struct {
	// RevokedFor is how long a closed session stays blocked: the life of an access token plus its clock leeway.
	RevokedFor time.Duration
	// TermsVersion is the version people accept while no legal document has been published.
	TermsVersion string
	// Defaults overrides the default of a setting (key to value); a key that is not here keeps the domain's default.
	Defaults map[string]float64
}

// core is what every use case of the package shares.
type core struct {
	tx           TxManager
	users        UserRepository
	applications ApplicationRepository
	profiles     ProfileRepository
	settings     SettingRepository
	legal        LegalRepository
	personal     PersonalDataRepository
	buildings    FirstBuildings
	hasher       PasswordVerifier
	revocations  RevocationStore
	notifier     Notifier
	audit        Auditor
	history      SettingHistory
	clock        Clock
	log          *slog.Logger
	opts         Options
}

func newCore(d Deps, o Options) *core {
	c := &core{
		tx: d.Tx, users: d.Users, applications: d.Applications, profiles: d.Profiles, settings: d.Settings, legal: d.Legal,
		personal: d.Personal, buildings: d.Buildings, hasher: d.Hasher, revocations: d.Revocations, notifier: d.Notifier,
		audit: d.Audit, history: d.History, clock: d.Clock, log: d.Log, opts: o,
	}
	if c.notifier == nil {
		c.notifier = NoNotifier{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
	}
	return c
}
