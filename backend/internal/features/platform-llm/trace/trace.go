// Package trace is a small, dependency-free tracing layer: W3C Trace Context
// and Baggage propagation, parent-based ratio sampling, span recording and an
// OTLP/HTTP (JSON) batch exporter. A nil *Tracer is valid and does nothing, so
// callers never need to guard.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Attr is one span attribute. Value is a string, bool, int, int64 or float64.
type Attr struct {
	Key   string
	Value any
}

// String builds a string attribute.
func String(k, v string) Attr { return Attr{k, v} }

// Int builds an integer attribute.
func Int(k string, v int64) Attr { return Attr{k, v} }

// Bool builds a boolean attribute.
func Bool(k string, v bool) Attr { return Attr{k, v} }

// Kind classifies a span.
type Kind int

// Span kinds (values match the OTLP enumeration).
const (
	KindInternal Kind = 1
	KindServer   Kind = 2
	KindClient   Kind = 3
)

// SpanContext identifies a span within a trace.
type SpanContext struct {
	TraceID [16]byte
	SpanID  [8]byte
	Sampled bool
	Remote  bool
}

// Valid reports whether both identifiers are non-zero.
func (c SpanContext) Valid() bool {
	return c.TraceID != [16]byte{} && c.SpanID != [8]byte{}
}

// TraceIDString returns the trace id as 32 hex characters.
func (c SpanContext) TraceIDString() string { return hex.EncodeToString(c.TraceID[:]) }

// SpanData is a finished span handed to an Exporter.
type SpanData struct {
	Context  SpanContext
	ParentID [8]byte
	Name     string
	Kind     Kind
	Start    time.Time
	End      time.Time
	Attrs    []Attr
	Failed   bool
	Message  string
}

// Exporter receives finished spans in batches.
type Exporter interface {
	Export(ctx context.Context, spans []*SpanData) error
	Shutdown(ctx context.Context) error
}

// Options builds a Tracer.
type Options struct {
	Exporter     Exporter
	SamplingRate float64 // clamped to [0, 1]
	Now          func() time.Time
	// BatchSize and FlushEvery tune the batch processor (defaults 512, 5 s).
	BatchSize  int
	FlushEvery time.Duration
	// QueueSize bounds buffered spans; extras are dropped (default 2048).
	QueueSize int
}

// Tracer creates spans and forwards sampled ones to its exporter.
type Tracer struct {
	exp   Exporter
	rate  float64
	now   func() time.Time
	queue chan *SpanData
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
	batch int
	every time.Duration
}

// New returns a running Tracer. With a nil Exporter, spans are created (so
// propagation still works) but never exported.
func New(o Options) *Tracer {
	t := &Tracer{exp: o.Exporter, rate: clamp01(o.SamplingRate), now: o.Now}
	if t.now == nil {
		t.now = time.Now
	}
	t.batch = o.BatchSize
	if t.batch <= 0 {
		t.batch = 512
	}
	t.every = o.FlushEvery
	if t.every <= 0 {
		t.every = 5 * time.Second
	}
	q := o.QueueSize
	if q <= 0 {
		q = 2048
	}
	if t.exp != nil {
		t.queue = make(chan *SpanData, q)
		t.stop = make(chan struct{})
		t.done = make(chan struct{})
		go t.loop()
	}
	return t
}

func clamp01(v float64) float64 {
	if v != v || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (t *Tracer) loop() {
	defer close(t.done)
	tick := time.NewTicker(t.every)
	defer tick.Stop()
	var buf []*SpanData
	flush := func() {
		if len(buf) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = t.exp.Export(ctx, buf)
		cancel()
		buf = nil
	}
	for {
		select {
		case s := <-t.queue:
			buf = append(buf, s)
			if len(buf) >= t.batch {
				flush()
			}
		case <-tick.C:
			flush()
		case <-t.stop:
			for {
				select {
				case s := <-t.queue:
					buf = append(buf, s)
				default:
					flush()
					return
				}
			}
		}
	}
}
