package config

import "sync/atomic"

// Holder publishes configuration snapshots. Readers always see a complete,
// consistent Config; writers replace it wholesale, and a snapshot already
// handed out never changes.
type Holder struct {
	p atomic.Pointer[Config]
}

// NewHolder returns a holder initialized with c (cloned).
func NewHolder(c *Config) *Holder {
	h := &Holder{}
	h.Store(c)
	return h
}

// Load returns the current snapshot. On a nil or empty holder it returns an
// empty configuration. Callers must not mutate the result; use Clone.
func (h *Holder) Load() *Config {
	if h == nil {
		return &Config{}
	}
	if c := h.p.Load(); c != nil {
		return c
	}
	return &Config{}
}

// Store replaces the snapshot with a normalized private copy of c.
func (h *Holder) Store(c *Config) {
	if c == nil {
		return
	}
	h.p.Store(c.Clone().Normalize())
}

// Update applies fn to a clone of the current snapshot and publishes the
// result. Concurrent Updates are serialized by retrying on conflict.
func (h *Holder) Update(fn func(*Config)) {
	for {
		old := h.p.Load()
		next := old.Clone()
		fn(next)
		next.Normalize()
		if h.p.CompareAndSwap(old, next) {
			return
		}
	}
}
