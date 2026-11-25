package statspg

import (
	"context"
	"time"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/stats"
)

// Facts counts the platform for the staff.
func (r *Repository) Facts(ctx context.Context, monthStart, previousStart, reviewedSince, now time.Time) (domain.PlatformFacts, error) {
	q := postgres.Queries(ctx, r.pool)
	var f domain.PlatformFacts
	users, err := q.StatsUserCounts(ctx, sqlcgen.StatsUserCountsParams{MonthStart: monthStart, PreviousStart: previousStart})
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.Users = domain.UserCounts{Total: int(users.Total), Active: int(users.Active), NewThisMonth: int(users.NewThisMonth), NewPreviousMonth: int(users.NewPreviousMonth)}
	roles, err := q.StatsUsersByRole(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.UsersByRole = make(map[string]int, len(roles))
	for _, row := range roles {
		f.UsersByRole[row.RoleCode] = int(row.Users)
	}
	statuses, err := q.StatsApplicationsByStatus(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.ApplicationsByStatus = make(map[string]int, len(statuses))
	for _, row := range statuses {
		f.ApplicationsByStatus[row.Status] = int(row.Applications)
	}
	review, err := q.StatsApplicationReview(ctx, sqlcgen.StatsApplicationReviewParams{Since: reviewedSince, Now: now})
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.ReviewedApplications, f.AverageReviewHours = int(review.Reviewed), review.AverageReviewHours
	f.WaitingApplications, f.OldestWaitingDays = int(review.Waiting), int(review.OldestWaitingDays)
	pending, err := q.StatsPendingSubscriptions(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.PendingSubscriptions = int(pending)
	sent, err := q.StatsAnnouncementsSent(ctx, monthStart)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.AnnouncementsSent = int(sent)
	reads, err := q.StatsAnnouncementReads(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.AnnouncementReached, f.AnnouncementRead = reads.Reached, reads.Read
	return f, nil
}

// SignUps counts new accounts and new landlord applications by month.
func (r *Repository) SignUps(ctx context.Context, from, before time.Time) (map[string]int, map[string]int, error) {
	q := postgres.Queries(ctx, r.pool)
	users, err := q.StatsUsersByMonth(ctx, sqlcgen.StatsUsersByMonthParams{FromAt: from, BeforeAt: before})
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	apps, err := q.StatsApplicationsByMonth(ctx, sqlcgen.StatsApplicationsByMonthParams{FromAt: from, BeforeAt: before})
	if err != nil {
		return nil, nil, postgres.Translate(err)
	}
	usersOut, appsOut := make(map[string]int, len(users)), make(map[string]int, len(apps))
	for _, row := range users {
		usersOut[row.Month] = int(row.Total)
	}
	for _, row := range apps {
		appsOut[row.Month] = int(row.Total)
	}
	return usersOut, appsOut, nil
}

// System reads the figures of the system.
func (r *Repository) System(ctx context.Context, auditSince time.Time) (domain.SystemFacts, error) {
	q := postgres.Queries(ctx, r.pool)
	var f domain.SystemFacts
	staff, err := q.StatsStaffCounts(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.Admins, f.SuperAdmins = int(staff.Admins), int(staff.SuperAdmins)
	plans, err := q.StatsPlanSubscribers(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	for _, p := range plans {
		f.Plans = append(f.Plans, domain.PlanFacts{Code: p.Code, Name: p.Name, Audience: p.Audience, PriceVND: p.PriceVnd, BillingInterval: p.BillingInterval, Subscribers: int(p.Subscribers)})
	}
	changes, err := q.StatsRecentSettingChanges(ctx)
	if err != nil {
		return f, postgres.Translate(err)
	}
	for _, c := range changes {
		f.SettingChanges = append(f.SettingChanges, domain.SettingChange{At: c.CreatedAt, ActorID: c.ActorID, Changes: c.Changes})
	}
	counts, err := q.StatsAuditCounts(ctx, auditSince)
	if err != nil {
		return f, postgres.Translate(err)
	}
	f.AuditImportant, f.AuditRoutine = counts.Important, counts.Routine
	return f, nil
}
