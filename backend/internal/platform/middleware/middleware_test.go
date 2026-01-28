package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

func init() { gin.SetMode(gin.TestMode) }

type memRecorder struct {
	mu   sync.Mutex
	recs []ErrorRecord
}

func (m *memRecorder) RecordError(_ context.Context, r ErrorRecord) {
	m.mu.Lock()
	m.recs = append(m.recs, r)
	m.mu.Unlock()
}

func (m *memRecorder) all() []ErrorRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ErrorRecord(nil), m.recs...)
}

type harness struct {
	engine *gin.Engine
	logs   *bytes.Buffer
	rec    *memRecorder
	cfg    *config.Holder
	clock  *clock.Fake
	tokens *token.Manager
}

func newHarness(t *testing.T, trusted []string, mutate func(*config.Config)) *harness {
	t.Helper()
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	if mutate != nil {
		mutate(&cfg)
	}
	h := &harness{
		logs:  &bytes.Buffer{},
		rec:   &memRecorder{},
		cfg:   config.NewHolder(&cfg),
		clock: clock.NewFake(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)),
	}
	h.tokens = token.NewManager(func() string { return "unit-test-secret-0123456789" }, h.clock)
	set, err := NewProxySet(trusted)
	if err != nil {
		t.Fatal(err)
	}
	log := logx.New(h.logs, logx.LevelDebug)
	e := gin.New()
	e.SetTrustedProxies(nil)
	e.Use(Recovery(log, h.rec, h.clock), TrustedProxy(set), RequestID(),
		AccessLog(AccessLogOptions{Logger: log, Clock: h.clock, Recorder: h.rec}),
		SecurityHeaders(), CORS(h.cfg), Gzip())
	h.engine = e
	return h
}

func (h *harness) do(method, target string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "192.0.2.10:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	return w
}

func env(t *testing.T, w *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var e httpx.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, w.Body.String())
	}
	return e
}

func ok(c *gin.Context) { httpx.OK(c, "ok") }

func TestRequestIDTrustAndValidation(t *testing.T) {
	h := newHarness(t, []string{"192.0.2.0/24"}, nil)
	h.engine.GET("/api/v1/x", ok)

	w := h.do("GET", "/api/v1/x", map[string]string{"X-Request-ID": "abc.DEF_1:2-3"}, nil)
	if w.Header().Get("X-Request-ID") != "abc.DEF_1:2-3" {
		t.Fatalf("trusted peer + valid id must be echoed, got %q", w.Header().Get("X-Request-ID"))
	}
	for _, bad := range []string{"has space", strings.Repeat("a", 65), "semi;colon"} {
		w = h.do("GET", "/api/v1/x", map[string]string{"X-Request-ID": bad}, nil)
		got := w.Header().Get("X-Request-ID")
		if got == bad || len(got) != 36 {
			t.Fatalf("invalid id %q must be replaced by a UUID, got %q", bad, got)
		}
	}
	if len(w.Header().Get("X-Trace-ID")) != 32 {
		t.Fatalf("trace id: %q", w.Header().Get("X-Trace-ID"))
	}

	untrusted := newHarness(t, nil, nil)
	untrusted.engine.GET("/api/v1/x", ok)
	w = untrusted.do("GET", "/api/v1/x", map[string]string{"X-Request-ID": "abc"}, nil)
	if w.Header().Get("X-Request-ID") == "abc" {
		t.Fatal("untrusted peer must not control the request id")
	}
}

func TestTraceIDInbound(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.engine.GET("/api/v1/x", ok)
	good := "0AF7651916CD43DD8448EB211C80319C"
	w := h.do("GET", "/api/v1/x", map[string]string{"X-Trace-ID": good}, nil)
	if w.Header().Get("X-Trace-ID") != strings.ToLower(good) {
		t.Fatalf("valid inbound trace id lowercased: %q", w.Header().Get("X-Trace-ID"))
	}
	for _, bad := range []string{strings.Repeat("0", 32), "short", strings.Repeat("g", 32)} {
		w = h.do("GET", "/api/v1/x", map[string]string{"X-Trace-ID": bad}, nil)
		if got := w.Header().Get("X-Trace-ID"); got == bad || len(got) != 32 || got == strings.Repeat("0", 32) {
			t.Fatalf("bad trace id %q -> %q", bad, got)
		}
	}
}

func TestClientIPResolution(t *testing.T) {
	var got string
	h := newHarness(t, []string{"192.0.2.0/24", "10.0.0.0/8"}, nil)
	h.engine.GET("/api/v1/ip", func(c *gin.Context) { got = ClientIP(c); httpx.OK(c, got) })
	h.do("GET", "/api/v1/ip", map[string]string{"X-Forwarded-For": "198.51.100.7, 10.1.1.1"}, nil)
	if got != "198.51.100.7" {
		t.Fatalf("right-most non-trusted address expected, got %q", got)
	}
	h.do("GET", "/api/v1/ip", map[string]string{"X-Real-IP": "203.0.113.9"}, nil)
	if got != "203.0.113.9" {
		t.Fatalf("X-Real-IP fallback: %q", got)
	}
	h.do("GET", "/api/v1/ip", nil, nil)
	if got != "192.0.2.10" {
		t.Fatalf("no headers -> peer: %q", got)
	}
	plain := newHarness(t, nil, nil)
	plain.engine.GET("/api/v1/ip", func(c *gin.Context) { got = ClientIP(c); httpx.OK(c, got) })
	plain.do("GET", "/api/v1/ip", map[string]string{"X-Forwarded-For": "198.51.100.7"}, nil)
	if got != "192.0.2.10" {
		t.Fatalf("untrusted peer's forwarding headers must be ignored: %q", got)
	}
	if _, err := NewProxySet([]string{"nonsense"}); err == nil {
		t.Fatal("bad proxy entry must fail")
	}
}

func TestSecurityHeadersDoNotOverride(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.engine.GET("/api/v1/a", ok)
	h.engine.GET("/api/v1/b", func(c *gin.Context) { c.Header("X-Frame-Options", "SAMEORIGIN"); httpx.OK(c, 1) })
	w := h.do("GET", "/api/v1/a", nil, nil)
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(w.Header().Get("Permissions-Policy"), "camera=()") {
		t.Fatalf("headers: %v", w.Header())
	}
	if h.do("GET", "/api/v1/b", nil, nil).Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("handler-set header must win")
	}
}

func TestCORS(t *testing.T) {
	h := newHarness(t, nil, func(c *config.Config) {
		c.Server.CORSAllowOrigin = "https://app.example.com/ , https://Other.example.com"
	})
	h.engine.GET("/api/v1/x", ok)

	w := h.do("GET", "/api/v1/x", map[string]string{"Origin": "https://evil.example"}, nil)
	if w.Code != 403 || env(t, w).ErrorCode != "cors.origin_forbidden" {
		t.Fatalf("evil origin: %d %s", w.Code, w.Body)
	}
	w = h.do("OPTIONS", "/api/v1/x", map[string]string{"Origin": "https://app.example.com"}, nil)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Access-Control-Max-Age") != "86400" ||
		w.Header().Get("Access-Control-Allow-Methods") != "GET,POST,PUT,PATCH,DELETE,OPTIONS" ||
		w.Header().Get("Access-Control-Allow-Headers") != "Authorization,Content-Type,X-Request-ID,X-Client-Platform" ||
		w.Header().Get("Access-Control-Expose-Headers") != "X-Request-ID" ||
		w.Header().Get("Vary") != "Origin" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	if h.do("GET", "/api/v1/x", map[string]string{"Origin": "https://other.example.com"}, nil).Code != 200 {
		t.Fatal("origin comparison must be case-insensitive")
	}
	if h.do("GET", "/api/v1/x", map[string]string{"Origin": "tauri://localhost"}, nil).Code != 200 ||
		h.do("GET", "/api/v1/x", map[string]string{"Origin": "http://tauri.localhost"}, nil).Code != 200 {
		t.Fatal("desktop origins are always allowed")
	}
	if h.do("GET", "/api/v1/x", nil, nil).Code != 200 {
		t.Fatal("requests without Origin pass")
	}

	star := newHarness(t, nil, func(c *config.Config) { c.Server.CORSAllowOrigin = "" })
	star.engine.GET("/api/v1/x", ok)
	w = star.do("GET", "/api/v1/x", map[string]string{"Origin": "https://anything.example"}, nil)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://anything.example" {
		t.Fatalf("empty list means *: %d %v", w.Code, w.Header())
	}
}
