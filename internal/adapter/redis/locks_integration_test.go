//go:build integration

package redisstore

import (
	"context"
	"testing"
	"time"
)

func TestLockerGivesTheLockToOneHolderUntilReleased(t *testing.T) {
	client := rds.Client(t)
	ctx := context.Background()
	first, second := NewLocker(client, "test:"), NewLocker(client, "test:")

	release, ok, err := first.TryLock(ctx, "worker:lock:job", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if _, ok, err := second.TryLock(ctx, "worker:lock:job", time.Minute); err != nil || ok {
		t.Fatalf("second lock must be refused: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := second.TryLock(ctx, "worker:lock:other", time.Minute); !ok {
		t.Fatal("another key must be free")
	}
	release()
	release2, ok, err := second.TryLock(ctx, "worker:lock:job", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lock after release: ok=%v err=%v", ok, err)
	}
	release2()
}

func TestLockExpiresAndAStaleReleaseDoesNotFreeTheNewHolder(t *testing.T) {
	client := rds.Client(t)
	ctx := context.Background()
	locker := NewLocker(client, "test:")

	staleRelease, ok, _ := locker.TryLock(ctx, "worker:lock:ttl", 100*time.Millisecond)
	if !ok {
		t.Fatal("first lock refused")
	}
	time.Sleep(200 * time.Millisecond)
	release, ok, _ := locker.TryLock(ctx, "worker:lock:ttl", time.Minute)
	if !ok {
		t.Fatal("an expired lock must be free")
	}
	staleRelease() // must not delete the lock of the new holder
	if _, ok, _ := locker.TryLock(ctx, "worker:lock:ttl", time.Minute); ok {
		t.Fatal("a stale release freed somebody else's lock")
	}
	release()
}

func TestMarksRoundTrip(t *testing.T) {
	marks := NewMarks(rds.Client(t), "test:")
	ctx := context.Background()
	if last, err := marks.Last(ctx, "never"); err != nil || last != nil {
		t.Fatalf("unknown job: %v %v", last, err)
	}
	at := time.Date(2026, 11, 24, 7, 0, 0, 5, time.UTC)
	if err := marks.Set(ctx, "job", at); err != nil {
		t.Fatal(err)
	}
	if last, err := marks.Last(ctx, "job"); err != nil || last == nil || !last.Equal(at) {
		t.Fatalf("mark = %v, %v; want %v", last, err, at)
	}
}
