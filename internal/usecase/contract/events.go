package contract

// The kinds of notification the contract use cases send; the web translates each with the notification's params
// (room, tenant, landlord, contract_id, and reason for an ended contract). They are the kinds of the Laravel API.
const (
	NotifyRegistrationConfirmed = "registration_confirmed"
	NotifyRegistrationApproved  = "registration_approved"
	NotifyRegistrationRejected  = "registration_rejected"
	NotifyCancellationRequested = "cancellation_requested"
	NotifyCancellationDeclined  = "cancellation_declined"
	NotifyContractEnded         = "contract_ended"
	// NotifyInvitationReceived tells an account that a landlord offered it a room.
	NotifyInvitationReceived = "contract_invitation_received"
	// NotifyInvitationAccepted and NotifyInvitationDeclined tell the landlord how the person answered.
	NotifyInvitationAccepted = "contract_invitation_accepted"
	NotifyInvitationDeclined = "contract_invitation_declined"
	// NotifyInvitationNeedsApproval tells the landlord that a manager wrote an offer waiting for their answer;
	// NotifyInvitationApproved and NotifyInvitationRejected tell the manager the answer.
	NotifyInvitationNeedsApproval = "contract_invitation_needs_approval"
	NotifyInvitationApproved      = "contract_invitation_approved"
	NotifyInvitationRejected      = "contract_invitation_rejected"
)

// The audit actions the contract use cases record.
const (
	AuditRegistered            = "contract.registered"
	AuditCreated               = "contract.created"
	AuditUpdated               = "contract.updated"
	AuditDeleted               = "contract.deleted"
	AuditTerminated            = "contract.terminated"
	AuditCancellationRequested = "contract.cancellation_requested"
	AuditCancellationWithdrawn = "contract.cancellation_withdrawn"
	AuditCancellationApproved  = "contract.cancellation_approved"
	AuditCancellationDeclined  = "contract.cancellation_declined"

	AuditInvitationCreated     = "contract_invitation.created"
	AuditInvitationRegenerated = "contract_invitation.regenerated"
	AuditInvitationRevoked     = "contract_invitation.revoked"
	AuditInvitationAccepted    = "contract_invitation.accepted"
	AuditInvitationDeclined    = "contract_invitation.declined"
	AuditInvitationApproved    = "contract_invitation.approved"
	AuditInvitationRejected    = "contract_invitation.rejected"
)

// The audiences of a notification: which side of the contract it is for.
const (
	AudienceLandlord = "landlord"
	AudienceTenant   = "tenant"
)
