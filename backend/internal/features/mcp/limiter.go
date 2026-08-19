package mcp

import (
	"context"
	"sync"
)

// callLimiter bounds the number of concurrent remote calls process-wide. The
// limit is supplied on every acquire so that setting changes apply at once.
type callLimiter struct {
	mu      sync.Mutex
	active  int
	waiters []chan struct{}
}

// acquire blocks until a slot is free under limit or ctx ends.
func (l *callLimiter) acquire(ctx context.Context, limit int) error {
	if limit < 1 {
		limit = 1
	}
	for {
		l.mu.Lock()
		if l.active < limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		ch := make(chan struct{})
		l.waiters = append(l.waiters, ch)
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			l.mu.Lock()
			removed := false
			for i, w := range l.waiters {
				if w == ch {
					l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
					removed = true
					break
				}
			}
			if !removed {
				// We were already woken: hand the wake-up on.
				l.wakeLocked()
			}
			l.mu.Unlock()
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			l.mu.Lock()
			l.wakeLocked()
			l.mu.Unlock()
			return err
		}
	}
}

func (l *callLimiter) wakeLocked() {
	if len(l.waiters) > 0 {
		close(l.waiters[0])
		l.waiters = l.waiters[1:]
	}
}

func (l *callLimiter) release() {
	l.mu.Lock()
	l.active--
	l.wakeLocked()
	l.mu.Unlock()
}
