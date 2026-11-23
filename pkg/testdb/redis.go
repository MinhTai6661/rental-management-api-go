//go:build integration

package testdb

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// Redis is a throwaway Redis container.
type Redis struct {
	container *tcredis.RedisContainer
	host      string
	port      uint16
}

// StartRedis starts the container. Call Terminate when the package's tests are done.
func StartRedis(ctx context.Context) (*Redis, error) {
	ctr, err := tcredis.Run(ctx, "redis:8-alpine", testcontainers.WithLabels(map[string]string{Label: "1"}))
	if err != nil {
		return nil, fmt.Errorf("start redis container: %w", err)
	}
	r := &Redis{container: ctr}

	host, err := ctr.Host(ctx)
	if err != nil {
		r.Terminate()
		return nil, err
	}
	mapped, err := ctr.MappedPort(ctx, "6379/tcp")
	if err != nil {
		r.Terminate()
		return nil, err
	}
	port, err := strconv.ParseUint(mapped.Port(), 10, 16)
	if err != nil {
		r.Terminate()
		return nil, err
	}
	r.host, r.port = host, uint16(port)
	return r, nil
}

// Terminate stops and removes the container.
func (r *Redis) Terminate() { _ = testcontainers.TerminateContainer(r.container) }

// Client returns a client of the container with an empty database. Flushing is allowed here, and only here,
// because the guard has just checked that the client points at the test container.
func (r *Redis) Client(t *testing.T) *redis.Client {
	t.Helper()
	if err := CheckRedisTarget(r.host, r.port, fmt.Sprintf("%s:%d", r.host, r.port)); err != nil {
		panic(err)
	}
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", r.host, r.port)})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return client
}
