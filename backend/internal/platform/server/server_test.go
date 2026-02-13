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

func TestUnknownRouteIs404Envelope(t *testing.T) {
	f := newFixture(t, nil, false, false)
	w := f.get("/api/v1/does-not-exist")
	var env httpx.Envelope
	decode(t, w, &env)
	if w.Code != 404 || env.ErrorCode != "resource.not_found" || env.RequestID == "" || w.Header().Get("X-Request-ID") != env.RequestID {
		t.Fatalf("%d %+v", w.Code, env)
	}
	if f.get("/anything").Code != 404 {
		t.Fatal("without front end every unknown path is 404")
	}
}

func TestRouteGroupsEnforceAuth(t *testing.T) {
	f := newFixture(t, nil, false, false)
	f.srv.Routes.Public.GET("/pub", func(c *gin.Context) { httpx.OK(c, "pub") })
	f.srv.Routes.Auth.GET("/priv", func(c *gin.Context) { httpx.OK(c, "priv") })
	f.srv.Routes.Admin.GET("/adm", func(c *gin.Context) { httpx.OK(c, "adm") })

	if f.get("/api/v1/pub").Code != 200 {
		t.Fatal("public group is open")
	}
	if w := f.get("/api/v1/priv"); w.Code != 401 {
		t.Fatalf("auth group needs a token: %d", w.Code)
	}
	if w := f.get("/api/v1/adm"); w.Code != 401 {
		t.Fatalf("admin group needs a token: %d", w.Code)
	}
	mgr := token.NewManager(func() string { return "secret-for-tests-0123456789" }, nil)
	user, _ := mgr.Issue(token.Claims{UserID: 1, Role: "user"}, time.Hour)
	admin, _ := mgr.Issue(token.Claims{UserID: 2, Role: "admin"}, time.Hour)
	if f.get("/api/v1/priv", "Authorization", "Bearer "+user).Code != 200 {
		t.Fatal("user can use auth group")
	}
	if f.get("/api/v1/adm", "Authorization", "Bearer "+user).Code != 403 {
		t.Fatal("user must not use admin group")
	}
	if f.get("/api/v1/adm", "Authorization", "Bearer "+admin).Code != 200 {
		t.Fatal("admin can use admin group")
	}
}

func TestInvalidTrustedProxyFailsStartup(t *testing.T) {
	c := config.Default()
	c.Server.TrustedProxies = "bogus"
	_, err := New(Options{Config: config.NewHolder(&c), Capabilities: capability.Server()})
	if err == nil {
		t.Fatal("unparsable trusted proxy must fail startup")
	}
}

func writeSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"index.html":                        "<html>index</html>",
		"settings/general.html":             "<html>general</html>",
		"docs/index.html":                   "<html>docs</html>",
		"_next/static/chunk.js":             "console.log(1)",
		"fonts/a.woff2":                     "font",
		"vendor/lobehub-icons/1.2.3/x.svg":  "<svg/>",
		"vendor/lobehub-icons/latest/x.svg": "<svg/>",
		"robots.txt":                        "User-agent: *",
		"logo.png":                          "png",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestStaticHosting(t *testing.T) {
	root := writeSite(t)
	f := newFixture(t, func(c *config.Config) { c.Server.FrontendDistDir = root }, false, false)

	for _, p := range []string{"/api/unknown", "/api", "/healthz/x", "/swagger/index.html", "/readyz/deep"} {
		w := f.get(p)
		var env httpx.Envelope
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != 404 || env.ErrorCode != "resource.not_found" {
			t.Errorf("%s must be the JSON 404, got %d %q", p, w.Code, w.Body.String())
		}
	}
	checks := []struct {
		path, body, cache string
	}{
		{"/", "<html>index</html>", "no-cache"},
		{"/settings/general", "<html>general</html>", "no-cache"},
		{"/docs", "<html>docs</html>", "no-cache"},
		{"/some/spa/route", "<html>index</html>", "no-cache"},
		{"/_next/static/chunk.js", "console.log(1)", "public, max-age=31536000, immutable"},
		{"/fonts/a.woff2", "font", "public, max-age=31536000, immutable"},
		{"/vendor/lobehub-icons/1.2.3/x.svg", "<svg/>", "public, max-age=31536000, immutable"},
		{"/vendor/lobehub-icons/latest/x.svg", "<svg/>", "public, max-age=3600"},
		{"/robots.txt", "User-agent: *", "no-cache"},
		{"/logo.png", "png", "public, max-age=3600"},
	}
	for _, c := range checks {
		w := f.get(c.path)
		if w.Code != 200 || w.Body.String() != c.body || w.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s: %d body=%q cache=%q", c.path, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
		}
	}
	if f.get("/../../etc/passwd").Body.String() != "<html>index</html>" {
		t.Fatal("path escape must never leave the root")
	}
	req := httptest.NewRequest("POST", "/settings/general", nil)
	w := httptest.NewRecorder()
	f.srv.Engine.ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "errorCode") {
		t.Fatalf("non-GET must not hit the front end: %d", w.Code)
	}
	if !strings.Contains(f.logs.String(), "frontend_static_enabled") {
		t.Fatalf("expected frontend_static_enabled log: %s", f.logs)
	}
}

func TestStaticFallbackTo404Page(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "404.html"), []byte("<html>nope</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, func(c *config.Config) { c.Server.FrontendDistDir = root }, false, false)
	w := f.get("/missing")
	if w.Code != 404 || w.Body.String() != "<html>nope</html>" {
		t.Fatalf("404.html: %d %q", w.Code, w.Body.String())
	}
	empty := t.TempDir()
	f2 := newFixture(t, func(c *config.Config) { c.Server.FrontendDistDir = empty }, false, false)
	if w := f2.get("/missing"); w.Code != 404 || !strings.Contains(w.Body.String(), "errorCode") {
		t.Fatalf("no 404.html -> envelope: %d %q", w.Code, w.Body.String())
	}
}

func TestStaticMissingDirWarns(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.Server.FrontendDistDir = filepath.Join(t.TempDir(), "nope") }, false, false)
	if w := f.get("/"); w.Code != 404 {
		t.Fatalf("missing dir: %d", w.Code)
	}
	if !strings.Contains(f.logs.String(), "frontend_static_disabled") {
		t.Fatalf("expected warning: %s", f.logs)
	}
}
