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

func TestHTTPTransportSpans(t *testing.T) {
	var gotTP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTP = r.Header.Get("traceparent")
		if r.URL.Path == "/fail" {
			w.WriteHeader(502)
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	tr, s := newTracer(1)
	client := &http.Client{Transport: Transport(nil, tr)}
	do := func(path string, ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path+"?secret=SIG", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	do("/a", context.Background())
	do("/fail", context.Background())
	do("/hidden", WithoutURL(context.Background()))
	tr.Shutdown(context.Background())
	if gotTP == "" {
		t.Fatal("outgoing requests carry traceparent")
	}
	spans := s.got()
	if len(spans) != 3 {
		t.Fatalf("%d spans", len(spans))
	}
	attrs := func(sp *SpanData) map[string]any {
		m := map[string]any{}
		for _, a := range sp.Attrs {
			m[a.Key] = a.Value
		}
		return m
	}
	a0, a1, a2 := attrs(spans[0]), attrs(spans[1]), attrs(spans[2])
	if a0["http.request.method"] != "GET" || a0["http.response.status_code"] != "200" || !strings.HasSuffix(a0["url.full"].(string), "/a") || strings.Contains(a0["url.full"].(string), "SIG") {
		t.Fatalf("%v", a0)
	}
	if !spans[1].Failed || a1["http.response.status_code"] != "502" {
		t.Fatalf("5xx marks the span failed: %+v", spans[1])
	}
	if _, ok := a2["url.full"]; ok {
		t.Fatal("artifact downloads do not record URLs")
	}
}

func TestParseSettings(t *testing.T) {
	tru, fls := true, false
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	s, err := ParseSettings(nil, "", "", false, "grpc", 1, "", env(nil))
	if err != nil || s.Enabled || s.ServiceName != "riochat" {
		t.Fatalf("%v %+v", err, s)
	}
	s, err = ParseSettings(nil, "collector:4318", "a=1, b = 2,,bad,=x", true, "http/protobuf", 0.25, "MyApp", env(map[string]string{
		"KUBERNETES_NAMESPACE": "ns", "KUBERNETES_POD_NAME": "pod", "KUBERNETES_POD_UID": "uid"}))
	if err != nil || !s.Enabled || s.Endpoint != "http://collector:4318/v1/traces" || s.Protocol != "http" || s.Headers["a"] != "1" || s.Headers["b"] != "2" || len(s.Headers) != 2 {
		t.Fatalf("%v %+v", err, s)
	}
	if s.Resource["service.name"] != "MyApp" || s.Resource["k8s.namespace.name"] != "ns" || s.Resource["k8s.pod.name"] != "pod" || s.Resource["k8s.pod.uid"] != "uid" || s.SamplingRate != 0.25 {
		t.Fatalf("%v", s.Resource)
	}
	s, _ = ParseSettings(nil, "https://otel.example/custom/path", "", false, "", 9, "x", env(nil))
	if s.Endpoint != "https://otel.example/custom/path" || s.Protocol != "grpc" || s.SamplingRate != 1 {
		t.Fatalf("%+v", s)
	}
	if _, err := ParseSettings(nil, "https://otel.example", "", false, "weird", 1, "x", env(nil)); err == nil || err.Error() != "unsupported otel exporter protocol" {
		t.Fatalf("unknown protocol must fail: %v", err)
	}
	if _, err := ParseSettings(nil, "", "", false, "weird", 1, "x", env(nil)); err != nil {
		t.Fatalf("a disabled tracer ignores the protocol: %v", err)
	}
	s, _ = ParseSettings(nil, "otel.example", "", false, "http", 1, "x", env(nil))
	if s.Endpoint != "https://otel.example/v1/traces" {
		t.Fatalf("secure by default: %s", s.Endpoint)
	}
	if _, err := ParseSettings(&tru, "", "", false, "grpc", 1, "x", env(nil)); err == nil || err.Error() != "otel exporter endpoint is required when tracing is enabled" {
		t.Fatalf("%v", err)
	}
	s, err = ParseSettings(&fls, "collector:4317", "", false, "grpc", 1, "x", env(nil))
	if err != nil || s.Enabled {
		t.Fatal("an explicit false disables tracing even with an endpoint")
	}
	if _, err := ParseSettings(&tru, "http://", "", false, "http", 1, "x", env(nil)); err == nil {
		t.Fatal("invalid endpoint")
	}
}

func TestOTLPExport(t *testing.T) {
	var mu sync.Mutex
	var body []byte
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, _ = io.ReadAll(r.Body)
		hdr = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	st, err := ParseSettings(nil, srv.URL, "x-token=abc", true, "http", 1, "svc", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	tr := NewFromSettings(st, nil)
	ctx, parent := tr.Start(context.Background(), "root", KindServer, String("route", "/x"), Int("n", 2), Bool("b", true))
	_, child := tr.Start(ctx, "kid", KindClient)
	child.RecordError(io.EOF)
	child.End()
	parent.End()
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hdr.Get("x-token") != "abc" || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("%v", hdr)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	rs := doc["resourceSpans"].([]any)[0].(map[string]any)
	spans := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	if len(spans) != 2 {
		t.Fatalf("%d", len(spans))
	}
	var kid map[string]any
	for _, sp := range spans {
		if sp.(map[string]any)["name"] == "kid" {
			kid = sp.(map[string]any)
		}
	}
	if len(kid["traceId"].(string)) != 32 || len(kid["spanId"].(string)) != 16 || kid["parentSpanId"] == nil || kid["kind"] != float64(3) {
		t.Fatalf("%v", kid)
	}
	if kid["status"].(map[string]any)["code"] != float64(2) {
		t.Fatal("errors map to status code 2")
	}
	if !strings.Contains(string(body), `"service.name"`) || !strings.Contains(string(body), `"stringValue":"svc"`) || !strings.Contains(string(body), `"intValue":"2"`) {
		t.Fatalf("resource and attribute encoding: %s", body)
	}
}

func TestExporterFailureNeverBreaksCallers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	st, _ := ParseSettings(nil, srv.URL, "", true, "http", 1, "x", func(string) string { return "" })
	tr := NewFromSettings(st, nil)
	_, sp := tr.Start(context.Background(), "x", KindInternal)
	sp.End()
	done := make(chan error, 1)
	go func() { done <- tr.Shutdown(context.Background()) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("shutdown is bounded")
	}
}

func TestQueueOverflowDropsInsteadOfBlocking(t *testing.T) {
	block := make(chan struct{})
	exp := &blockingExporter{block: block}
	tr := New(Options{Exporter: exp, SamplingRate: 1, QueueSize: 2, BatchSize: 1, FlushEvery: time.Hour})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, sp := tr.Start(context.Background(), "x", KindInternal)
			sp.End()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("span producers must never block on a slow exporter")
	}
	close(block)
	tr.Shutdown(context.Background())
}

type blockingExporter struct{ block chan struct{} }

func (b *blockingExporter) Export(ctx context.Context, _ []*SpanData) error {
	select {
	case <-b.block:
	case <-ctx.Done():
	}
	return nil
}
func (b *blockingExporter) Shutdown(context.Context) error { return nil }
