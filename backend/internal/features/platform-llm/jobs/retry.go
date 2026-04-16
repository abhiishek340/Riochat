package jobs

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// RetryPolicy describes exponential backoff.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries (minimum 1).
	MaxAttempts int
	// Base is the delay after the first failure; Multiplier (default 2)
	// grows it; Max caps it.
	Base       time.Duration
	Multiplier float64
	Max        time.Duration
	// Jitter in [0,1] randomly shortens a delay by up to that fraction.
	Jitter float64
}

// Delay returns the wait after the n-th failure (n starts at 1).
func (p RetryPolicy) Delay(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	m := p.Multiplier
	if m <= 1 {
		m = 2
	}
	d := float64(p.Base) * math.Pow(m, float64(n-1))
	if p.Max > 0 && d > float64(p.Max) {
		d = float64(p.Max)
	}
	if p.Jitter > 0 {
		j := p.Jitter
		if j > 1 {
			j = 1
		}
		d -= d * j * rand.Float64()
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// RetryOptions customise Retry.
type RetryOptions struct {
	// Retryable decides whether an error may be retried (default: all but
	// context errors).
	Retryable func(error) bool
	// Sleep waits between attempts and must honor ctx (default: a timer).
	Sleep func(ctx context.Context, d time.Duration) error
	// After may raise the computed delay (for example a Retry-After hint).
	After func(err error) time.Duration
}

// ErrGaveUp wraps the last error when attempts are exhausted.
type ErrGaveUp struct {
	Attempts int
	Err      error
}

func (e *ErrGaveUp) Error() string { return e.Err.Error() }

// Unwrap returns the last error.
func (e *ErrGaveUp) Unwrap() error { return e.Err }

// Retry runs fn up to MaxAttempts times with backoff. A non-retryable error
// or a canceled context returns immediately. After the final failure the last
// error is returned wrapped in *ErrGaveUp.
func Retry(ctx context.Context, p RetryPolicy, o RetryOptions, fn func(attempt int) error) error {
	max := p.MaxAttempts
	if max < 1 {
		max = 1
	}
	retryable := o.Retryable
	if retryable == nil {
		retryable = func(err error) bool {
			return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		}
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	var last error
	for attempt := 1; attempt <= max; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = fn(attempt)
		if last == nil {
			return nil
		}
		if !retryable(last) {
			return last
		}
		if attempt == max {
			break
		}
		d := p.Delay(attempt)
		if o.After != nil {
			if hint := o.After(last); hint > d {
				d = hint
			}
		}
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}
	return &ErrGaveUp{Attempts: max, Err: last}
}
