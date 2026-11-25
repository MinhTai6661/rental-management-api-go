package stats

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// The types below are what the repository reads for the use cases: plain facts, counted and summed by the database.
// Money is whole VND; days are Day values (midnight UTC of the calendar day in Zone).

// Scope is whose rooms a figure is about: one landlord's, or only one of their buildings.
type Scope struct {
	LandlordID uuid.UUID
	BuildingID *uuid.UUID
}

// Room statuses the figures count.
const (
	RoomRented      = "rented"
	RoomAvailable   = "available"
	RoomMaintenance = "maintenance"
)

// RoomFact is a room of the scope, deleted or not (a deleted room still had days and still billed).
type RoomFact struct {
	ID           uuid.UUID
	BuildingID   *uuid.UUID
	BuildingName *string
	Name         string
	Status       string
	CreatedOn    time.Time
	DeletedOn    *time.Time
}

// Deleted tells whether the room is gone.
func (r RoomFact) Deleted() bool { return r.DeletedOn != nil }

// Life is the days the room existed.
func (r RoomFact) Life() Span { return Span{From: r.CreatedOn, To: r.DeletedOn} }

// RentedContract is a contract that began (active or ended), as the occupancy reads it.
type RentedContract struct {
	RoomID   uuid.UUID
	StartsOn time.Time
	EndsOn   *time.Time
	Ended    bool
}

// Span is the days the contract lasted: an ended contract with no end date is taken to have ended today.
func (c RentedContract) Span(today time.Time) Span {
	if c.EndsOn == nil && c.Ended {
		return Span{From: c.StartsOn, To: &today}
	}
	return Span{From: c.StartsOn, To: c.EndsOn}
}

// ContractCounts are the contracts of the scope's standing rooms.
type ContractCounts struct {
	Active       int
	Pending      int
	ExpiringSoon int
}

// Owed is what the landlord still has to collect, whatever month it was billed in.
type Owed struct {
	OutstandingVND int64
	OverdueVND     int64
	OverdueCount   int
}

// UtilityKind is a metered service.
type UtilityKind string

// The metered services.
const (
	Electricity UtilityKind = "electricity"
	Water       UtilityKind = "water"
)

// UtilityUse is what was read in a month for one metered service.
type UtilityUse struct {
	Month       string
	Kind        UtilityKind
	Consumption float64
	CostVND     int64
}

// PlatformFacts are the counts of the whole platform for the staff.
type PlatformFacts struct {
	Users                UserCounts
	UsersByRole          map[string]int
	ApplicationsByStatus map[string]int
	ReviewedApplications int
	AverageReviewHours   float64
	WaitingApplications  int
	OldestWaitingDays    int
	PendingSubscriptions int
	AnnouncementsSent    int
	AnnouncementReached  int64
	AnnouncementRead     int64
}

// UserCounts are the accounts that are not deleted.
type UserCounts struct {
	Total            int
	Active           int
	NewThisMonth     int
	NewPreviousMonth int
}

// PlanFacts is a plan and how many people are on it.
type PlanFacts struct {
	Code            string
	Name            string
	Audience        string
	PriceVND        int64
	BillingInterval string
	Subscribers     int
}

// SettingChange is a change of a system setting, from the audit trail.
type SettingChange struct {
	At      time.Time
	ActorID *uuid.UUID
	Changes json.RawMessage
}

// SystemFacts are the figures only a super admin sees.
type SystemFacts struct {
	Admins         int
	SuperAdmins    int
	Plans          []PlanFacts
	SettingChanges []SettingChange
	AuditImportant int64
	AuditRoutine   int64
}

// YearlyInterval is the billing interval of a plan paid once a year.
const YearlyInterval = "year"

// MonthlyRecurring is what the plans bring in a month: price times subscribers, a yearly plan spread over 12 months.
func MonthlyRecurring(plans []PlanFacts) int64 {
	var monthly, yearly int64
	for _, p := range plans {
		total := p.PriceVND * int64(p.Subscribers)
		if p.BillingInterval == YearlyInterval {
			yearly += total
		} else {
			monthly += total
		}
	}
	return monthly + yearly/12
}

// PaidSubscribers is how many people are on a plan that costs money.
func PaidSubscribers(plans []PlanFacts) int {
	n := 0
	for _, p := range plans {
		if p.PriceVND > 0 {
			n += p.Subscribers
		}
	}
	return n
}
