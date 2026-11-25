package invoice

// The kinds of notification the invoice use cases send; the web translates each with the notification's params
// (invoice_number, room, amount_vnd, due_date, invoice_id, contract_id; the overdue notice to the landlord adds
// tenant). They are the kinds of the Laravel API.
const (
	NotifyInvoiceSent          = "invoice_sent"
	NotifyInvoiceUpdated       = "invoice_updated"
	NotifyInvoiceDueSoon       = "invoice_due_soon"
	NotifyInvoiceOverdue       = "invoice_overdue"
	NotifyTenantInvoiceOverdue = "tenant_invoice_overdue"
	// NotifyManagerBillSent and NotifyManagerBillEdited tell the landlord a manager sent or corrected a bill (params add
	// manager).
	NotifyManagerBillSent   = "manager_bill_sent"
	NotifyManagerBillEdited = "manager_bill_edited"
)

// The audit actions the invoice use cases record.
const (
	AuditCreated       = "invoice.created"
	AuditSent          = "invoice.sent"
	AuditUpdated       = "invoice.updated"
	AuditDeleted       = "invoice.deleted"
	AuditStatusChanged = "invoice.status_changed"
	AuditLinkCreated   = "invoice.link_created"
	AuditLinkRevoked   = "invoice.link_revoked"
)

// The audiences of a notification: which side of the contract it is for.
const (
	AudienceLandlord = "landlord"
	AudienceTenant   = "tenant"
)

// RentDescription is the words of the rent line of a bill (the line's kind, rent, lets a client translate it).
const RentDescription = "Tiền thuê phòng"
