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
