package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"rental-management/config"
	"rental-management/internal/adapter/persistence/postgres"
	adminpg "rental-management/internal/adapter/persistence/postgres/admin"
	redisstore "rental-management/internal/adapter/redis"
	"rental-management/internal/adapter/security"
	adminuc "rental-management/internal/usecase/admin"
	notificationuc "rental-management/internal/usecase/notification"
	propertyuc "rental-management/internal/usecase/property"
)

// admin is the use cases of the platform's staff work, on Postgres.
type admin struct {
	users        *adminuc.Users
	applications *adminuc.Applications
	profiles     *adminuc.Profiles
	settings     *adminuc.Settings
	legal        *adminuc.Legal
	personal     *adminuc.PersonalData
}

// adminPorts are the collaborators of the admin use cases that come from other contexts or from Redis; any may be nil
// (the seed and the commands that never touch them), which leaves the port at its default.
type adminPorts struct {
	rdb       *redis.Client
	notify    *notificationuc.Deliveries
	buildings *propertyuc.Buildings
}

// newAdmin builds the admin use cases. The first building of a new landlord comes from the property use cases, the
// messages go through the notification use cases and the sessions of a removed account are closed in Redis (see
// bridges_admin.go); the audit trail takes the events and the settings history reads from it.
func newAdmin(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, p adminPorts) admin {
	deps := adminuc.Deps{
		Tx: postgres.NewTxManager(pool), Users: adminpg.NewUserRepository(pool), Applications: adminpg.NewApplicationRepository(pool),
		Profiles: adminpg.NewProfileRepository(pool), Settings: adminpg.NewSettingRepository(pool), Legal: adminpg.NewLegalRepository(pool),
		Personal: adminpg.NewPersonalDataRepository(pool), Hasher: security.PasswordHasher{}, Clock: adminuc.ClockFunc(time.Now), Log: log,
		Audit: adminAudit{r: newAuditRecorder(log, pool)}, History: adminSettingHistory{r: newAudit(cfg, log, pool).reader},
	}
	if p.notify != nil {
		deps.Notifier = adminNotifier{m: p.notify}
	}
	if p.buildings != nil {
		deps.Buildings = firstBuildings{b: p.buildings}
	}
	if p.rdb != nil {
		deps.Revocations = redisstore.NewRevocationStore(p.rdb, cfg.RedisKeyPrefix)
	}
	opts := adminuc.Options{
		RevokedFor: cfg.AccessTokenTTL + cfg.JWTLeeway, TermsVersion: cfg.TermsVersion,
		Defaults: map[string]float64{
			"invoices.due_days": float64(cfg.InvoiceDueDays), "invoices.reminder_days": float64(cfg.InvoiceReminderDays),
		},
	}
	return admin{
		users: adminuc.NewUsers(deps, opts), applications: adminuc.NewApplications(deps, opts), profiles: adminuc.NewProfiles(deps, opts),
		settings: adminuc.NewSettings(deps, opts), legal: adminuc.NewLegal(deps, opts), personal: adminuc.NewPersonalData(deps, opts),
	}
}

// termsVersions tells the identity use cases which version of the terms people must accept: the newest publication
// of a legal document, or the configured version while none was published.
type termsVersions struct{ legal *adminuc.Legal }

// Current is the version in force.
func (t termsVersions) Current(ctx context.Context) (string, error) {
	return t.legal.CurrentVersion(ctx)
}

// firstBuildings gives a new landlord their first building through the property use cases.
type firstBuildings struct{ b *propertyuc.Buildings }

// EnsureFirstBuilding creates the building unless the landlord ever had one.
func (f firstBuildings) EnsureFirstBuilding(ctx context.Context, landlordID uuid.UUID, first adminuc.FirstBuilding) error {
	_, err := f.b.EnsureFirstBuilding(ctx, landlordID, propertyuc.FirstBuilding{
		Name: first.Name, WardCode: first.WardCode, AddressDetail: first.AddressDetail,
	})
	return err
}

// adminNotifier is the admin context's Notifier.
type adminNotifier struct{ m messenger }

// Notify sends an admin message to its user, or to every admin.
func (b adminNotifier) Notify(ctx context.Context, n adminuc.Notification) error {
	return sendTo(ctx, b.m, n.ToAdmins, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

var (
	_ adminuc.Notifier       = adminNotifier{}
	_ adminuc.FirstBuildings = firstBuildings{}
)
