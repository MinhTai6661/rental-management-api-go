package plan

import "errors"

// The errors of the plan use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code and the fields.
var (
	// ErrPlanNotFound: no such plan (named in a path).
	ErrPlanNotFound = errors.New("plan not found")
	// ErrPlanUnknown: the plan a request asks for does not exist (named in the body).
	ErrPlanUnknown = errors.New("plan does not exist")
	// ErrSubscriptionNotFound: no such subscription.
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrAccountNotFound: no such account.
	ErrAccountNotFound = errors.New("account not found")
	// ErrWrongAudience: the account does not hold the role the plan is for (or acts in the other one).
	ErrWrongAudience = errors.New("plan is not for this kind of account")
	// ErrPlanInactive: the plan is no longer offered.
	ErrPlanInactive = errors.New("plan is no longer offered")
	// ErrAlreadyOnPlan: the account is on that plan already.
	ErrAlreadyOnPlan = errors.New("already on this plan")
	// ErrNotPending: only a pending subscription can be activated.
	ErrNotPending = errors.New("subscription is not pending")
	// ErrCodeTaken: another plan has the code.
	ErrCodeTaken = errors.New("plan code is taken")
	// ErrInvalidCode: the code is not lower case letters, digits and underscores.
	ErrInvalidCode = errors.New("plan code is invalid")
	// ErrEndInPast: a subscription cannot end before it starts.
	ErrEndInPast = errors.New("end is not in the future")
)
