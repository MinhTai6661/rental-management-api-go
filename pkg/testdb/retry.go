//go:build integration

package testdb

import (
	"context"
	"time"
)

// startAttempts is how many times a container is started before the package gives up. The Docker daemon
// sometimes answers "No such container" right after creating one (a race with its own cleanup), which says
// nothing about the code under test; a second try nearly always works.
const startAttempts = 3

// retryStart runs start until it succeeds, the attempts are used up or ctx ends, pausing a little between tries.
func retryStart[T any](ctx context.Context, start func(context.Context) (T, error)) (T, error) {
	var (
		out T
		err error
	)
	for attempt := 1; attempt <= startAttempts; attempt++ {
		if out, err = start(ctx); err == nil {
			return out, nil
		}
		if attempt == startAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return out, err
}
