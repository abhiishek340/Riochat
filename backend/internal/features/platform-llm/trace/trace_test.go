package trace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu    sync.Mutex
	spans []*SpanData
	shut  bool
}

func (s *sink) Export(_ context.Context, sp []*SpanData) error {
	s.mu.Lock()
	s.spans = append(s.spans, sp...)
	s.mu.Unlock()
	return nil
}
func (s *sink) Shutdown(context.Context) error { s.mu.Lock(); s.shut = true; s.mu.Unlock(); return nil }
func (s *sink) got() []*SpanData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*SpanData(nil), s.spans...)
}

func newTracer(rate float64) (*Tracer, *sink) {
	s := &sink{}
	return New(Options{Exporter: s, SamplingRate: rate, FlushEvery: time.Hour}), s
}

func TestNilTracerIsSafe(t *testing.T) {
	var tr *Tracer
	ctx, sp := tr.Start(context.Background(), "x", KindInternal)
	sp.SetAttr(String("a", "b"))
	sp.RecordError(io.EOF)
	sp.End()
	if ctx == nil || sp.SpanContext().Valid() {
		t.Fatal("no-op span")
	}
	if tr.Shutdown(context.Background()) != nil {
		t.Fatal("shutdown")
	}
	rt := Transport(http.DefaultTransport, nil)
	if rt != http.DefaultTransport {
		t.Fatal("a nil tracer leaves the transport untouched")
	}
}

func TestSpanParentingAndExport(t *testing.T) {
	tr, s := newTracer(1)
	ctx, parent := tr.Start(context.Background(), "parent", KindServer, String("k", "v"))
	_, child := tr.Start(ctx, "child", KindClient)
	child.RecordError(io.ErrUnexpectedEOF)
	child.End()
	child.End() // idempotent
	parent.SetAttr(Int("n", 3), Bool("ok", true))
	parent.End()
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.got()
	if len(got) != 2 || !s.shut {
		t.Fatalf("%d spans, shut=%v", len(got), s.shut)
	}
	byName := map[string]*SpanData{got[0].Name: got[0], got[1].Name: got[1]}
	c, p := byName["child"], byName["parent"]
	if c.Context.TraceID != p.Context.TraceID || c.ParentID != p.Context.SpanID || p.ParentID != [8]byte{} {
		t.Fatal("parent/child linkage")
	}
	if !c.Failed || c.Message != io.ErrUnexpectedEOF.Error() || len(p.Attrs) != 3 || p.Kind != KindServer || c.End.Before(c.Start) {
		t.Fatalf("%+v %+v", c, p)
	}
}

func TestSamplingRates(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		min  int
		max  int
	}{{0, 0, 0}, {1, 400, 400}, {-3, 0, 0}, {7, 400, 400}, {0.5, 140, 260}} {
		tr, s := newTracer(tc.rate)
		for i := 0; i < 400; i++ {
			_, sp := tr.Start(context.Background(), "x", KindInternal)
			sp.End()
		}
		tr.Shutdown(context.Background())
		if n := len(s.got()); n < tc.min || n > tc.max {
			t.Errorf("rate %v exported %d of 400", tc.rate, n)
		}
	}
}

func TestParentBasedSampling(t *testing.T) {
	tr, s := newTracer(0) // would never sample a root
	h := http.Header{}
	h.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	ctx := Extract(context.Background(), h)
	_, sp := tr.Start(ctx, "child", KindServer)
	sp.End()
	tr.Shutdown(context.Background())
	got := s.got()
	if len(got) != 1 || got[0].Context.TraceIDString() != "0af7651916cd43dd8448eb211c80319c" || got[0].ParentID[0] != 0xb7 {
		t.Fatalf("a sampled remote parent wins: %+v", got)
	}
	// Unsampled parent: not exported even at rate 1.
	tr2, s2 := newTracer(1)
	h.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00")
	_, sp = tr2.Start(Extract(context.Background(), h), "child", KindServer)
	sp.End()
	tr2.Shutdown(context.Background())
	if len(s2.got()) != 0 {
		t.Fatal("unsampled parents are respected")
	}
}

func TestPropagationHeaders(t *testing.T) {
	tr, _ := newTracer(1)
	h := http.Header{}
	h.Set("baggage", "user=abc,tenant=7")
	ctx := Extract(context.Background(), h)
	ctx, sp := tr.Start(ctx, "x", KindClient)
	spanID := sp.SpanContext().SpanID
	out := http.Header{}
	Inject(ctx, out)
	tp := out.Get("traceparent")
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || parts[3] != "01" || parts[2] != hexOf(spanID[:]) {
		t.Fatalf("traceparent %q", tp)
	}
	if out.Get("baggage") != "user=abc,tenant=7" {
		t.Fatal("baggage is forwarded")
	}
	for _, bad := range []string{"", "garbage", "01-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "00-00000000000000000000000000000000-b7ad6b7169203331-01", "00-zz-b7ad6b7169203331-01"} {
		hh := http.Header{}
		hh.Set("traceparent", bad)
		if FromContext(Extract(context.Background(), hh)).Valid() {
			t.Errorf("%q should be rejected", bad)
		}
	}
	empty := http.Header{}
	Inject(context.Background(), empty)
	if len(empty) != 0 {
		t.Fatal("nothing to inject without a span")
	}
}

func hexOf(b []byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, d[c>>4], d[c&15])
	}
	return string(out)
}
