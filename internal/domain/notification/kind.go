// Package notification holds the rules of the in-app inbox: what kinds of message exist, which side of an account a
// message is for and who sees it. It is plain Go: no database, no HTTP, no framework.
package notification

import (
	"errors"
	"slices"
)

// Kind says what a notification is about. The API stores the kind and its params only; the web translates them, so the
// message reads in the language the person picks when they look at it. The values are the kinds of the Laravel API.
type Kind string

// The kinds. A new feature adds its kinds here, once, so every context sends from one catalog.
const (
	InvoiceSent                     Kind = "invoice_sent"
	InvoiceUpdated                  Kind = "invoice_updated"
	InvoiceDueSoon                  Kind = "invoice_due_soon"
	InvoiceOverdue                  Kind = "invoice_overdue"
	PaymentConfirmed                Kind = "payment_confirmed"
	PaymentRejected                 Kind = "payment_rejected"
	ContractCreated                 Kind = "contract_created"
	ContractInvitationReceived      Kind = "contract_invitation_received"
	ManagerInvitationReceived       Kind = "manager_invitation_received"
	ContractInvitationNeedsApproval Kind = "contract_invitation_needs_approval"
	ContractInvitationApproved      Kind = "contract_invitation_approved"
	ContractInvitationRejected      Kind = "contract_invitation_rejected"
	ManagerInvitationWithdrawn      Kind = "manager_invitation_withdrawn"
	RegistrationApproved            Kind = "registration_approved"
	RegistrationRejected            Kind = "registration_rejected"
	CancellationDeclined            Kind = "cancellation_declined"
	PaymentSubmitted                Kind = "payment_submitted"
	ReportReceived                  Kind = "report_received"
	ReportReplied                   Kind = "report_replied"
	TenantInvoiceOverdue            Kind = "tenant_invoice_overdue"
	RegistrationConfirmed           Kind = "registration_confirmed"
	CancellationRequested           Kind = "cancellation_requested"
	ContractInvitationAccepted      Kind = "contract_invitation_accepted"
	ContractInvitationDeclined      Kind = "contract_invitation_declined"
	ContractEnded                   Kind = "contract_ended"
	ReportAnswered                  Kind = "report_answered"
	KindAnnouncement                Kind = "announcement"
	SubscriptionRequested           Kind = "subscription_requested"
	SubscriptionActivated           Kind = "subscription_activated"
	ManagerAccepted                 Kind = "manager_accepted"
	ManagerDeclined                 Kind = "manager_declined"
	ManagerRevoked                  Kind = "manager_revoked"
	ManagerBillSent                 Kind = "manager_bill_sent"
	ManagerBillEdited               Kind = "manager_bill_edited"
	LandlordApplicationReceived     Kind = "landlord_application_received"
	LandlordApplicationApproved     Kind = "landlord_application_approved"
	LandlordApplicationRejected     Kind = "landlord_application_rejected"
	LandlordRevoked                 Kind = "landlord_revoked"
	ListingSubmitted                Kind = "listing_submitted"
	ListingApproved                 Kind = "listing_approved"
	ListingRejected                 Kind = "listing_rejected"
	ListingTakenDown                Kind = "listing_taken_down"
)

// ErrUnknownKind is returned for a kind that is not in the catalog.
var ErrUnknownKind = errors.New("unknown notification kind")

// kindAudience is the side of an account each kind is for; a kind that is not listed shows in every role.
var kindAudience = map[Kind]Audience{
	// tenant side
	InvoiceSent: AudienceTenant, InvoiceUpdated: AudienceTenant, InvoiceDueSoon: AudienceTenant, InvoiceOverdue: AudienceTenant,
	PaymentConfirmed: AudienceTenant, PaymentRejected: AudienceTenant, ContractCreated: AudienceTenant,
	ContractInvitationReceived: AudienceTenant, ManagerInvitationReceived: AudienceTenant,
	ManagerInvitationWithdrawn: AudienceTenant, RegistrationApproved: AudienceTenant, RegistrationRejected: AudienceTenant,
	CancellationDeclined: AudienceTenant, LandlordApplicationApproved: AudienceTenant, LandlordApplicationRejected: AudienceTenant,
	LandlordRevoked: AudienceTenant,
	// landlord side
	PaymentSubmitted: AudienceLandlord, ReportReceived: AudienceLandlord, ReportReplied: AudienceLandlord,
	TenantInvoiceOverdue: AudienceLandlord, RegistrationConfirmed: AudienceLandlord, CancellationRequested: AudienceLandlord,
	ContractInvitationAccepted: AudienceLandlord, ContractInvitationDeclined: AudienceLandlord,
	ContractInvitationNeedsApproval: AudienceLandlord, ListingApproved: AudienceLandlord, ListingRejected: AudienceLandlord,
	ListingTakenDown: AudienceLandlord, ManagerAccepted: AudienceLandlord, ManagerDeclined: AudienceLandlord,
	ManagerBillSent: AudienceLandlord, ManagerBillEdited: AudienceLandlord,
}

// Kinds returns every kind, sorted.
func Kinds() []Kind {
	all := []Kind{
		InvoiceSent, InvoiceUpdated, InvoiceDueSoon, InvoiceOverdue, PaymentConfirmed, PaymentRejected, ContractCreated,
		ContractInvitationReceived, ManagerInvitationReceived, ContractInvitationNeedsApproval, ContractInvitationApproved,
		ContractInvitationRejected, ManagerInvitationWithdrawn, RegistrationApproved, RegistrationRejected,
		CancellationDeclined, PaymentSubmitted, ReportReceived, ReportReplied, TenantInvoiceOverdue, RegistrationConfirmed,
		CancellationRequested, ContractInvitationAccepted, ContractInvitationDeclined, ContractEnded, ReportAnswered,
		KindAnnouncement, SubscriptionRequested, SubscriptionActivated, ManagerAccepted, ManagerDeclined, ManagerRevoked,
		ManagerBillSent, ManagerBillEdited, LandlordApplicationReceived, LandlordApplicationApproved,
		LandlordApplicationRejected, LandlordRevoked, ListingSubmitted, ListingApproved, ListingRejected, ListingTakenDown,
	}
	slices.Sort(all)
	return all
}

// ParseKind returns the kind named s, or ErrUnknownKind.
func ParseKind(s string) (Kind, error) {
	if _, ok := kindSet[Kind(s)]; ok {
		return Kind(s), nil
	}
	return "", ErrUnknownKind
}

var kindSet = func() map[Kind]struct{} {
	set := make(map[Kind]struct{})
	for _, k := range Kinds() {
		set[k] = struct{}{}
	}
	return set
}()

// DefaultAudience is the side of an account the kind is for when the sender does not say: AudienceNone for a kind that
// shows in every role.
func (k Kind) DefaultAudience() Audience { return kindAudience[k] }
