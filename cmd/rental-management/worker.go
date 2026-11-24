package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	redisstore "rental-management/internal/adapter/redis"
	"rental-management/pkg/logger"
	"rental-management/pkg/schedule"
)

// Zones and limits of the worker. Vietnam has no daylight saving time, so a fixed offset is exact.
const (
	workerJobTimeout = 10 * time.Minute
	workerLockTTL    = 11 * time.Minute
)

var workerZone = time.FixedZone("ICT", 7*60*60)

// runWorker runs `rental-management worker`: the jobs of the cron-style commands, each on its own schedule, until the
// process is asked to stop. Any number of workers may run at once: a Redis lock and a Redis mark per job make every
// slot run on one of them only.
func runWorker(ctx context.Context, out io.Writer) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	if !cfg.HasRedis() {
		return errors.New("the worker needs REDIS_URL (its locks live there)")
	}
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := openRedis(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	pusher := realtimePusher{broadcast: redisstore.NewBroadcaster(rdb, cfg.RedisKeyPrefix, log)}
	inbox := newNotifications(cfg, log, pool, nil, pusher)
	pricing := newPlans(log, pool, inbox.deliveries)
	sealer, err := newSealer(cfg)
	if err != nil {
		return err
	}
	billing, err := newInvoices(cfg, log, pool, pricing, sealer, inbox.deliveries)
	if err != nil {
		return err
	}

	jobs := []schedule.Job{
		{Name: "announcements-dispatch-due", Every: cfg.WorkerDispatchInterval, Run: func(ctx context.Context) (string, error) {
			delivered, err := inbox.announcements.DispatchDue(ctx)
			return fmt.Sprintf("delivered %d announcements", delivered), err
		}},
		dailyJob(cfg.WorkerDailyInterval, "subscriptions-expire", "00:15", func(ctx context.Context) (string, error) {
			expired, err := pricing.subscriptions.ExpireDue(ctx)
			return fmt.Sprintf("expired %d subscriptions", expired), err
		}),
		dailyJob(cfg.WorkerDailyInterval, "notifications-prune", "03:30", func(ctx context.Context) (string, error) {
			deleted, err := inbox.pruner.Prune(ctx)
			return fmt.Sprintf("deleted %d notifications", deleted), err
		}),
		dailyJob(cfg.WorkerDailyInterval, "invoices-send-automatic", "07:00", func(ctx context.Context) (string, error) {
			sent, err := billing.automation.SendAutomatic(ctx)
			return fmt.Sprintf("sent %d invoices", sent), err
		}),
		dailyJob(cfg.WorkerDailyInterval, "invoices-send-reminders", "08:00", func(ctx context.Context) (string, error) {
			sent, err := billing.automation.SendReminders(ctx)
			return fmt.Sprintf("sent %d reminders", sent), err
		}),
	}

	runner := &schedule.Runner{
		Jobs: jobs, Locker: redisstore.NewLocker(rdb, cfg.RedisKeyPrefix), Marks: redisstore.NewMarks(rdb, cfg.RedisKeyPrefix),
		Loc: workerZone, Tick: cfg.WorkerTick, JobTimeout: workerJobTimeout, LockTTL: workerLockTTL, Log: log,
	}
	if err := runner.Run(ctx); err != nil {
		return err
	}
	log.Info("worker stopped")
	_, _ = fmt.Fprintln(out, "worker stopped")
	return nil
}

// dailyJob is a job that runs once a day at timeOfDay (ICT); a positive testInterval replaces that with a short
// interval, which is how the smoke test and demos see the daily jobs without waiting for the clock.
func dailyJob(testInterval time.Duration, name, timeOfDay string, run func(context.Context) (string, error)) schedule.Job {
	if testInterval > 0 {
		return schedule.Job{Name: name, Every: testInterval, Run: run}
	}
	return schedule.Job{Name: name, DailyAt: timeOfDay, Run: run}
}
