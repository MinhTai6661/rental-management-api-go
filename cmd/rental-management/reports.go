package main

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	reportpg "rental-management/internal/adapter/persistence/postgres/report"
	notificationuc "rental-management/internal/usecase/notification"
	reportuc "rental-management/internal/usecase/report"
)

// reporting is the use cases of problem reports, on Postgres.
type reporting struct {
	reports *reportuc.Reports
	seeder  *reportuc.Seeder
}

// newReporting builds the report use cases. The rooms tenants rented are a read-only view in the report
// database adapter, the messages go through n (nil: nobody is told), the audit entries through the audit trail, and events
// tells the open conversations (nil: nobody).
func newReporting(log *slog.Logger, pool *pgxpool.Pool, n *notificationuc.Deliveries, events reportuc.ReportEvents) reporting {
	deps := reportuc.Deps{
		Tx: postgres.NewTxManager(pool), Reports: reportpg.NewReportRepository(pool), Rooms: reportpg.NewRoomReader(pool),
		Audit: reportAudit{r: newAuditRecorder(log, pool)}, Clock: reportuc.ClockFunc(time.Now), Log: log,
	}
	if n != nil {
		deps.Notifier = reportNotifier{m: n}
	}
	if events != nil {
		deps.Events = events
	}
	return reporting{reports: reportuc.NewReports(deps), seeder: reportuc.NewSeeder(deps)}
}
