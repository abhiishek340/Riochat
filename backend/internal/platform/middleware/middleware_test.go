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

func TestAccessLogRedactionLevelsAndScope(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.engine.GET("/api/v1/items/:id", ok)
	h.engine.GET("/api/v1/bad", func(c *gin.Context) { httpx.Fail(c, 400, apperr.ErrInvalidRequest) })
	h.engine.GET("/api/v1/boom", func(c *gin.Context) {
		httpx.Fail(c, 500, errors.New("call failed: Authorization: Bearer abcdefghijklmnopqrstuvwxyz and api_key=sk-live-1"))
	})
	h.engine.GET("/api/v1/silent500", func(c *gin.Context) { c.AbortWithStatus(500) })
	h.engine.GET("/outside", ok)

	h.do("GET", "/api/v1/items/7?token=abc&page=2", map[string]string{"User-Agent": "unit/1.0"}, nil)
	logs := h.logs.String()
	for _, want := range []string{"[AccessLog] GET /api/v1/items/7?token=REDACTED&page=2", `Route: /api/v1/items/:id`, "Response: 200", "UserAgent: unit/1.0", "ClientIP: 192.0.2.10", `"level":"info"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\n%s", want, logs)
		}
	}
	if len(h.rec.all()) != 0 {
		t.Fatal("success must not be persisted")
	}

	h.logs.Reset()
	h.do("GET", "/api/v1/bad", nil, nil)
	if !strings.Contains(h.logs.String(), `"level":"warn"`) || !strings.Contains(h.logs.String(), "ErrorCode: request.invalid") {
		t.Fatalf("4xx must warn: %s", h.logs)
	}
	if len(h.rec.all()) != 0 {
		t.Fatal("4xx is only logged")
	}

	h.logs.Reset()
	w := h.do("GET", "/api/v1/boom", nil, nil)
	logs = h.logs.String()
	if strings.Contains(logs, "abcdefghijklmnopqrstuvwxyz") || strings.Contains(logs, "sk-live-1") {
		t.Fatalf("secrets leaked into the log: %s", logs)
	}
	if !strings.Contains(logs, `"level":"error"`) || !strings.Contains(logs, "Bearer REDACTED") || !strings.Contains(logs, "api_key=REDACTED") {
		t.Fatalf("5xx log: %s", logs)
	}
	recs := h.rec.all()
	if len(recs) != 1 || recs[0].Status != 500 || recs[0].ErrorCode != "internal.error" || recs[0].RequestID != w.Header().Get("X-Request-ID") || !strings.Contains(recs[0].Cause, "Bearer REDACTED") || recs[0].Route != "/api/v1/boom" {
		t.Fatalf("recorded: %+v", recs)
	}

	h.logs.Reset()
	h.do("GET", "/api/v1/silent500", nil, nil)
	if !strings.Contains(h.logs.String(), "Error: <no cause recorded>") {
		t.Fatalf("missing marker: %s", h.logs)
	}

	h.logs.Reset()
	h.do("GET", "/outside", nil, nil)
	h.do("GET", "/api/v1/nope", nil, nil)
	if strings.Contains(h.logs.String(), "/outside") {
		t.Fatal("only /api/ paths are logged")
	}
	if !strings.Contains(h.logs.String(), "Route: -") {
		t.Fatalf("unmatched route must print '-': %s", h.logs)
	}
}

func TestStreamFailureAfterHeaderIsPersisted(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.engine.GET("/api/v1/stream", func(c *gin.Context) {
		c.Writer.WriteString("partial")
		reqctx.FromRequest(c.Request).RecordFailure("upstream.unavailable", errors.New("socket closed"), "")
	})
	h.do("GET", "/api/v1/stream", nil, nil)
	recs := h.rec.all()
	if len(recs) != 1 || recs[0].Status != 200 || recs[0].ErrorCode != "upstream.unavailable" {
		t.Fatalf("stream failure must be persisted: %+v", recs)
	}
	if !strings.Contains(h.logs.String(), `"level":"warn"`) {
		t.Fatal("stream failure logs at warn")
	}
}

func TestRecoveryPanic(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.engine.GET("/api/v1/panic", func(c *gin.Context) { panic("secret=hunter2 exploded") })
	w := h.do("GET", "/api/v1/panic", nil, nil)
	e := env(t, w)
	if w.Code != 500 || e.ErrorCode != "internal.error" || e.ErrorMsg != "internal server error" || e.RequestID == "" {
		t.Fatalf("panic response: %d %+v", w.Code, e)
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatal("panic text leaked")
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "http_panic_recovered") || !strings.Contains(logs, "goroutine") {
		t.Fatalf("panic log must carry the stack: %s", logs)
	}
	if strings.Contains(logs, "hunter2") {
		t.Fatal("panic value must be redacted in logs")
	}
	recs := h.rec.all()
	if len(recs) != 1 || recs[0].Status != 500 || recs[0].ErrorCode != "internal.error" || !strings.HasPrefix(recs[0].Cause, "panic: ") || strings.Contains(recs[0].Cause, "goroutine") {
		t.Fatalf("panic record: %+v", recs)
	}
	// the engine keeps serving after a panic
	h.engine.GET("/api/v1/fine", ok)
	if h.do("GET", "/api/v1/fine", nil, nil).Code != 200 {
		t.Fatal("engine must survive a panic")
	}
}

func authedEngine(h *harness, hook *SessionHook) *gin.RouterGroup {
	g := h.engine.Group("/api/v1/secure", Authenticate(h.tokens, hook))
	g.GET("/me", func(c *gin.Context) { httpx.OK(c, MustPrincipal(c)) })
	admin := h.engine.Group("/api/v1/admin", Authenticate(h.tokens, hook), AdminOnly())
	admin.GET("/x", ok)
	return g
}

func bearer(t *testing.T, h *harness, c token.Claims) map[string]string {
	t.Helper()
	raw, err := h.tokens.Issue(c, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + raw}
}

func TestAuthenticateFailures(t *testing.T) {
	h := newHarness(t, nil, nil)
	hook := &SessionHook{}
	authedEngine(h, hook)

	cases := []struct {
		name string
		hdr  map[string]string
		code string
		msg  string
	}{
		{"missing", nil, "auth.invalid_token", "authorization header is required"},
		{"basic scheme", map[string]string{"Authorization": "Basic xxx"}, "auth.invalid_token", "invalid authorization header"},
		{"empty bearer", map[string]string{"Authorization": "Bearer "}, "auth.invalid_token", "invalid authorization header"},
		{"garbage", map[string]string{"Authorization": "Bearer nope"}, "auth.invalid_token", "invalid token"},
		{"refresh type", bearer(t, h, token.Claims{UserID: 1, TokenType: token.TypeRefresh}), "auth.invalid_token", "invalid token type"},
	}
	for _, c := range cases {
		w := h.do("GET", "/api/v1/secure/me", c.hdr, nil)
		e := env(t, w)
		if w.Code != 401 || e.ErrorCode != c.code || e.ErrorMsg != c.msg {
			t.Errorf("%s: %d %+v", c.name, w.Code, e)
		}
	}
}

func TestAuthenticateSessionValidatorAndPrincipal(t *testing.T) {
	h := newHarness(t, []string{"192.0.2.0/24"}, nil)
	hook := &SessionHook{}
	authedEngine(h, hook)
	hdr := bearer(t, h, token.Claims{UserID: 5, Username: "ann", Role: "user", SessionID: 11})

	// no validator registered: accepted on signature and expiry alone
	if hook.IsSet() {
		t.Fatal("hook must start unset")
	}
	w := h.do("GET", "/api/v1/secure/me", hdr, nil)
	if w.Code != 200 {
		t.Fatalf("no validator: %d %s", w.Code, w.Body)
	}

	var seen SessionCheck
	hook.Set(SessionValidatorFunc(func(_ context.Context, c SessionCheck) error { seen = c; return nil }))
	hdr["User-Agent"] = "ua/1"
	hdr["CF-IPCountry"] = "DE"
	hdr["X-Vercel-IP-City"] = "Berlin"
	hdr["CF-IPLatitude"] = "52.52"
	hdr["CF-IPLongitude"] = "not-a-number"
	w = h.do("GET", "/api/v1/secure/me", hdr, nil)
	if w.Code != 200 {
		t.Fatalf("valid session: %d %s", w.Code, w.Body)
	}
	if seen.UserID != 5 || seen.SessionID != 11 || seen.IssuedAt.IsZero() ||
		seen.Audit.ClientIP != "192.0.2.10" || seen.Audit.UserAgent != "ua/1" ||
		seen.Audit.CountryCode != "DE" || seen.Audit.City != "Berlin" || seen.Audit.Location() != "Berlin, DE" ||
		seen.Audit.GeoSource != "proxy_header_trusted" || seen.Audit.GeoAccuracy != "ip" ||
		seen.Audit.Latitude == nil || *seen.Audit.Latitude != 52.52 || seen.Audit.Longitude != nil {
		t.Fatalf("session check: %+v", seen)
	}
	if e := env(t, w); e.Data.(map[string]any)["Username"] != "ann" {
		t.Fatalf("principal: %+v", e.Data)
	}

	hook.Set(SessionValidatorFunc(func(context.Context, SessionCheck) error { return errors.New("revoked") }))
	w = h.do("GET", "/api/v1/secure/me", hdr, nil)
	if e := env(t, w); w.Code != 401 || e.ErrorCode != "auth.session_invalid" || e.ErrorMsg != "session invalid" {
		t.Fatalf("revoked: %d %+v", w.Code, e)
	}
}

func TestGeoHeadersIgnoredFromUntrustedPeer(t *testing.T) {
	h := newHarness(t, nil, nil)
	var audit AuditContext
	h.engine.GET("/api/v1/a", func(c *gin.Context) { audit = AuditFromRequest(c.Request); httpx.OK(c, 1) })
	h.do("GET", "/api/v1/a", map[string]string{"CF-IPCountry": "DE"}, nil)
	if audit.CountryCode != "" || audit.GeoSource != "" || audit.ClientIP != "192.0.2.10" {
		t.Fatalf("untrusted geo headers must be ignored: %+v", audit)
	}
}

func TestAdminOnly(t *testing.T) {
	h := newHarness(t, nil, nil)
	authedEngine(h, &SessionHook{})
	w := h.do("GET", "/api/v1/admin/x", bearer(t, h, token.Claims{UserID: 1, Role: "user"}), nil)
	if e := env(t, w); w.Code != 403 || e.ErrorCode != "auth.admin_required" || e.ErrorMsg != "admin permission required" {
		t.Fatalf("user: %d %+v", w.Code, e)
	}
	w = h.do("GET", "/api/v1/admin/x", bearer(t, h, token.Claims{UserID: 1}), nil)
	if e := env(t, w); w.Code != 403 || e.ErrorCode != "auth.forbidden" {
		t.Fatalf("no role: %d %+v", w.Code, e)
	}
	for _, role := range []string{"admin", "superadmin"} {
		if w := h.do("GET", "/api/v1/admin/x", bearer(t, h, token.Claims{UserID: 1, Role: role}), nil); w.Code != 200 {
			t.Fatalf("%s: %d", role, w.Code)
		}
	}
}

func TestFeatureGate(t *testing.T) {
	h := newHarness(t, nil, nil)
	caps := capability.Local()
	h.engine.Group("/api/v1/billing", FeatureGate(caps, capability.BillingGating)).GET("/x", ok)
	h.engine.Group("/api/v1/usage", FeatureGate(caps, capability.UsageMetering)).GET("/x", ok)
	w := h.do("GET", "/api/v1/billing/x", nil, nil)
	e := env(t, w)
	d, _ := e.Details.(map[string]any)
	if w.Code != 404 || e.ErrorCode != "feature.disabled" || e.ErrorMsg != "this feature is not available on this server" || d["feature"] != "billingGating" {
		t.Fatalf("gate: %d %+v", w.Code, e)
	}
	if h.do("GET", "/api/v1/usage/x", nil, nil).Code != 200 {
		t.Fatal("enabled flag must pass")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("unknown flag must panic at registration")
		}
	}()
	FeatureGate(caps, "billingGatng")
}

func gzipBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	b, _ := io.ReadAll(zr)
	return string(b)
}

func TestGzipRules(t *testing.T) {
	h := newHarness(t, nil, nil)
	big := strings.Repeat("x", 5000)
	h.engine.GET("/api/v1/big", func(c *gin.Context) { httpx.OK(c, big) })
	h.engine.GET("/api/v1/small", func(c *gin.Context) { httpx.OK(c, "tiny") })
	h.engine.GET("/api/v1/sse", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Writer.WriteString("data: " + big + "\n\n")
	})
	h.engine.GET("/api/v1/nd", func(c *gin.Context) {
		c.Header("Content-Type", "application/x-ndjson")
		c.Writer.WriteString(big)
	})
	h.engine.GET("/api/v1/bin", func(c *gin.Context) { c.Data(200, "image/png", []byte(big)) })
	h.engine.GET("/api/v1/notransform", func(c *gin.Context) {
		c.Header("Cache-Control", "no-transform")
		httpx.OK(c, big)
	})
	h.engine.GET("/api/v1/etag", func(c *gin.Context) { c.Header("ETag", `"abc"`); httpx.OK(c, big) })
	h.engine.GET("/api/v1/empty", func(c *gin.Context) { c.Status(204) })

	gz := map[string]string{"Accept-Encoding": "gzip"}
	w := h.do("GET", "/api/v1/big", gz, nil)
	if w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Encoding") || w.Header().Get("Content-Length") != "" {
		t.Fatalf("big: %v", w.Header())
	}
	if !strings.Contains(gzipBody(t, w), big) {
		t.Fatal("decompressed body mismatch")
	}
	if w := h.do("GET", "/api/v1/big", nil, nil); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("no Accept-Encoding -> no compression")
	}
	if w := h.do("GET", "/api/v1/big", map[string]string{"Accept-Encoding": "gzip;q=0, br"}, nil); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 means no")
	}
	if w := h.do("GET", "/api/v1/big", map[string]string{"Accept-Encoding": "*"}, nil); w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("* accepts gzip")
	}
	if w := h.do("HEAD", "/api/v1/big", gz, nil); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("HEAD is not compressed")
	}
	if w := h.do("GET", "/api/v1/big", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-10"}, nil); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("Range requests are not compressed")
	}
	for _, p := range []string{"small", "sse", "nd", "bin", "notransform", "empty"} {
		w := h.do("GET", "/api/v1/"+p, gz, nil)
		if w.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s must not be compressed", p)
		}
	}
	if w := h.do("GET", "/api/v1/etag", gz, nil); w.Header().Get("ETag") != `W/"abc"` {
		t.Fatalf("strong etag must turn weak: %q", w.Header().Get("ETag"))
	}
}

func TestGzipFlushKeepsStreamTimely(t *testing.T) {
	h := newHarness(t, nil, nil)
	big := strings.Repeat("y", 3000)
	h.engine.GET("/api/v1/chunks", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Writer.WriteString(big)
		c.Writer.Flush()
		c.Writer.WriteString(big)
	})
	w := h.do("GET", "/api/v1/chunks", map[string]string{"Accept-Encoding": "gzip"}, nil)
	if w.Header().Get("Content-Encoding") != "gzip" || gzipBody(t, w) != big+big {
		t.Fatalf("flush + gzip: %v", w.Header())
	}
}

func rlHarness(t *testing.T, mutate func(*config.Config)) (*harness, RateLimitOptions) {
	h := newHarness(t, nil, func(c *config.Config) {
		c.Dynamic.Auth.RateLimitEnabled = true
		if mutate != nil {
			mutate(c)
		}
	})
	opts := RateLimitOptions{Config: h.cfg, Limiter: cache.NewMemory(h.clock), Clock: h.clock, Logger: logx.Nop()}
	return h, opts
}

func TestUserPolicies(t *testing.T) {
	cases := []struct {
		method, route string
		name          string
		limit         int
	}{
		{"POST", "/conversations/1/media/images/gen", "media_generation", 30},
		{"POST", "/conversations/1/messages", "message_generation", 60},
		{"POST", "/conversations/1/messages/stream", "message_generation", 60},
		{"POST", "/runs/1/cancel", "message_generation", 60},
		{"POST", "/files", "file_upload", 120},
		{"POST", "/files/processing/statuses", "polling", 600},
		{"POST", "/conversation-runs/statuses", "polling", 600},
		{"GET", "/conversations/1/runs", "polling", 600},
		{"GET", "/conversations/1/stream", "polling", 600},
		{"GET", "/files/processing", "polling", 600},
		{"GET", "/conversations", "read", 600},
		{"PUT", "/conversations/1", "write", 240},
		{"DELETE", "/conversations/1", "write", 240},
		{"POST", "/conversations", "write", 240},
		{"HEAD", "/conversations", "general", 360},
	}
	for _, c := range cases {
		name, limit := UserPolicy(c.method, c.route, 60)
		if name != c.name || limit != c.limit {
			t.Errorf("%s %s: got %s/%d want %s/%d", c.method, c.route, name, limit, c.name, c.limit)
		}
	}
	if _, limit := UserPolicy("GET", "/x", 0); limit != 600 {
		t.Fatalf("non-positive base falls back to 60: %d", limit)
	}
	if _, limit := UserPolicy("GET", "/x", 200); limit != 2000 {
		t.Fatalf("scaled limit: %d", limit)
	}
}

func TestPublicPolicies(t *testing.T) {
	cases := []struct {
		method, route string
		name          string
		limit         int
		code          string
	}{
		{"POST", "/auth/refresh", "auth_refresh", 300, "rate_limit.refresh_exceeded"},
		{"GET", "/auth/login-options", "public_auth_read", 300, "rate_limit.exceeded"},
		{"GET", "/settings/branding/logo", "public_auth_read", 300, "rate_limit.exceeded"},
		{"POST", "/auth/login", "public_auth", 30, "rate_limit.authentication_exceeded"},
		{"POST", "/billing/payments/epay/notify", "payment_callback", 600, "rate_limit.exceeded"},
		{"GET", "/settings/login-page", "public_read", 600, "rate_limit.exceeded"},
		{"GET", "/shared-conversations/abc", "public_read", 600, "rate_limit.exceeded"},
		{"GET", "/anything", "public", 300, "rate_limit.exceeded"},
	}
	for _, c := range cases {
		name, limit, e := PublicPolicy(c.method, c.route, 30)
		if name != c.name || limit != c.limit || e.Code != c.code {
			t.Errorf("%s %s: got %s/%d/%s want %s/%d/%s", c.method, c.route, name, limit, e.Code, c.name, c.limit, c.code)
		}
	}
}

func TestUserRateLimitEnforcement(t *testing.T) {
	h, opts := rlHarness(t, nil)
	g := h.engine.Group("/api/v1", Authenticate(h.tokens, &SessionHook{}), UserRateLimit(opts))
	g.GET("/things", ok)
	g.PUT("/things/:id", ok)

	user := bearer(t, h, token.Claims{UserID: 9, Role: "user"})
	for i := 0; i < 61; i++ {
		if w := h.do("GET", "/api/v1/things", user, nil); w.Code != 200 {
			t.Fatalf("GET %d denied: %d", i+1, w.Code)
		}
	}
	for i := 0; i < 240; i++ {
		if w := h.do("PUT", "/api/v1/things/1", user, nil); w.Code != 200 {
			t.Fatalf("PUT %d denied: %d", i+1, w.Code)
		}
	}
	w := h.do("PUT", "/api/v1/things/1", user, nil)
	e := env(t, w)
	if w.Code != 429 || e.ErrorCode != "rate_limit.exceeded" ||
		w.Header().Get("Retry-After") != "60" || w.Header().Get("X-RateLimit-Limit") != "240" ||
		w.Header().Get("X-RateLimit-Remaining") != "0" ||
		w.Header().Get("X-RateLimit-Reset") != "1780315260" { // 2026-06-01T12:01:00Z
		t.Fatalf("241st PUT: %d %v %+v", w.Code, w.Header(), e)
	}
	// another user is unaffected; an admin is never limited
	if h.do("PUT", "/api/v1/things/1", bearer(t, h, token.Claims{UserID: 10, Role: "user"}), nil).Code != 200 {
		t.Fatal("limits are per user")
	}
	admin := bearer(t, h, token.Claims{UserID: 1, Role: "admin"})
	for i := 0; i < 300; i++ {
		if w := h.do("PUT", "/api/v1/things/1", admin, nil); w.Code != 200 {
			t.Fatalf("admin limited at %d", i+1)
		}
	}
	h.clock.Advance(61 * time.Second)
	if h.do("PUT", "/api/v1/things/1", user, nil).Code != 200 {
		t.Fatal("window must roll over")
	}
}

func TestRateLimitDisabledByDefaultAndFailsOpen(t *testing.T) {
	h := newHarness(t, nil, nil) // RateLimitEnabled false
	opts := RateLimitOptions{Config: h.cfg, Limiter: cache.NewMemory(h.clock), Clock: h.clock}
	g := h.engine.Group("/api/v1", Authenticate(h.tokens, &SessionHook{}), UserRateLimit(opts))
	g.PUT("/t", ok)
	user := bearer(t, h, token.Claims{UserID: 9, Role: "user"})
	for i := 0; i < 400; i++ {
		if h.do("PUT", "/api/v1/t", user, nil).Code != 200 {
			t.Fatal("limiter must be inert when disabled")
		}
	}

	h2, opts2 := rlHarness(t, nil)
	opts2.Limiter = failingLimiter{}
	g2 := h2.engine.Group("/api/v1", UserRateLimit(opts2))
	g2.GET("/pub", ok)
	h2.engine.Group("/api/v1/p", PublicRateLimit(opts2)).GET("/auth/login", ok)
	if h2.do("GET", "/api/v1/pub", nil, nil).Code != 200 || h2.do("GET", "/api/v1/p/auth/login", nil, nil).Code != 200 {
		t.Fatal("storage errors must fail open")
	}
}

type failingLimiter struct{}

func (failingLimiter) AllowSliding(context.Context, string, int, time.Duration, time.Duration) (bool, error) {
	return false, errors.New("redis down")
}
func (failingLimiter) AllowFixed(context.Context, []string, int, time.Duration) (bool, error) {
	return false, errors.New("redis down")
}

func TestPublicRateLimitEnforcement(t *testing.T) {
	h, opts := rlHarness(t, nil)
	g := h.engine.Group("/api/v1", PublicRateLimit(opts))
	g.POST("/auth/login", ok)
	g.POST("/auth/refresh", ok)
	g.GET("/auth/login-options", ok)

	for i := 0; i < 30; i++ {
		if w := h.do("POST", "/api/v1/auth/login", nil, nil); w.Code != 200 {
			t.Fatalf("login %d denied", i+1)
		}
	}
	w := h.do("POST", "/api/v1/auth/login", nil, nil)
	if e := env(t, w); w.Code != 429 || e.ErrorCode != "rate_limit.authentication_exceeded" || e.ErrorMsg != "too many authentication attempts" {
		t.Fatalf("31st login: %d %+v", w.Code, e)
	}
	for i := 0; i < 300; i++ {
		if w := h.do("POST", "/api/v1/auth/refresh", nil, nil); w.Code != 200 {
			t.Fatalf("refresh %d denied", i+1)
		}
	}
	w = h.do("POST", "/api/v1/auth/refresh", nil, nil)
	if e := env(t, w); w.Code != 429 || e.ErrorCode != "rate_limit.refresh_exceeded" {
		t.Fatalf("301st refresh: %d %+v", w.Code, e)
	}
	// a different client IP has its own counters
	req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	req.RemoteAddr = "198.51.100.1:1000"
	rw := httptest.NewRecorder()
	h.engine.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("limits are per client ip: %d", rw.Code)
	}
}

func TestUpstreamProbeLimitAppliesToAdminsAndIgnoresSwitch(t *testing.T) {
	h := newHarness(t, nil, nil) // global switch off
	opts := RateLimitOptions{Config: h.cfg, Limiter: cache.NewMemory(h.clock), Clock: h.clock}
	g := h.engine.Group("/api/v1", Authenticate(h.tokens, &SessionHook{}))
	g.POST("/probe", UpstreamProbeLimit(opts, 2), ok)
	h.engine.POST("/api/v1/anon", UpstreamProbeLimit(opts, 1), ok)

	admin := bearer(t, h, token.Claims{UserID: 1, Role: "admin"})
	h.do("POST", "/api/v1/probe", admin, nil)
	h.do("POST", "/api/v1/probe", admin, nil)
	if w := h.do("POST", "/api/v1/probe", admin, nil); w.Code != 429 {
		t.Fatalf("third probe must be limited even for admins: %d", w.Code)
	}
	for i := 0; i < 5; i++ {
		if h.do("POST", "/api/v1/anon", nil, nil).Code != 200 {
			t.Fatal("unauthenticated requests are skipped")
		}
	}
}
