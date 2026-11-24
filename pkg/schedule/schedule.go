// Package schedule runs named jobs on their own intervals or at a time of day, so that a single binary can replace
// cron. It holds no business rules: a job is a function. Several instances may run the same job list; a Locker and a
// Marks store (Redis in production) make sure that each slot runs once across all of them.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Job is one piece of periodic work. Exactly one of Every and DailyAt is set.
type Job struct {
	Name string
	// Every runs the job when this much time has passed since its last run.
	Every time.Duration
	// DailyAt ("HH:MM", in the Runner's location) runs the job once a day, once its time of day has come.
	DailyAt string
	// Run does the work and returns a one-line result for the log ("sent 3 reminders").
	Run func(ctx context.Context) (string, error)
}

// Validate reports a job that cannot be scheduled.
func (job Job) Validate() error {
	if job.Name == "" {
		return errors.New("a job needs a name")
	}
	if job.Run == nil {
		return fmt.Errorf("job %s has no function", job.Name)
	}
	if (job.Every > 0) == (job.DailyAt != "") {
		return fmt.Errorf("job %s needs exactly one of Every and DailyAt", job.Name)
	}
	if job.DailyAt != "" {
		if _, _, err := parseClock(job.DailyAt); err != nil {
			return fmt.Errorf("job %s: %w", job.Name, err)
		}
	}
	return nil
}

// parseClock reads "HH:MM".
func parseClock(value string) (hour, minute int, err error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("time of day %q is not HH:MM", value)
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("time of day %q is not HH:MM", value)
	}
	return hour, minute, nil
}

// Due reports whether the job should run at now, given the start of its last run (nil: never). A job that is not
// valid is never due. For a daily job, the most recent slot at or before now must be after the last run; a job that
// never ran waits for today's slot (it does not run at start-up just because a day has passed).
func Due(job Job, lastRun *time.Time, now time.Time, loc *time.Location) bool {
	if job.Every > 0 {
		return lastRun == nil || !now.Before(lastRun.Add(job.Every))
	}
	hour, minute, err := parseClock(job.DailyAt)
	if err != nil {
		return false
	}
	local := now.In(loc)
	slot := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if slot.After(local) {
		if lastRun == nil {
			return false
		}
		slot = slot.AddDate(0, 0, -1)
	}
	return lastRun == nil || lastRun.Before(slot)
}

// Locker hands out a lock that only one holder owns at a time.
type Locker interface {
	// TryLock returns ok=false when somebody else holds key. release frees the lock and is safe to call once.
	TryLock(ctx context.Context, key string, ttl time.Duration) (release func(), ok bool, err error)
}

// Marks remembers when each job last started, shared by every instance.
type Marks interface {
	Last(ctx context.Context, name string) (*time.Time, error)
	Set(ctx context.Context, name string, at time.Time) error
}

// Runner runs the jobs that are due on every tick.
type Runner struct {
	Jobs   []Job
	Locker Locker
	Marks  Marks
	// Loc is the time zone of the DailyAt slots (UTC when nil).
	Loc *time.Location
	// Tick is how often the runner looks at the jobs.
	Tick time.Duration
	// JobTimeout bounds one run; LockTTL must be longer than it, so the lock never lapses under a running job.
	JobTimeout time.Duration
	LockTTL    time.Duration
	Log        *slog.Logger
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

func (runner *Runner) now() time.Time {
	if runner.Now != nil {
		return runner.Now()
	}
	return time.Now()
}

func (runner *Runner) location() *time.Location {
	if runner.Loc != nil {
		return runner.Loc
	}
	return time.UTC
}

// Run looks at the jobs now and then every Tick until ctx ends. A job that is running when ctx ends is finished (up to
// JobTimeout) before Run returns.
func (runner *Runner) Run(ctx context.Context) error {
	for _, job := range runner.Jobs {
		if err := job.Validate(); err != nil {
			return err
		}
	}
	if runner.Tick <= 0 || runner.JobTimeout <= 0 || runner.LockTTL <= runner.JobTimeout {
		return errors.New("schedule: Tick and JobTimeout must be positive and LockTTL longer than JobTimeout")
	}
	runner.Log.Info("worker started", "jobs", len(runner.Jobs), "tick", runner.Tick.String())
	ticker := time.NewTicker(runner.Tick)
	defer ticker.Stop()
	for {
		runner.RunDue(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunDue runs, one after another, the jobs that are due now.
func (runner *Runner) RunDue(ctx context.Context) {
	for _, job := range runner.Jobs {
		if ctx.Err() != nil {
			return
		}
		runner.runIfDue(ctx, job)
	}
}

func (runner *Runner) runIfDue(ctx context.Context, job Job) {
	last, err := runner.Marks.Last(ctx, job.Name)
	if err != nil {
		runner.Log.Error("job mark unreadable", "job", job.Name, "error", err)
		return
	}
	if !Due(job, last, runner.now(), runner.location()) {
		return
	}
	release, locked, err := runner.Locker.TryLock(ctx, "worker:lock:"+job.Name, runner.LockTTL)
	if err != nil {
		runner.Log.Error("job lock failed", "job", job.Name, "error", err)
		return
	}
	if !locked {
		return // another instance is running it
	}
	defer release()

	// Another instance may have run the slot between the first read and the lock.
	last, err = runner.Marks.Last(ctx, job.Name)
	if err != nil {
		runner.Log.Error("job mark unreadable", "job", job.Name, "error", err)
		return
	}
	startedAt := runner.now()
	if !Due(job, last, startedAt, runner.location()) {
		return
	}

	// The run is not cancelled with ctx: a stop request waits for the job instead of cutting it in half.
	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runner.JobTimeout)
	defer cancel()
	began := time.Now()
	result, runErr := job.Run(jobCtx)
	duration := time.Since(began).Round(time.Millisecond)
	// The mark is set after a failure too, so a broken job retries at its next slot, not on every tick.
	if err := runner.Marks.Set(jobCtx, job.Name, startedAt); err != nil {
		runner.Log.Error("job mark not saved", "job", job.Name, "error", err)
	}
	if runErr != nil {
		runner.Log.Error("job failed", "job", job.Name, "duration", duration.String(), "error", runErr)
		return
	}
	runner.Log.Info("job done", "job", job.Name, "duration", duration.String(), "result", result)
}
