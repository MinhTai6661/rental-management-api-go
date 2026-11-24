package redisstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"rental-management/internal/domain/shared"
)

// releaseScript deletes the lock only while it still holds our token, so a lock that lapsed and was taken by somebody
// else is never released by mistake.
var releaseScript = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)

// Locker is a lock shared by every instance (SET NX PX). It implements schedule.Locker.
type Locker struct {
	client *redis.Client
	prefix string
}

// NewLocker returns the locker; prefix is the Redis key prefix of the application.
func NewLocker(client *redis.Client, prefix string) *Locker {
	return &Locker{client: client, prefix: prefix}
}

// TryLock takes key for ttl. ok is false when somebody holds it; release frees it early.
func (locker *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (func(), bool, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, false, fmt.Errorf("lock token: %w", err)
	}
	token, fullKey := hex.EncodeToString(tokenBytes), locker.prefix+key
	taken, err := locker.client.SetNX(ctx, fullKey, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("take lock: %w: %w", shared.ErrUnavailable, err)
	}
	if !taken {
		return nil, false, nil
	}
	return func() {
		// ctx may be cancelled by now (the worker is stopping), so the release has its own deadline.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = releaseScript.Run(releaseCtx, locker.client, []string{fullKey}, token).Err()
	}, true, nil
}

// Marks keeps when each worker job last started. It implements schedule.Marks.
type Marks struct {
	client *redis.Client
	prefix string
}

// NewMarks returns the marks store; prefix is the Redis key prefix of the application.
func NewMarks(client *redis.Client, prefix string) *Marks {
	return &Marks{client: client, prefix: prefix}
}

// Last returns the start of the last run of the job, or nil when it never ran.
func (marks *Marks) Last(ctx context.Context, name string) (*time.Time, error) {
	value, err := marks.client.Get(ctx, marks.prefix+"worker:last:"+name).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read job mark: %w: %w", shared.ErrUnavailable, err)
	}
	last, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("job mark %q is not a time: %w", name, err)
	}
	return &last, nil
}

// Set records that the job started at the given time.
func (marks *Marks) Set(ctx context.Context, name string, at time.Time) error {
	if err := marks.client.Set(ctx, marks.prefix+"worker:last:"+name, at.UTC().Format(time.RFC3339Nano), 0).Err(); err != nil {
		return fmt.Errorf("save job mark: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}
