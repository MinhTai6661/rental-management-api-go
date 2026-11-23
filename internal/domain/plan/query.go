package plan

import "github.com/google/uuid"

// PlanQuery selects a page of the catalog. A nil Audience means both.
type PlanQuery struct {
	Audience   *Audience
	ActiveOnly bool
	Limit      int
	Offset     int
}

// SubscriptionQuery selects a page of subscriptions, newest first. Nil filters are off.
type SubscriptionQuery struct {
	Status   *Status
	Audience *Audience
	UserID   *uuid.UUID
	Limit    int
	Offset   int
}

// Account is what the plan rules need to know about a user.
type Account struct {
	ID    uuid.UUID
	Name  string
	Roles []string
}

// LandlordUsage is what a landlord has used of the limits of their plan.
type LandlordUsage struct {
	Rooms           int
	Buildings       int
	ActiveContracts int
}
