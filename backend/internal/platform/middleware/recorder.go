// Package middleware holds the HTTP middleware shared by every route: panic
// recovery, trusted-proxy and client IP resolution, request/trace identifiers,
// access logging, security headers, CORS, compression, authentication,
// rate limiting and feature gates.
package middleware

import (
	"context"
	"sync/atomic"
	"time"
)

// ErrorRecord is what the access logger and the panic handler hand to an
// ErrorRecorder. Text fields are already redacted and truncated.
type ErrorRecord struct {
	RequestID string
	TraceID   string
	UserID    uint
	Method    string
	Route     string // matched route template, "-" when unmatched
	Path      string // request path with a redacted query
	Status    int
	ErrorCode string
	Cause     string
	Detail    string // diagnostic detail (upstream body etc.)
	LatencyMS int64
	ClientIP  string
	UserAgent string
	At        time.Time
}

// ErrorRecorder persists server-side failures for administrators. The error
// log feature implements it; it is absent in the desktop sidecar and in tests.
// Implementations should return quickly (hand work off if it is slow) and must
// not panic.
type ErrorRecorder interface {
	RecordError(ctx context.Context, rec ErrorRecord)
}

// ErrorRecorderFunc adapts a function to ErrorRecorder.
type ErrorRecorderFunc func(ctx context.Context, rec ErrorRecord)

// RecordError implements ErrorRecorder.
func (f ErrorRecorderFunc) RecordError(ctx context.Context, rec ErrorRecord) { f(ctx, rec) }

// RecorderHook lets the error-log feature register its ErrorRecorder after the
// router has been built. Until one is registered, records are dropped.
type RecorderHook struct{ v atomic.Pointer[ErrorRecorder] }

// Set registers the recorder (last writer wins).
func (h *RecorderHook) Set(r ErrorRecorder) { h.v.Store(&r) }

// IsSet reports whether a recorder is registered.
func (h *RecorderHook) IsSet() bool { return h != nil && h.v.Load() != nil }

// RecordError implements ErrorRecorder by forwarding to the registered one.
func (h *RecorderHook) RecordError(ctx context.Context, rec ErrorRecord) {
	if h == nil {
		return
	}
	if p := h.v.Load(); p != nil && *p != nil {
		(*p).RecordError(ctx, rec)
	}
}
