package manager

// The kinds of notification the manager use cases send; the web translates each with the notification's params.
const (
	// NotifyInvitationReceived tells an account that a landlord asked it to manage rooms.
	NotifyInvitationReceived = "manager_invitation_received"
	// NotifyInvitationWithdrawn tells the account an invitation was addressed to that rooms left it.
	NotifyInvitationWithdrawn = "manager_invitation_withdrawn"
	// NotifyAccepted and NotifyDeclined tell the landlord how the person answered (one message for all the rooms).
	NotifyAccepted = "manager_accepted"
	NotifyDeclined = "manager_declined"
	// NotifyRevoked tells a manager that rooms were taken from them (one message for all the rooms).
	NotifyRevoked = "manager_revoked"
)

// The audit actions the manager use cases record.
const (
	AuditInvited           = "manager.invited"
	AuditInvitationRevoked = "manager.invitation_revoked"
	AuditAccepted          = "manager.accepted"
	AuditDeclined          = "manager.declined"
	AuditRevoked           = "manager.revoked"
	AuditUpdated           = "manager.updated"
)

// The audiences of a notification: which side of the account it is for ("" shows in every role).
const (
	AudienceLandlord = "landlord"
	AudienceTenant   = "tenant"
)
