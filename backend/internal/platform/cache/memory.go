package cache

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/clock"
)

// sweepEvery controls opportunistic housekeeping: expired entries are removed
// on every Nth operation, so the cache needs no timer goroutine.
const sweepEvery = 256

type memEntry struct {
	value   []byte
	expires time.Time
}

type memCounter struct {
	count   int64
	expires time.Time
}

type memWindow struct {
	events  []time.Time
	expires time.Time
}

// Memory is the in-process backend. It is safe for concurrent use. Nothing
// persists across restarts.
type Memory struct {
	clock clock.Clock

	mu      sync.Mutex
	kv      map[string]memEntry
	fixed   map[string]memCounter
	sliding map[string]*memWindow
	ops     uint64
	closed  bool
}

// NewMemory builds a memory cache. clk may be nil (system clock).
func NewMemory(clk clock.Clock) *Memory {
	if clk == nil {
		clk = clock.System{}
	}
	return &Memory{
		clock:   clk,
		kv:      map[string]memEntry{},
		fixed:   map[string]memCounter{},
		sliding: map[string]*memWindow{},
	}
}

// Name implements Cache.
func (m *Memory) Name() string { return "memory" }

// Ping implements Cache.
func (m *Memory) Ping(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

// Close implements Cache.
func (m *Memory) Close() error {
	m.mu.Lock()
	m.closed = true
	m.kv, m.fixed, m.sliding = map[string]memEntry{}, map[string]memCounter{}, map[string]*memWindow{}
	m.mu.Unlock()
	return nil
}

// begin acquires the lock, counts the operation and sweeps when due. The caller
// must unlock. It returns the current time.
func (m *Memory) begin() (time.Time, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return time.Time{}, ErrClosed
	}
	now := m.clock.Now()
	m.ops++
	if m.ops%sweepEvery == 0 {
		m.sweepLocked(now)
	}
	return now, nil
}

func (m *Memory) sweepLocked(now time.Time) {
	for k, e := range m.kv {
		if !e.expires.After(now) {
			delete(m.kv, k)
		}
	}
	for k, c := range m.fixed {
		if !c.expires.After(now) {
			delete(m.fixed, k)
		}
	}
	for k, w := range m.sliding {
		if !w.expires.After(now) {
			delete(m.sliding, k)
		}
	}
}

func (m *Memory) liveLocked(key string, now time.Time) (memEntry, bool) {
	e, ok := m.kv[key]
	if !ok {
		return memEntry{}, false
	}
	if !e.expires.After(now) {
		delete(m.kv, key)
		return memEntry{}, false
	}
	return e, true
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// Get implements KV.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	now, err := m.begin()
	if err != nil {
		return nil, false, err
	}
	defer m.mu.Unlock()
	e, ok := m.liveLocked(key, now)
	if !ok {
		return nil, false, nil
	}
	return clone(e.value), true, nil
}

// Set implements KV.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	now, err := m.begin()
	if err != nil {
		return err
	}
	defer m.mu.Unlock()
	m.kv[key] = memEntry{value: clone(value), expires: now.Add(ttlOrDefault(ttl))}
	return nil
}

// SetNX implements KV.
func (m *Memory) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	now, err := m.begin()
	if err != nil {
		return false, err
	}
	defer m.mu.Unlock()
	if _, ok := m.liveLocked(key, now); ok {
		return false, nil
	}
	m.kv[key] = memEntry{value: clone(value), expires: now.Add(ttlOrDefault(ttl))}
	return true, nil
}

// GetDel implements KV.
func (m *Memory) GetDel(_ context.Context, key string) ([]byte, bool, error) {
	now, err := m.begin()
	if err != nil {
		return nil, false, err
	}
	defer m.mu.Unlock()
	e, ok := m.liveLocked(key, now)
	if !ok {
		return nil, false, nil
	}
	delete(m.kv, key)
	return clone(e.value), true, nil
}

// Delete implements KV.
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	if _, err := m.begin(); err != nil {
		return err
	}
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.kv, k)
	}
	return nil
}

// Incr implements KV.
func (m *Memory) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	now, err := m.begin()
	if err != nil {
		return 0, err
	}
	defer m.mu.Unlock()
	e, ok := m.liveLocked(key, now)
	var n int64
	if ok {
		n, _ = strconv.ParseInt(string(e.value), 10, 64)
	} else {
		e.expires = now.Add(ttlOrDefault(ttl))
	}
	n++
	e.value = []byte(strconv.FormatInt(n, 10))
	m.kv[key] = e
	return n, nil
}

// AllowSliding implements RateLimiter.
func (m *Memory) AllowSliding(_ context.Context, key string, limit int, window, ttl time.Duration) (bool, error) {
	if key == "" || limit <= 0 {
		return true, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	if ttl <= 0 {
		ttl = 2 * window
	}
	now, err := m.begin()
	if err != nil {
		return false, err
	}
	defer m.mu.Unlock()
	w := m.sliding[key]
	if w == nil || !w.expires.After(now) {
		w = &memWindow{}
		m.sliding[key] = w
	}
	cutoff := now.Add(-window)
	kept := w.events[:0]
	for _, t := range w.events {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	w.events = kept
	if len(w.events) >= limit {
		return false, nil
	}
	w.events = append(w.events, now)
	w.expires = now.Add(ttl)
	return true, nil
}

// AllowFixed implements RateLimiter.
func (m *Memory) AllowFixed(_ context.Context, keys []string, limit int, ttl time.Duration) (bool, error) {
	if len(keys) == 0 || limit <= 0 {
		return true, nil
	}
	ttl = ttlOrDefault(ttl)
	now, err := m.begin()
	if err != nil {
		return false, err
	}
	defer m.mu.Unlock()
	allowed := true
	for _, k := range keys {
		c := m.fixed[k]
		if !c.expires.After(now) {
			c = memCounter{expires: now.Add(ttl)}
		}
		c.count++
		m.fixed[k] = c
		if c.count > int64(limit) {
			allowed = false
		}
	}
	return allowed, nil
}
