package listing

// The kinds of notification the listing use cases send; the web translates each with the notification's params
// (landlord, title and room for a submission; title, room and reason for a decision). They are the kinds of the
// Laravel API.
const (
	NotifySubmitted = "listing_submitted"
	NotifyApproved  = "listing_approved"
	NotifyRejected  = "listing_rejected"
	NotifyTakenDown = "listing_taken_down"
)

// The audit actions the listing use cases record.
const (
	AuditCreated     = "listing.created"
	AuditUpdated     = "listing.updated"
	AuditSubmitted   = "listing.submitted"
	AuditApproved    = "listing.approved"
	AuditRejected    = "listing.rejected"
	AuditUnpublished = "listing.unpublished"
	AuditTakenDown   = "listing.taken_down"
	AuditDeleted     = "listing.deleted"
)

// The audiences of a notification: which side it is for (the admins' one has none).
const AudienceLandlord = "landlord"
