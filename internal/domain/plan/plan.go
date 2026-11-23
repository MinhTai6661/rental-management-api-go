// Package plan holds the rules of service plans and subscriptions: which plan a user is on, how long a subscription
// runs, who may be given which plan and what a limit of null means. It imports the standard library, uuid and
// pkg/authz only: no database, no HTTP, no other context.
package plan

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// Audience is the kind of user a plan is for: landlords get landlord plans, everybody else tenant plans.
type Audience string

// The audiences.
const (
	AudienceLandlord Audience = "landlord"
	AudienceTenant   Audience = "tenant"
)

// ParseAudience reads an audience code.
func ParseAudience(s string) (Audience, bool) {
	switch a := Audience(s); a {
	case AudienceLandlord, AudienceTenant:
		return a, true
	}
	return "", false
}

// AudienceOfRole is the audience of the role a request acts in: landlord for a landlord, tenant for everybody else.
func AudienceOfRole(role string) Audience {
	if role == authz.RoleLandlord {
		return AudienceLandlord
	}
	return AudienceTenant
}

// Interval is how often a paid plan has to be renewed.
type Interval string

// The billing intervals.
const (
	IntervalMonth Interval = "month"
	IntervalYear  Interval = "year"
)

// ParseInterval reads an interval code.
func ParseInterval(s string) (Interval, bool) {
	switch i := Interval(s); i {
	case IntervalMonth, IntervalYear:
		return i, true
	}
	return "", false
}

// EndsAfter is when a subscription that starts at start has to be renewed: one month or one year later, never
// overflowing into the next month (31 Jan + 1 month = 28 Feb).
func (i Interval) EndsAfter(start time.Time) time.Time {
	if i == IntervalYear {
		return addMonths(start, 12)
	}
	return addMonths(start, 1)
}

// addMonths adds calendar months and keeps the day of the month when the target month has it, else its last day.
func addMonths(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	lastDay := time.Date(y, m+time.Month(months)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(y, m+time.Month(months), min(d, lastDay), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// Feature is something a plan switches on besides its numeric limits.
type Feature string

// The features. Codes never change meaning and are never reused.
const (
	// FeatureAutoInvoicing: invoices are generated and sent to the tenants automatically.
	FeatureAutoInvoicing Feature = "auto_invoicing"
	// FeatureVIPBadge: a VIP badge next to the landlord's name and rooms.
	FeatureVIPBadge Feature = "vip_badge"
	// FeaturePrioritySupport: the account's requests are answered first.
	FeaturePrioritySupport Feature = "priority_support"
)

// Features lists every feature.
func Features() []Feature {
	return []Feature{FeatureAutoInvoicing, FeatureVIPBadge, FeaturePrioritySupport}
}

// ParseFeature reads a feature code.
func ParseFeature(s string) (Feature, bool) {
	f := Feature(s)
	return f, slices.Contains(Features(), f)
}

// Limits are the numbers a plan caps. nil means unlimited.
type Limits struct {
	MaxRooms                *int
	MaxBuildings            *int
	MaxImagesPerRoom        *int
	MaxActiveContracts      *int
	MaxPendingRegistrations *int
}

// Plan is a row of the catalog.
type Plan struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description *string
	Audience    Audience
	// PriceVND is the price of one billing interval in whole VND; 0 is a free plan.
	PriceVND int64
	Interval Interval
	Limits   Limits
	// SearchPriority puts the landlord's rooms higher in find rooms (greater first).
	SearchPriority int
	Features       []Feature
	// IsDefault: the plan an account of the audience is on while it has no subscription.
	IsDefault bool
	// IsActive: an inactive plan can no longer be taken, but the subscriptions to it keep working.
	IsActive     bool
	DisplayOrder int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsFree reports whether the plan costs nothing.
func (p Plan) IsFree() bool { return p.PriceVND == 0 }

// HasFeature reports whether the plan switches the feature on.
func (p Plan) HasFeature(f Feature) bool { return slices.Contains(p.Features, f) }

// Status is where a subscription stands.
type Status string

// The subscription statuses.
const (
	// StatusPending: asked for by the user, waiting for an admin to activate it once paid.
	StatusPending Status = "pending"
	StatusActive  Status = "active"
	// StatusCanceled: replaced by another plan or ended by the user or an admin.
	StatusCanceled Status = "canceled"
	// StatusExpired: ran past its end.
	StatusExpired Status = "expired"
)

// ParseStatus reads a status code.
func ParseStatus(s string) (Status, bool) {
	switch st := Status(s); st {
	case StatusPending, StatusActive, StatusCanceled, StatusExpired:
		return st, true
	}
	return "", false
}

// Subscription ties an account to a plan. An account has at most one active and one pending subscription per
// audience, so a person who is both landlord and tenant has a plan on each side.
type Subscription struct {
	ID     uuid.UUID
	UserID uuid.UUID
	PlanID uuid.UUID
	// Audience is the audience of the plan, copied so the "one per audience" rule is a database constraint.
	Audience Audience
	Status   Status
	StartsAt time.Time
	// EndsAt is when it lapses; nil = never.
	EndsAt     *time.Time
	CanceledAt *time.Time
	// GrantedByID is the admin who handed it out; nil when the user took it themselves.
	GrantedByID *uuid.UUID
	CreatedAt   time.Time
}

// RunningAt reports whether the subscription counts at the moment: active, started and not past its end.
func (s Subscription) RunningAt(now time.Time) bool {
	return s.Status == StatusActive && !s.StartsAt.After(now) && (s.EndsAt == nil || s.EndsAt.After(now))
}

// Detail is a subscription with its plan and, for the admin's list, whose it is.
type Detail struct {
	Subscription
	Plan      Plan
	UserName  string
	UserEmail *string
}
