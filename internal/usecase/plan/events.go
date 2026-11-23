package plan

// The kinds of notification the plan use cases send; the web translates each with the notification's params
// (user and plan for a request, plan for an activation). They are the kinds of the Laravel API.
const (
	NotifySubscriptionRequested = "subscription_requested"
	NotifySubscriptionActivated = "subscription_activated"
)

// The audit actions the plan use cases record.
const (
	AuditSubscriptionRequested = "subscription.requested"
	AuditSubscriptionAssigned  = "subscription.assigned"
	AuditSubscriptionActivated = "subscription.activated"
	AuditSubscriptionCanceled  = "subscription.canceled"
	AuditSubscriptionsExpired  = "subscription.expired"
	AuditPlanSaved             = "plan.saved"
)
