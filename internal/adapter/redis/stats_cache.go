package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"rental-management/internal/domain/shared"
)

// StatsCache keeps the answers of the statistics as JSON under the key prefix of the application.
type StatsCache struct {
	client *redis.Client
	prefix string
}

// NewStatsCache returns the cache; prefix is the Redis key prefix of the application.
func NewStatsCache(client *redis.Client, prefix string) *StatsCache {
	return &StatsCache{client: client, prefix: prefix}
}

// Get decodes the answer under key into dst and says whether there was one.
func (c *StatsCache) Get(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read stats cache: %w: %w", shared.ErrUnavailable, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, nil // an answer in a shape this build does not know is only a miss
	}
	return true, nil
}

// Set keeps v under key for ttl.
func (c *StatsCache) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := c.client.Set(ctx, c.prefix+key, raw, ttl).Err(); err != nil {
		return fmt.Errorf("write stats cache: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}
