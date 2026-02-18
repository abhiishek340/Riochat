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

// Shutdown flushes queued spans and stops the exporter. It waits at most 5
// seconds (or until ctx ends).
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.exp == nil {
		return nil
	}
	var err error
	t.once.Do(func() {
		close(t.stop)
		wait, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		select {
		case <-t.done:
		case <-wait.Done():
		}
		err = t.exp.Shutdown(wait)
	})
	return err
}

// Span is an in-progress operation.
type Span struct {
	t     *Tracer
	data  SpanData
	mu    sync.Mutex
	ended bool
}

type ctxKey int

const (
	spanKey ctxKey = iota
	baggageKey
	noURLKey
)

// FromContext returns the current span context (zero when none).
func FromContext(ctx context.Context) SpanContext {
	if s, ok := ctx.Value(spanKey).(*Span); ok && s != nil {
		return s.data.Context
	}
	if sc, ok := ctx.Value(remoteKey{}).(SpanContext); ok {
		return sc
	}
	return SpanContext{}
}

type remoteKey struct{}

// Start begins a span as a child of the span (or remote context) in ctx. The
// returned context carries the new span. Safe on a nil Tracer.
func (t *Tracer) Start(ctx context.Context, name string, kind Kind, attrs ...Attr) (context.Context, *Span) {
	if t == nil {
		return ctx, nil
	}
	parent := FromContext(ctx)
	sc := SpanContext{}
	if parent.Valid() {
		sc.TraceID = parent.TraceID
		sc.Sampled = parent.Sampled
	} else {
		_, _ = rand.Read(sc.TraceID[:])
		sc.Sampled = t.sample(sc.TraceID)
	}
	_, _ = rand.Read(sc.SpanID[:])
	s := &Span{t: t}
	s.data = SpanData{Context: sc, ParentID: parent.SpanID, Name: name, Kind: kind, Start: t.now(), Attrs: append([]Attr(nil), attrs...)}
	if !parent.Valid() {
		s.data.ParentID = [8]byte{}
	}
	return context.WithValue(ctx, spanKey, s), s
}

func (t *Tracer) sample(id [16]byte) bool {
	switch {
	case t.rate >= 1:
		return true
	case t.rate <= 0:
		return false
	}
	x := binary.BigEndian.Uint64(id[8:]) >> 1
	return float64(x) < t.rate*float64(uint64(1)<<63)
}

// SetAttr adds attributes. Safe on a nil Span.
func (s *Span) SetAttr(attrs ...Attr) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.data.Attrs = append(s.data.Attrs, attrs...)
	s.mu.Unlock()
}

// RecordError marks the span failed with the error text.
func (s *Span) RecordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.data.Failed = true
	s.data.Message = err.Error()
	s.mu.Unlock()
}

// SpanContext returns the span's identifiers.
func (s *Span) SpanContext() SpanContext {
	if s == nil {
		return SpanContext{}
	}
	return s.data.Context
}

// End finishes the span; later calls do nothing.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.data.End = s.t.now()
	d := s.data
	s.mu.Unlock()
	if !d.Context.Sampled || s.t.queue == nil {
		return
	}
	select {
	case s.t.queue <- &d:
	default: // queue full: drop rather than block the caller
	}
}

// ---- propagation ----------------------------------------------------------

// Extract reads traceparent and baggage from h into ctx.
func Extract(ctx context.Context, h http.Header) context.Context {
	if sc, ok := parseTraceparent(h.Get("traceparent")); ok {
		ctx = context.WithValue(ctx, remoteKey{}, sc)
	}
	if b := strings.TrimSpace(h.Get("baggage")); b != "" {
		ctx = context.WithValue(ctx, baggageKey, b)
	}
	return ctx
}

// Inject writes traceparent (when a span is active) and baggage into h.
func Inject(ctx context.Context, h http.Header) {
	if sc := FromContext(ctx); sc.Valid() {
		flags := "00"
		if sc.Sampled {
			flags = "01"
		}
		h.Set("traceparent", "00-"+hex.EncodeToString(sc.TraceID[:])+"-"+hex.EncodeToString(sc.SpanID[:])+"-"+flags)
	}
	if b, ok := ctx.Value(baggageKey).(string); ok && b != "" {
		h.Set("baggage", b)
	}
}

func parseTraceparent(v string) (SpanContext, bool) {
	parts := strings.Split(strings.TrimSpace(v), "-")
	if len(parts) < 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return SpanContext{}, false
	}
	var sc SpanContext
	if _, err := hex.Decode(sc.TraceID[:], []byte(parts[1])); err != nil {
		return SpanContext{}, false
	}
	if _, err := hex.Decode(sc.SpanID[:], []byte(parts[2])); err != nil {
		return SpanContext{}, false
	}
	fl, err := hex.DecodeString(parts[3])
	if err != nil || !sc.Valid() {
		return SpanContext{}, false
	}
	sc.Sampled = fl[0]&1 == 1
	sc.Remote = true
	return sc, true
}

// WithoutURL marks ctx so HTTP transports omit the request URL from spans
// (used for signed media-artifact downloads).
func WithoutURL(ctx context.Context) context.Context { return context.WithValue(ctx, noURLKey, true) }

func urlSuppressed(ctx context.Context) bool {
	v, _ := ctx.Value(noURLKey).(bool)
	return v
}
