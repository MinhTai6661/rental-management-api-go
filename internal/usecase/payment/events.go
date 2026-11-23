package payment

// The kinds of notification the payment use cases send; the web translates each with the notification's params (room,
// tenant, invoice_number, amount_vnd, invoice_id, contract_id; the one to the landlord adds remaining_vnd). They are
// the kinds of the Laravel API.
const (
	NotifyPaymentSubmitted = "payment_submitted"
	NotifyPaymentConfirmed = "payment_confirmed"
	NotifyPaymentRejected  = "payment_rejected"
)

// The audit actions the payment use cases record.
const (
	AuditSubmitted     = "payment.submitted"
	AuditConfirmed     = "payment.confirmed"
	AuditRejected      = "payment.rejected"
	AuditWithdrawn     = "payment.withdrawn"
	AuditRecorded      = "payment.recorded"
	AuditMethodSaved   = "payment_method.saved"
	AuditMethodRemoved = "payment_method.removed"
)

// The audiences of a notification: which side of the contract it is for.
const (
	AudienceLandlord = "landlord"
	AudienceTenant   = "tenant"
)
