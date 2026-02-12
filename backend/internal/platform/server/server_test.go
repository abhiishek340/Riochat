package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/buildinfo"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

type fixture struct {
	srv   *Server
	logs  *bytes.Buffer
	cache cache.Cache
	drain *lifecycle.Drain
	cfg   *config.Holder
}

func newFixture(t *testing.T, mutate func(*config.Config), noDB, noCache bool) *fixture {
	t.Helper()
	c := config.Default()
	c.App.Env = config.EnvDev
	c.Server.FrontendDistDir = ""
	if mutate != nil {
		mutate(&c)
	}
	f := &fixture{logs: &bytes.Buffer{}, drain: lifecycle.NewDrain(), cfg: config.NewHolder(&c)}
	opts := Options{
		Config:       f.cfg,
		Logger:       logx.New(f.logs, logx.LevelDebug),
		Capabilities: capability.Server(),
		Tokens:       token.NewManager(func() string { return "secret-for-tests-0123456789" }, nil),
		Build:        buildinfo.Info{Version: "9.9.9", Commit: "abc", BuildTime: "t", BuildID: "9.9.9-abc-t"},
		Drain:        f.drain,
	}
	if !noDB {
		opts.DB = dbtest.New(t)
	}
	if !noCache {
		f.cache = cache.NewMemory(nil)
		opts.Cache = f.cache
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.srv = s
	return f
}

func (f *fixture) get(target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	f.srv.Engine.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("bad json %q: %v", w.Body.String(), err)
	}
}

func TestHealthzIsNotAccessLogged(t *testing.T) {
	f := newFixture(t, nil, false, false)
	w := f.get("/healthz")
	var body map[string]string
	decode(t, w, &body)
	if w.Code != 200 || body["status"] != "ok" || body["version"] != "9.9.9" {
		t.Fatalf("%d %v", w.Code, body)
	}
	if strings.Contains(f.logs.String(), "AccessLog") {
		t.Fatal("healthz must not be access-logged")
	}
}

func TestReadyz(t *testing.T) {
	f := newFixture(t, nil, false, false)
	w := f.get("/readyz")
	var body struct {
		Status string
		Checks map[string]string
	}
	decode(t, w, &body)
	if w.Code != 200 || body.Status != "ok" || body.Checks["db"] != "ok" || body.Checks["memory"] != "ok" {
		t.Fatalf("ready: %d %+v", w.Code, body)
	}

	f.drain.Begin()
	w = f.get("/readyz")
	decode(t, w, &body)
	if w.Code != 503 || body.Status != "draining" {
		t.Fatalf("draining: %d %+v", w.Code, body)
	}

	nc := newFixture(t, nil, false, true)
	w = nc.get("/readyz")
	body.Checks = nil
	decode(t, w, &body)
	if w.Code != 503 || body.Status != "degraded" || body.Checks["cache"] != "not_configured" {
		t.Fatalf("no cache: %d %+v", w.Code, body)
	}

	nd := newFixture(t, nil, true, false)
	w = nd.get("/readyz")
	body.Checks = nil
	decode(t, w, &body)
	if w.Code != 503 || body.Checks["db"] != "not_configured" {
		t.Fatalf("no db: %d %+v", w.Code, body)
	}

	broken := newFixture(t, nil, false, false)
	_ = broken.cache.Close()
	w = broken.get("/readyz")
	body.Checks = nil
	decode(t, w, &body)
	if w.Code != 503 || body.Checks["memory"] != "error" {
		t.Fatalf("broken cache: %d %+v", w.Code, body)
	}
}

func TestVersionAndCapabilities(t *testing.T) {
	f := newFixture(t, nil, false, false)
	w := f.get("/api/v1/version")
	var env httpx.Envelope
	decode(t, w, &env)
	data := env.Data.(map[string]any)
	if w.Code != 200 || data["product"] != "Riochat" || data["version"] != "9.9.9" || data["commit"] != "abc" || data["buildTime"] != "t" || data["buildID"] != "9.9.9-abc-t" {
		t.Fatalf("version: %d %v", w.Code, data)
	}
	if w.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate" || w.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("version cache headers: %v", w.Header())
	}

	w = f.get("/api/v1/capabilities")
	decode(t, w, &env)
	feats := env.Data.(map[string]any)["features"].(map[string]any)
	if len(feats) != 10 || feats["multiUser"] != true || w.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("capabilities: %v %v", feats, w.Header())
	}
}
