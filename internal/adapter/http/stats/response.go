package statshttp

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	usecase "rental-management/internal/usecase/stats"
)

// The views give every figure a name that says what it is; money ends in _vnd and a ratio is a percentage with one
// decimal, null when there was nothing to divide by.

type roomCountsView struct {
	Total       int `json:"total"`
	Rented      int `json:"rented"`
	Available   int `json:"available"`
	Maintenance int `json:"maintenance"`
}

type monthFiguresView struct {
	BilledVND      int64    `json:"billed_vnd"`
	CollectedVND   int64    `json:"collected_vnd"`
	CollectionRate *float64 `json:"collection_rate"`
}

type overviewMoneyView struct {
	OutstandingVND  int64            `json:"outstanding_vnd"`
	OverdueVND      int64            `json:"overdue_vnd"`
	OverdueCount    int              `json:"overdue_count"`
	ThisMonth       monthFiguresView `json:"this_month"`
	PreviousMonth   monthFiguresView `json:"previous_month"`
	BilledChange    *float64         `json:"billed_change"`
	CollectedChange *float64         `json:"collected_change"`
}

type overviewView struct {
	Rooms                roomCountsView    `json:"rooms"`
	OccupancyRate        *float64          `json:"occupancy_rate"`
	ActiveContracts      int               `json:"active_contracts"`
	PendingRegistrations int               `json:"pending_registrations"`
	ExpiringSoon         int               `json:"expiring_soon"`
	Money                overviewMoneyView `json:"money"`
}

func overviewOf(o usecase.Overview) overviewView {
	figures := func(f usecase.MonthFigures) monthFiguresView {
		return monthFiguresView{BilledVND: f.BilledVND, CollectedVND: f.CollectedVND, CollectionRate: f.CollectionRate}
	}
	return overviewView{
		Rooms:         roomCountsView(o.Rooms),
		OccupancyRate: o.OccupancyRate, ActiveContracts: o.ActiveContracts, PendingRegistrations: o.PendingRegistrations, ExpiringSoon: o.ExpiringSoon,
		Money: overviewMoneyView{
			OutstandingVND: o.Money.OutstandingVND, OverdueVND: o.Money.OverdueVND, OverdueCount: o.Money.OverdueCount,
			ThisMonth: figures(o.Money.ThisMonth), PreviousMonth: figures(o.Money.PreviousMonth),
			BilledChange: o.Money.BilledChange, CollectedChange: o.Money.CollectedChange,
		},
	}
}

type revenueMonthView struct {
	Month          string `json:"month"`
	BilledVND      int64  `json:"billed_vnd"`
	CollectedVND   int64  `json:"collected_vnd"`
	OutstandingVND int64  `json:"outstanding_vnd"`
}

type revenueTotalsView struct {
	BilledVND      int64 `json:"billed_vnd"`
	CollectedVND   int64 `json:"collected_vnd"`
	OutstandingVND int64 `json:"outstanding_vnd"`
}

type revenueView struct {
	Months []revenueMonthView `json:"months"`
	Totals revenueTotalsView  `json:"totals"`
}

func revenueOf(r usecase.Revenue) revenueView {
	out := revenueView{Months: make([]revenueMonthView, len(r.Months)),
		Totals: revenueTotalsView{BilledVND: r.Totals.Billed, CollectedVND: r.Totals.Collected, OutstandingVND: r.Totals.Outstanding}}
	for i, m := range r.Months {
		out.Months[i] = revenueMonthView(m)
	}
	return out
}

type occupancyMonthView struct {
	Month         string   `json:"month"`
	OccupiedDays  int      `json:"occupied_days"`
	AvailableDays int      `json:"available_days"`
	Rate          *float64 `json:"rate"`
}

type occupancyView struct {
	Months []occupancyMonthView `json:"months"`
}

func occupancyOf(months []usecase.OccupancyMonth) occupancyView {
	out := occupancyView{Months: make([]occupancyMonthView, len(months))}
	for i, m := range months {
		out.Months[i] = occupancyMonthView(m)
	}
	return out
}

type roomFiguresView struct {
	RoomID        uuid.UUID  `json:"room_id"`
	BuildingID    *uuid.UUID `json:"building_id"`
	Name          string     `json:"name"`
	BilledVND     int64      `json:"billed_vnd"`
	CollectedVND  int64      `json:"collected_vnd"`
	OccupiedDays  int        `json:"occupied_days"`
	AvailableDays int        `json:"available_days"`
	OccupancyRate *float64   `json:"occupancy_rate"`
}

type roomsView struct {
	Rooms []roomFiguresView `json:"rooms"`
}

func roomsOf(rooms []usecase.RoomFigures) roomsView {
	out := roomsView{Rooms: make([]roomFiguresView, len(rooms))}
	for i, r := range rooms {
		out.Rooms[i] = roomFiguresView(r)
	}
	return out
}

type buildingFiguresView struct {
	BuildingID    *uuid.UUID `json:"building_id"`
	Name          *string    `json:"name"`
	Rooms         int        `json:"rooms"`
	Rented        int        `json:"rented"`
	BilledVND     int64      `json:"billed_vnd"`
	CollectedVND  int64      `json:"collected_vnd"`
	OccupiedDays  int        `json:"occupied_days"`
	AvailableDays int        `json:"available_days"`
	OccupancyRate *float64   `json:"occupancy_rate"`
}

type buildingsView struct {
	Buildings []buildingFiguresView `json:"buildings"`
}

func buildingsOf(buildings []usecase.BuildingFigures) buildingsView {
	out := buildingsView{Buildings: make([]buildingFiguresView, len(buildings))}
	for i, b := range buildings {
		out.Buildings[i] = buildingFiguresView(b)
	}
	return out
}

type utilityView struct {
	Consumption float64 `json:"consumption"`
	CostVND     int64   `json:"cost_vnd"`
}

type utilityMonthView struct {
	Month       string      `json:"month"`
	Electricity utilityView `json:"electricity"`
	Water       utilityView `json:"water"`
}

type utilitiesView struct {
	Months []utilityMonthView `json:"months"`
}

func utilitiesOf(months []usecase.UtilityMonth) utilitiesView {
	out := utilitiesView{Months: make([]utilityMonthView, len(months))}
	for i, m := range months {
		out.Months[i] = utilityMonthView{Month: m.Month, Electricity: utilityView(m.Electricity), Water: utilityView(m.Water)}
	}
	return out
}

type dashboardView struct {
	Overview  overviewView  `json:"overview"`
	Revenue   revenueView   `json:"revenue"`
	Occupancy occupancyView `json:"occupancy"`
	Rooms     roomsView     `json:"rooms"`
	Utilities utilitiesView `json:"utilities"`
}

func dashboardOf(d usecase.Dashboard) dashboardView {
	return dashboardView{Overview: overviewOf(d.Overview), Revenue: revenueOf(d.Revenue), Occupancy: occupancyOf(d.Occupancy), Rooms: roomsOf(d.Rooms), Utilities: utilitiesOf(d.Utilities)}
}

// Platform views.

type usersOverviewView struct {
	Total            int            `json:"total"`
	Active           int            `json:"active"`
	NewThisMonth     int            `json:"new_this_month"`
	NewPreviousMonth int            `json:"new_previous_month"`
	Change           *float64       `json:"change"`
	ByRole           map[string]int `json:"by_role"`
}

type applicationsOverviewView struct {
	ByStatus           map[string]int `json:"by_status"`
	AverageReviewHours *float64       `json:"average_review_hours"`
	OldestWaitingDays  *int           `json:"oldest_waiting_days"`
}

type platformOverviewView struct {
	Users         usersOverviewView        `json:"users"`
	Applications  applicationsOverviewView `json:"applications"`
	Subscriptions struct {
		PendingRequests int `json:"pending_requests"`
	} `json:"subscriptions"`
	Announcements struct {
		SentThisMonth  int      `json:"sent_this_month"`
		RecentReadRate *float64 `json:"recent_read_rate"`
	} `json:"announcements"`
}

func platformOverviewOf(o usecase.PlatformOverview) platformOverviewView {
	v := platformOverviewView{
		Users:        usersOverviewView(o.Users),
		Applications: applicationsOverviewView(o.Applications),
	}
	v.Subscriptions.PendingRequests = o.Subscriptions.PendingRequests
	v.Announcements.SentThisMonth, v.Announcements.RecentReadRate = o.Announcements.SentThisMonth, o.Announcements.RecentReadRate
	return v
}

type growthMonthView struct {
	Month        string `json:"month"`
	Users        int    `json:"users"`
	Applications int    `json:"applications"`
}

type growthView struct {
	Months []growthMonthView `json:"months"`
}

func growthOf(months []usecase.GrowthMonth) growthView {
	out := growthView{Months: make([]growthMonthView, len(months))}
	for i, m := range months {
		out.Months[i] = growthMonthView(m)
	}
	return out
}

type systemPlanView struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Audience        string `json:"audience"`
	PriceVND        int64  `json:"price_vnd"`
	BillingInterval string `json:"billing_interval"`
	Subscribers     int    `json:"subscribers"`
}

type settingChangeView struct {
	At      time.Time       `json:"at"`
	ActorID *uuid.UUID      `json:"actor_id"`
	Changes json.RawMessage `json:"changes"`
}

type systemView struct {
	Staff struct {
		Admins      int `json:"admins"`
		SuperAdmins int `json:"super_admins"`
	} `json:"staff"`
	Plans               []systemPlanView    `json:"plans"`
	MonthlyRecurringVND int64               `json:"monthly_recurring_vnd"`
	PaidSubscribers     int                 `json:"paid_subscribers"`
	SettingChanges      []settingChangeView `json:"setting_changes"`
	AuditLast30Days     struct {
		Important int64 `json:"important"`
		Routine   int64 `json:"routine"`
	} `json:"audit_last_30_days"`
}

func systemOf(s usecase.System) systemView {
	v := systemView{Plans: make([]systemPlanView, len(s.Plans)), MonthlyRecurringVND: s.MonthlyRecurringVND,
		PaidSubscribers: s.PaidSubscribers, SettingChanges: make([]settingChangeView, len(s.SettingChanges))}
	v.Staff.Admins, v.Staff.SuperAdmins = s.Admins, s.SuperAdmins
	v.AuditLast30Days.Important, v.AuditLast30Days.Routine = s.AuditImportantLast30, s.AuditRoutineLast30
	for i, p := range s.Plans {
		v.Plans[i] = systemPlanView(p)
	}
	for i, c := range s.SettingChanges {
		v.SettingChanges[i] = settingChangeView(c)
	}
	return v
}
