// Package cache defines the cache contract shared by every feature area and
// two interchangeable backends, an in-process memory cache and Redis.
//
// Both backends are behaviorally equivalent. Callers treat cache errors as
// non-fatal wherever correctness does not depend on the cache (for example
// rate limiting fails open). Operations whose correctness depends on atomicity
// (SetNX, GetDel, Incr and the rate limiters) are atomic in both backends.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// DefaultTTL is applied when a caller passes a non-positive TTL.
const DefaultTTL = time.Minute

// ErrClosed is returned by operations on a closed cache.
var ErrClosed = errors.New("cache: closed")

// KV is a byte-oriented key/value store with expiry.
type KV interface {
	// Get returns the value and whether the key exists.
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	// Set stores value for ttl (DefaultTTL when ttl <= 0).
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetNX stores value only if the key is absent and reports whether it did.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// GetDel atomically reads and deletes a key. It is the primitive for
	// one-time consumption of short-lived records.
	GetDel(ctx context.Context, key string) (value []byte, found bool, err error)
	// Delete removes keys; missing keys are ignored.
	Delete(ctx context.Context, keys ...string) error
	// Incr atomically increments an integer counter and returns the new value.
	// The TTL (DefaultTTL when ttl <= 0) is applied when the counter is created.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
}

// RateLimiter provides the two windowing primitives used by the HTTP rate
// limiter. Storage errors are reported to the caller, which should allow the
// request (fail open).
type RateLimiter interface {
	// AllowSliding reports whether a new event fits in the sliding window.
	// A non-positive limit or an empty key always allows. Window defaults to one
	// minute and ttl to twice the window. Only allowed events are recorded.
	AllowSliding(ctx context.Context, key string, limit int, window, ttl time.Duration) (bool, error)
	// AllowFixed increments a counter per key and denies if any counter exceeds
	// limit. An empty key list or non-positive limit always allows; ttl defaults
	// to one minute. The window starts at the first hit.
	AllowFixed(ctx context.Context, keys []string, limit int, ttl time.Duration) (bool, error)
}

// Cache is the full backend contract.
type Cache interface {
	KV
	RateLimiter
	// Name is "redis" or "memory" (reported by health checks).
	Name() string
	Ping(ctx context.Context) error
	Close() error
}

// GetJSON reads and decodes a JSON value. A miss or an undecodable value is
// reported as found=false.
func GetJSON[T any](ctx context.Context, c KV, key string) (T, bool, error) {
	var zero T
	raw, found, err := c.Get(ctx, key)
	if err != nil || !found {
		return zero, false, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, false, nil
	}
	return v, true, nil
}

// SetJSON encodes v as JSON and stores it.
func SetJSON(ctx context.Context, c KV, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, raw, ttl)
}

// ConsumeJSON atomically reads, deletes and decodes a JSON value.
func ConsumeJSON[T any](ctx context.Context, c KV, key string) (T, bool, error) {
	var zero T
	raw, found, err := c.GetDel(ctx, key)
	if err != nil || !found {
		return zero, false, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, false, nil
	}
	return v, true, nil
}

func ttlOrDefault(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultTTL
	}
	return ttl
}
