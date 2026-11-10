package netx

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Retry runs fn until it succeeds, the context is cancelled, or attempts are
// exhausted. retryIf decides whether an error is transient.
func Retry(ctx context.Context, attempts int, fn func(context.Context) error, retryIf func(error) bool) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == attempts || (retryIf != nil && !retryIf(err)) {
			return err
		}
		delay := time.Second * time.Duration(1<<(attempt-1))
		if delay > 8*time.Second {
			delay = 8 * time.Second
		}
		delay = time.Duration(float64(delay) * (0.5 + rand.Float64()*0.5))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
	return lastErr
}
