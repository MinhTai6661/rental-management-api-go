package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

var ict = time.FixedZone("ICT", 7*3600)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, ict)
}

func TestDue(t *testing.T) {
	minuteJob := Job{Name: "m", Every: time.Minute}
	dailyJob := Job{Name: "d", DailyAt: "07:00"}
	yesterdayNoon, todayEight := at(23, 12, 0), at(24, 8, 0)
	for name, tc := range map[string]struct {
		job  Job
		last *time.Time
		now  time.Time
		want bool
	}{
		"every, never ran":                  {minuteJob, nil, at(24, 5, 0), true},
		"every, too early":                  {minuteJob, ptr(at(24, 5, 0)), at(24, 5, 0).Add(59 * time.Second), false},
		"every, exactly one interval":       {minuteJob, ptr(at(24, 5, 0)), at(24, 5, 1), true},
		"daily, first start before slot":    {dailyJob, nil, at(24, 6, 59), false},
		"daily, first start after slot":     {dailyJob, nil, at(24, 7, 0), true},
		"daily, ran after today's slot":     {dailyJob, &todayEight, at(24, 23, 0), false},
		"daily, ran yesterday, before slot": {dailyJob, &yesterdayNoon, at(24, 6, 0), false},
		"daily, ran yesterday, after slot":  {dailyJob, &yesterdayNoon, at(24, 7, 1), true},
		"daily, missed three days runs":     {dailyJob, ptr(at(21, 7, 5)), at(24, 9, 0), true},
		"daily, utc clock same instant":     {dailyJob, nil, at(24, 7, 0).UTC(), true},
		"bad time of day is never due":      {Job{Name: "x", DailyAt: "25:00"}, nil, at(24, 9, 0), false},
	} {
		if got := Due(tc.job, tc.last, tc.now, ict); got != tc.want {
			t.Errorf("%s: Due = %v, want %v", name, got, tc.want)
		}
	}
}

func TestJobValidate(t *testing.T) {
	run := func(context.Context) (string, error) { return "", nil }
	for name, job := range map[string]Job{
		"no name":   {Every: time.Minute, Run: run},
		"no func":   {Name: "a", Every: time.Minute},
		"neither":   {Name: "a", Run: run},
		"both":      {Name: "a", Every: time.Minute, DailyAt: "07:00", Run: run},
		"bad clock": {Name: "a", DailyAt: "7", Run: run},
	} {
		if job.Validate() == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if err := (Job{Name: "a", DailyAt: "07:00", Run: run}).Validate(); err != nil {
		t.Errorf("valid job refused: %v", err)
	}
}

func ptr(t time.Time) *time.Time { return &t }

type memoryLocker struct {
	mu   sync.Mutex
	held map[string]bool
}

func (locker *memoryLocker) TryLock(_ context.Context, key string, _ time.Duration) (func(), bool, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()
	if locker.held[key] {
		return nil, false, nil
	}
	locker.held[key] = true
	return func() {
		locker.mu.Lock()
		defer locker.mu.Unlock()
		delete(locker.held, key)
	}, true, nil
}

type memoryMarks struct {
	mu    sync.Mutex
	times map[string]time.Time
}

func (marks *memoryMarks) Last(_ context.Context, name string) (*time.Time, error) {
	marks.mu.Lock()
	defer marks.mu.Unlock()
	if last, ok := marks.times[name]; ok {
		return &last, nil
	}
	return nil, nil
}

func (marks *memoryMarks) Set(_ context.Context, name string, at time.Time) error {
	marks.mu.Lock()
	defer marks.mu.Unlock()
	marks.times[name] = at
	return nil
}

func newRunner(locker *memoryLocker, marks *memoryMarks, now *time.Time, job Job) *Runner {
	return &Runner{
		Jobs: []Job{job}, Locker: locker, Marks: marks, Loc: ict, Tick: time.Second, JobTimeout: time.Minute, LockTTL: 2 * time.Minute,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return *now },
	}
}

func TestRunnerRunsASlotOnceAcrossTwoRunners(t *testing.T) {
	locker, marks := &memoryLocker{held: map[string]bool{}}, &memoryMarks{times: map[string]time.Time{}}
	now := at(24, 9, 0)
	var runs int
	job := Job{Name: "j", Every: time.Minute, Run: func(context.Context) (string, error) { runs++; return "ok", nil }}
	first, second := newRunner(locker, marks, &now, job), newRunner(locker, marks, &now, job)

	first.RunDue(context.Background())
	second.RunDue(context.Background())
	if runs != 1 {
		t.Fatalf("one slot must run once, ran %d times", runs)
	}
	now = now.Add(time.Minute)
	second.RunDue(context.Background())
	first.RunDue(context.Background())
	if runs != 2 {
		t.Fatalf("the next slot must run once more, ran %d times", runs)
	}
}

func TestRunnerSkipsAJobThatAnotherInstanceIsRunning(t *testing.T) {
	locker, marks := &memoryLocker{held: map[string]bool{}}, &memoryMarks{times: map[string]time.Time{}}
	now := at(24, 9, 0)
	started, finish := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var runs int
	job := Job{Name: "j", Every: time.Minute, Run: func(context.Context) (string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		close(started)
		<-finish
		return "ok", nil
	}}
	first, second := newRunner(locker, marks, &now, job), newRunner(locker, marks, &now, job)

	done := make(chan struct{})
	go func() { first.RunDue(context.Background()); close(done) }()
	<-started
	second.RunDue(context.Background()) // the lock is held: it must not start a second run
	close(finish)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Fatalf("the job ran %d times at once", runs)
	}
}

func TestRunnerMarksAFailedJobSoItWaitsForItsNextSlot(t *testing.T) {
	locker, marks := &memoryLocker{held: map[string]bool{}}, &memoryMarks{times: map[string]time.Time{}}
	now := at(24, 9, 0)
	var runs int
	job := Job{Name: "j", Every: time.Hour, Run: func(context.Context) (string, error) { runs++; return "", errors.New("boom") }}
	runner := newRunner(locker, marks, &now, job)
	runner.RunDue(context.Background())
	runner.RunDue(context.Background())
	if runs != 1 {
		t.Fatalf("a failed job must not retry on the next tick, ran %d times", runs)
	}
}

func TestRunnerFinishesARunningJobWhenStopped(t *testing.T) {
	locker, marks := &memoryLocker{held: map[string]bool{}}, &memoryMarks{times: map[string]time.Time{}}
	now := at(24, 9, 0)
	ctx, stop := context.WithCancel(context.Background())
	finished := false
	job := Job{Name: "j", Every: time.Minute, Run: func(jobCtx context.Context) (string, error) {
		stop()
		time.Sleep(20 * time.Millisecond)
		if jobCtx.Err() != nil {
			t.Error("the running job was cancelled by the stop request")
		}
		finished = true
		return "ok", nil
	}}
	if err := newRunner(locker, marks, &now, job).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !finished {
		t.Fatal("Run returned before the job finished")
	}
}
