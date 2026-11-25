package stats

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/stats"
)

// The answers below carry no tags: the HTTP adapter shapes the JSON. The cache stores them as they are, so their fields
// must stay plain values.

// Query is what a landlord asks of their figures.
type Query struct {
	LandlordID uuid.UUID
	// Role is the role the landlord acts in; part of the cache key.
	Role       string
	BuildingID *uuid.UUID
	Range      domain.MonthRange
	// Sort and Limit shape the ranking of rooms and buildings.
	Sort  domain.SortKey
	Limit int
}

// RoomCounts counts the rooms that are not deleted by status.
type RoomCounts struct {
	Total       int
	Rented      int
	Available   int
	Maintenance int
}

// MonthMoney is a month of the three readings of money.
type MonthMoney struct {
	Billed      int64
	Collected   int64
	Outstanding int64
}

// Overview is the top of a landlord's page.
type Overview struct {
	Rooms RoomCounts
	// OccupancyRate is the share of rooms rented now (by status), nil without rooms.
	OccupancyRate        *float64
	ActiveContracts      int
	PendingRegistrations int
	ExpiringSoon         int
	Money                OverviewMoney
}

// OverviewMoney is what is owed now and how this month compares with the last.
type OverviewMoney struct {
	OutstandingVND  int64
	OverdueVND      int64
	OverdueCount    int
	ThisMonth       MonthFigures
	PreviousMonth   MonthFigures
	BilledChange    *float64
	CollectedChange *float64
}

// MonthFigures are the billed and collected money of a month; CollectionRate is the part of what was billed that has
// been paid (nil when nothing was billed).
type MonthFigures struct {
	BilledVND      int64
	CollectedVND   int64
	CollectionRate *float64
}

// RevenueMonth is one month of the revenue.
type RevenueMonth struct {
	Month          string
	BilledVND      int64
	CollectedVND   int64
	OutstandingVND int64
}

// Revenue is the money of every month of the range and the sum of them.
type Revenue struct {
	Months []RevenueMonth
	Totals MonthMoney
}

// OccupancyMonth is one month of occupancy.
type OccupancyMonth struct {
	Month         string
	OccupiedDays  int
	AvailableDays int
	Rate          *float64
}

// RoomFigures is the money and the occupancy of a room over the whole range.
type RoomFigures struct {
	RoomID        uuid.UUID
	BuildingID    *uuid.UUID
	Name          string
	BilledVND     int64
	CollectedVND  int64
	OccupiedDays  int
	AvailableDays int
	OccupancyRate *float64
}

// BuildingFigures sums the rooms of a building (BuildingID nil: the rooms that belong to none).
type BuildingFigures struct {
	BuildingID    *uuid.UUID
	Name          *string
	Rooms         int
	Rented        int
	BilledVND     int64
	CollectedVND  int64
	OccupiedDays  int
	AvailableDays int
	OccupancyRate *float64
}

// Utility is what was read of one metered service in a month.
type Utility struct {
	Consumption float64
	CostVND     int64
}

// UtilityMonth is one month of electricity and water.
type UtilityMonth struct {
	Month       string
	Electricity Utility
	Water       Utility
}

// Dashboard is the figures of the front page in one answer; each part equals the endpoint of its own.
type Dashboard struct {
	Overview  Overview
	Revenue   Revenue
	Occupancy []OccupancyMonth
	Rooms     []RoomFigures
	Utilities []UtilityMonth
}

// PlatformOverview is the platform's figures for the staff.
type PlatformOverview struct {
	Users         UsersOverview
	Applications  ApplicationsOverview
	Subscriptions SubscriptionsOverview
	Announcements AnnouncementsOverview
}

// UsersOverview counts the accounts (a person with two roles counts under each, so the roles can add up to more than
// the total).
type UsersOverview struct {
	Total            int
	Active           int
	NewThisMonth     int
	NewPreviousMonth int
	Change           *float64
	ByRole           map[string]int
}

// ApplicationsOverview counts the applications to become a landlord.
type ApplicationsOverview struct {
	ByStatus map[string]int
	// AverageReviewHours and OldestWaitingDays are nil when there is nothing to average or nobody waiting.
	AverageReviewHours *float64
	OldestWaitingDays  *int
}

// SubscriptionsOverview counts the plan requests waiting for a decision.
type SubscriptionsOverview struct{ PendingRequests int }

// AnnouncementsOverview counts the announcements sent this month and how many of the last five were read.
type AnnouncementsOverview struct {
	SentThisMonth  int
	RecentReadRate *float64
}

// GrowthMonth is the accounts and applications that came in a month.
type GrowthMonth struct {
	Month        string
	Users        int
	Applications int
}

// SystemPlan is a plan and its people.
type SystemPlan struct {
	Code            string
	Name            string
	Audience        string
	PriceVND        int64
	BillingInterval string
	Subscribers     int
}

// SettingChange is a recent change of a system setting.
type SettingChange struct {
	At      time.Time
	ActorID *uuid.UUID
	Changes json.RawMessage
}

// System is what only a super admin sees.
type System struct {
	Admins               int
	SuperAdmins          int
	Plans                []SystemPlan
	MonthlyRecurringVND  int64
	PaidSubscribers      int
	SettingChanges       []SettingChange
	AuditImportantLast30 int64
	AuditRoutineLast30   int64
}
