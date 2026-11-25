package stats

import (
	"context"
	"time"

	domain "rental-management/internal/domain/stats"
)

// reviewWindowDays is how far back the time to review an application is averaged; auditWindowDays is the span the
// audit trail is counted over.
const (
	reviewWindowDays = 90
	auditWindowDays  = 30
)

// platformRoles are the roles the overview counts, so a role nobody holds still shows 0.
var platformRoles = []string{"tenant", "landlord", "manager", "admin", "super_admin"}

// Platform serves the figures of the whole platform to the staff. It counts accounts and applications and never lists
// a person, and it is not cached.
type Platform struct {
	repo  PlatformRepository
	clock Clock
}

// NewPlatform returns the use cases.
func NewPlatform(repo PlatformRepository, clock Clock) *Platform {
	return &Platform{repo: repo, clock: clock}
}

// monthStart is the first instant of the month of day, in the zone people read dates in.
func monthStart(m domain.Month) time.Time {
	return time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, domain.Zone)
}

// Overview counts the accounts, the applications, the plan requests and the announcements.
func (p *Platform) Overview(ctx context.Context) (PlatformOverview, error) {
	now := p.clock.Now()
	this := domain.MonthOf(domain.Day(now))
	facts, err := p.repo.Facts(ctx, monthStart(this), monthStart(this.Previous()), now.AddDate(0, 0, -reviewWindowDays), now)
	if err != nil {
		return PlatformOverview{}, err
	}
	byRole := make(map[string]int, len(platformRoles))
	for _, role := range platformRoles {
		byRole[role] = facts.UsersByRole[role]
	}
	byStatus := map[string]int{"pending_review": 0, "approved": 0, "rejected": 0}
	for status, n := range facts.ApplicationsByStatus {
		byStatus[status] = n
	}
	apps := ApplicationsOverview{ByStatus: byStatus}
	if facts.ReviewedApplications > 0 {
		hours := facts.AverageReviewHours
		apps.AverageReviewHours = &hours
	}
	if facts.WaitingApplications > 0 {
		days := facts.OldestWaitingDays
		apps.OldestWaitingDays = &days
	}
	return PlatformOverview{
		Users: UsersOverview{
			Total: facts.Users.Total, Active: facts.Users.Active, NewThisMonth: facts.Users.NewThisMonth,
			NewPreviousMonth: facts.Users.NewPreviousMonth,
			Change:           domain.Change(int64(facts.Users.NewThisMonth), int64(facts.Users.NewPreviousMonth)),
			ByRole:           byRole,
		},
		Applications:  apps,
		Subscriptions: SubscriptionsOverview{PendingRequests: facts.PendingSubscriptions},
		Announcements: AnnouncementsOverview{
			SentThisMonth:  facts.AnnouncementsSent,
			RecentReadRate: domain.Percent(facts.AnnouncementRead, facts.AnnouncementReached),
		},
	}, nil
}

// Growth counts the new accounts and applications of every month of the range.
func (p *Platform) Growth(ctx context.Context, rng domain.MonthRange) ([]GrowthMonth, error) {
	months := rng.Months()
	users, apps, err := p.repo.SignUps(ctx, monthStart(months[0]), monthStart(months[len(months)-1].Next()))
	if err != nil {
		return nil, err
	}
	out := make([]GrowthMonth, len(months))
	for i, m := range months {
		out[i] = GrowthMonth{Month: m.String(), Users: users[m.String()], Applications: apps[m.String()]}
	}
	return out, nil
}

// System reads the figures only a super admin sees: staff, plans and what they bring in, the latest changes of the
// settings and the size of the audit trail over the last 30 days.
func (p *Platform) System(ctx context.Context) (System, error) {
	facts, err := p.repo.System(ctx, p.clock.Now().AddDate(0, 0, -auditWindowDays))
	if err != nil {
		return System{}, err
	}
	out := System{
		Admins: facts.Admins, SuperAdmins: facts.SuperAdmins,
		Plans:                make([]SystemPlan, len(facts.Plans)),
		MonthlyRecurringVND:  domain.MonthlyRecurring(facts.Plans),
		PaidSubscribers:      domain.PaidSubscribers(facts.Plans),
		SettingChanges:       make([]SettingChange, len(facts.SettingChanges)),
		AuditImportantLast30: facts.AuditImportant, AuditRoutineLast30: facts.AuditRoutine,
	}
	for i, pl := range facts.Plans {
		out.Plans[i] = SystemPlan(pl)
	}
	for i, c := range facts.SettingChanges {
		out.SettingChanges[i] = SettingChange(c)
	}
	return out, nil
}
