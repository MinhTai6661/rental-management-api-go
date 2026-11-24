// Package audit holds the pure rules of the audit trail: which actions exist and how long each is kept, what is
// scrubbed before anything is written, what each kind of reader may see, and how the monthly partitions are named. It
// imports nothing but the standard library and uuid.
package audit

import "strings"

// Tier says how long an entry is kept and which table holds it.
type Tier string

// The tiers.
const (
	// Important is what matters for years: money, rights, contracts, roles, settings.
	Important Tier = "important"
	// Routine is the daily noise (sign-ins), kept for a few months.
	Routine Tier = "routine"
)

// ParseTier reads a tier name; the empty string is "no filter" and reports ok with an empty tier.
func ParseTier(s string) (Tier, bool) {
	switch Tier(s) {
	case "", Important, Routine:
		return Tier(s), true
	}
	return "", false
}

// Action names what happened: "<area>.<verb>".
type Action string

// The actions. The ones marked routine in routineActions are the sign-in noise; everything else is important.
const (
	AuthRegister               Action = "auth.register"
	AuthEmailConfirmed         Action = "auth.email_confirmed"
	AuthLogin                  Action = "auth.login"
	AuthLoginFailed            Action = "auth.login_failed"
	AuthLogout                 Action = "auth.logout"
	AuthTokenReuse             Action = "auth.token_reuse" //nolint:gosec // an action name, not a credential
	AuthRoleSwitched           Action = "auth.role_switched"
	AuthPasswordChanged        Action = "auth.password_changed"
	AuthOAuthLinked            Action = "auth.oauth_linked"
	AuthOAuthUnlinked          Action = "auth.oauth_unlinked"
	AuthPasswordResetRequested Action = "auth.password_reset_requested"
	AuthPasswordReset          Action = "auth.password_reset"

	RoomCreated        Action = "room.created"
	RoomUpdated        Action = "room.updated"
	RoomDeleted        Action = "room.deleted"
	RoomReordered      Action = "room.reordered"
	RoomServiceChanged Action = "room.service_changed"

	BuildingCreated      Action = "building.created"
	BuildingUpdated      Action = "building.updated"
	BuildingDeleted      Action = "building.deleted"
	BuildingReordered    Action = "building.reordered"
	BuildingRoomsCreated Action = "building.rooms_created"

	ContractRegistered            Action = "contract.registered"
	ContractCreated               Action = "contract.created"
	ContractUpdated               Action = "contract.updated"
	ContractDeleted               Action = "contract.deleted"
	ContractTerminated            Action = "contract.terminated"
	ContractCancellationRequested Action = "contract.cancellation_requested"
	ContractCancellationWithdrawn Action = "contract.cancellation_withdrawn"
	ContractCancellationApproved  Action = "contract.cancellation_approved"
	ContractCancellationDeclined  Action = "contract.cancellation_declined"

	ContractInvitationCreated     Action = "contract_invitation.created"
	ContractInvitationAccepted    Action = "contract_invitation.accepted"
	ContractInvitationDeclined    Action = "contract_invitation.declined"
	ContractInvitationRevoked     Action = "contract_invitation.revoked"
	ContractInvitationApproved    Action = "contract_invitation.approved"
	ContractInvitationRejected    Action = "contract_invitation.rejected"
	ContractInvitationRegenerated Action = "contract_invitation.regenerated"

	InvoiceCreated       Action = "invoice.created"
	InvoiceSent          Action = "invoice.sent"
	InvoiceUpdated       Action = "invoice.updated"
	InvoiceDeleted       Action = "invoice.deleted"
	InvoiceStatusChanged Action = "invoice.status_changed"
	InvoiceLinkCreated   Action = "invoice.link_created"
	InvoiceLinkRevoked   Action = "invoice.link_revoked"

	PaymentSubmitted    Action = "payment.submitted"
	PaymentConfirmed    Action = "payment.confirmed"
	PaymentRejected     Action = "payment.rejected"
	PaymentWithdrawn    Action = "payment.withdrawn"
	PaymentRecorded     Action = "payment.recorded"
	PaymentMethodSaved  Action = "payment_method.saved"
	PaymentMethodRemove Action = "payment_method.removed"

	SubscriptionRequested Action = "subscription.requested"
	SubscriptionAssigned  Action = "subscription.assigned"
	SubscriptionActivated Action = "subscription.activated"
	SubscriptionCanceled  Action = "subscription.canceled"
	SubscriptionExpired   Action = "subscription.expired"
	PlanSaved             Action = "plan.saved"

	AnnouncementCreated       Action = "announcement.created"
	AnnouncementSent          Action = "announcement.sent"
	AnnouncementUpdated       Action = "announcement.updated"
	AnnouncementImagesChanged Action = "announcement.images_changed"
	AnnouncementUnpublished   Action = "announcement.unpublished"
	AnnouncementRecalled      Action = "announcement.recalled"

	ReportCreated       Action = "report.created"
	ReportReplied       Action = "report.replied"
	ReportStatusChanged Action = "report.status_changed"
	ReportDeleted       Action = "report.deleted"

	UserDeleted                      Action = "user.deleted"
	UserErased                       Action = "user.erased"
	UserDataExported                 Action = "user.data_exported"
	UserTermsAccepted                Action = "user.terms_accepted"
	UserInvitationsPreferenceChanged Action = "user.invitations_preference_changed"
	UserProfileUpdated               Action = "user.profile_updated"
	UserAvatarChanged                Action = "user.avatar_changed"
	UserRolesChanged                 Action = "user.roles_changed"
	InvitationOptedOut               Action = "invitation.opted_out"

	LandlordApplicationSubmitted Action = "landlord_application.submitted"
	LandlordApplicationWithdrawn Action = "landlord_application.withdrawn"
	LandlordApplicationApproved  Action = "landlord_application.approved"
	LandlordApplicationRejected  Action = "landlord_application.rejected"
	LandlordRevoked              Action = "landlord.revoked"
	LandlordProfileUpdated       Action = "landlord.profile_updated"

	InvitationLookup Action = "invitation.lookup"

	ManagerInvited           Action = "manager.invited"
	ManagerInvitationRevoked Action = "manager.invitation_revoked"
	ManagerAccepted          Action = "manager.accepted"
	ManagerDeclined          Action = "manager.declined"
	ManagerRevoked           Action = "manager.revoked"
	ManagerUpdated           Action = "manager.updated"

	ListingCreated     Action = "listing.created"
	ListingUpdated     Action = "listing.updated"
	ListingSubmitted   Action = "listing.submitted"
	ListingApproved    Action = "listing.approved"
	ListingRejected    Action = "listing.rejected"
	ListingUnpublished Action = "listing.unpublished"
	ListingTakenDown   Action = "listing.taken_down"
	ListingDeleted     Action = "listing.deleted"

	LegalPublished Action = "legal.published"
	SettingChanged Action = "setting.changed"
)

// all lists every action once; Parse and Actions read it.
var all = []Action{
	AuthRegister, AuthEmailConfirmed, AuthLogin, AuthLoginFailed, AuthLogout, AuthTokenReuse, AuthRoleSwitched,
	AuthPasswordChanged, AuthOAuthLinked, AuthOAuthUnlinked, AuthPasswordResetRequested, AuthPasswordReset,
	RoomCreated, RoomUpdated, RoomDeleted, RoomReordered, RoomServiceChanged,
	BuildingCreated, BuildingUpdated, BuildingDeleted, BuildingReordered, BuildingRoomsCreated,
	ContractRegistered, ContractCreated, ContractUpdated, ContractDeleted, ContractTerminated,
	ContractCancellationRequested, ContractCancellationWithdrawn, ContractCancellationApproved, ContractCancellationDeclined,
	ContractInvitationCreated, ContractInvitationAccepted, ContractInvitationDeclined, ContractInvitationRevoked,
	ContractInvitationApproved, ContractInvitationRejected, ContractInvitationRegenerated,
	InvoiceCreated, InvoiceSent, InvoiceUpdated, InvoiceDeleted, InvoiceStatusChanged, InvoiceLinkCreated, InvoiceLinkRevoked,
	PaymentSubmitted, PaymentConfirmed, PaymentRejected, PaymentWithdrawn, PaymentRecorded, PaymentMethodSaved, PaymentMethodRemove,
	SubscriptionRequested, SubscriptionAssigned, SubscriptionActivated, SubscriptionCanceled, SubscriptionExpired, PlanSaved,
	AnnouncementCreated, AnnouncementSent, AnnouncementUpdated, AnnouncementImagesChanged, AnnouncementUnpublished, AnnouncementRecalled,
	ReportCreated, ReportReplied, ReportStatusChanged, ReportDeleted,
	UserDeleted, UserErased, UserDataExported, UserTermsAccepted, UserInvitationsPreferenceChanged, UserProfileUpdated,
	UserAvatarChanged, UserRolesChanged, InvitationOptedOut,
	LandlordApplicationSubmitted, LandlordApplicationWithdrawn, LandlordApplicationApproved, LandlordApplicationRejected,
	LandlordRevoked, LandlordProfileUpdated,
	InvitationLookup,
	ManagerInvited, ManagerInvitationRevoked, ManagerAccepted, ManagerDeclined, ManagerRevoked, ManagerUpdated,
	ListingCreated, ListingUpdated, ListingSubmitted, ListingApproved, ListingRejected, ListingUnpublished, ListingTakenDown, ListingDeleted,
	LegalPublished, SettingChanged,
}

var byName = func() map[string]Action {
	m := make(map[string]Action, len(all))
	for _, a := range all {
		m[string(a)] = a
	}
	return m
}()

// Actions lists every action.
func Actions() []Action { return append([]Action(nil), all...) }

// ParseAction reads an action name; ok is false for a name that is not an action.
func ParseAction(s string) (Action, bool) {
	a, ok := byName[s]
	return a, ok
}

// Tier is Routine for the sign-in noise and the lookups of invitees, Important for everything else.
func (a Action) Tier() Tier {
	switch a {
	case AuthLogin, AuthLoginFailed, AuthLogout, InvitationLookup:
		return Routine
	}
	return Important
}

// IsPlatformMoney reports whether the action is about the platform's own money (plans, subscriptions): those amounts
// stay visible to the staff, the amounts of landlords' rent do not.
func (a Action) IsPlatformMoney() bool {
	return strings.HasPrefix(string(a), "plan.") || strings.HasPrefix(string(a), "subscription.")
}
