package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// syncBuffer is a concurrency-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.buf.String() }

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.Database.Driver = config.DriverSQLite
	cfg.Database.SQLite.Path = filepath.Join(t.TempDir(), "app.db")
	cfg.Cache.Driver = config.CacheMemory
	cfg.Server.FrontendDistDir = ""
	return &cfg
}

func build(t *testing.T, cfg *config.Config, list []features.Feature) (*App, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	a, err := Build(context.Background(), Options{Config: cfg, Features: list, LogWriter: logs})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, logs)
	}
	return a, logs
}

func startServing(t *testing.T, a *App) (addr string, cancel context.CancelCauseFunc, done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	done = make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()
	addr = ln.Addr().String()
	waitFor(t, func() bool {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	return addr, cancel, done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestBuildRejectsInvalidConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.App.Env = config.EnvProd // dev secrets are rejected in production
	if _, err := Build(context.Background(), Options{Config: cfg}); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("want JWT_SECRET error, got %v", err)
	}
	if _, err := Build(context.Background(), Options{}); err == nil {
		t.Fatal("nil config must fail")
	}
	bad := []features.Feature{{Name: "Bad Name", Mount: func(deps.Deps) error { return nil }}}
	if _, err := Build(context.Background(), Options{Config: testConfig(t), Features: bad}); err == nil {
		t.Fatal("invalid feature name must fail")
	}
}

func TestBuildLogsSecurityWarningsAndSucceedsWithPlaceholders(t *testing.T) {
	cfg := testConfig(t) // dev defaults are placeholders
	a, logs := build(t, cfg, nil)
	defer a.Close()
	if !strings.Contains(logs.String(), "security_config_warning") || !strings.Contains(logs.String(), "JWT_SECRET is a placeholder") {
		t.Fatalf("warnings missing: %s", logs)
	}
}

func TestBuildFailureClosesResourcesAndNamesArea(t *testing.T) {
	cfg := testConfig(t)
	boom := errors.New("mount exploded")
	_, err := Build(context.Background(), Options{Config: cfg, Features: []features.Feature{
		{Name: "broken", Mount: func(deps.Deps) error { return boom }},
	}})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), `"broken"`) {
		t.Fatalf("err: %v", err)
	}
}

func TestServeLifecycleAndShutdownOrder(t *testing.T) {
	cfg := testConfig(t)
	var order []string
	var mu sync.Mutex
	record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	var startHook atomic.Bool

	feat := features.Feature{Name: "probe", Mount: func(d deps.Deps) error {
		d.Routes.Public.GET("/hello", func(c *gin.Context) { httpx.OK(c, "hi") })
		d.Lifecycle.OnStart("probe", func(context.Context) error { startHook.Store(true); return nil })
		d.Lifecycle.Go("probe-worker", func(ctx context.Context) { <-ctx.Done(); record("worker-stopped") })
		d.Lifecycle.OnStop("probe-stop", func(context.Context) error {
			// the database must still be open while feature stop hooks run
			if err := database.Ping(context.Background(), d.DB); err != nil {
				record("db-already-closed")
			}
			record("feature-stop")
			return nil
		})
		return nil
	}}
	a, logs := build(t, cfg, []features.Feature{feat})
	addr, cancel, done := startServing(t, a)

	if !startHook.Load() {
		t.Fatal("start hook did not run before serving")
	}
	resp, err := http.Get("http://" + addr + "/api/v1/hello")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("hello: %v %v", resp, err)
	}
	resp.Body.Close()
	resp, _ = http.Get("http://" + addr + "/readyz")
	var ready struct{ Status string }
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	resp.Body.Close()
	if resp.StatusCode != 200 || ready.Status != "ok" {
		t.Fatalf("readyz: %d %+v", resp.StatusCode, ready)
	}

	cancel(errors.New("SIGTERM"))
	if err := <-done; err != nil {
		t.Fatalf("serve returned %v", err)
	}
	if !a.Lifecycle.Drain().Draining() {
		t.Fatal("drain must have begun")
	}
	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	if got != "worker-stopped,feature-stop" {
		t.Fatalf("shutdown order: %s", got)
	}
	if err := database.Ping(context.Background(), a.DB); err == nil {
		t.Fatal("database must be closed after shutdown")
	}
	if err := a.Cache.Ping(context.Background()); err == nil {
		t.Fatal("cache must be closed after shutdown")
	}
	for _, want := range []string{"server_starting", "server_shutting_down signal SIGTERM", "server_stopped", "session_validator_not_registered"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q", want)
		}
	}
}

func TestInFlightRequestFinishesWithinTimeout(t *testing.T) {
	cfg := testConfig(t)
	cfg.Server.ShutdownTimeoutSecs = 5
	started := make(chan struct{})
	release := make(chan struct{})
	feat := features.Feature{Name: "slow", Mount: func(d deps.Deps) error {
		d.Routes.Public.GET("/slow", func(c *gin.Context) {
			close(started)
			<-release
			httpx.OK(c, "done")
		})
		return nil
	}}
	a, _ := build(t, cfg, []features.Feature{feat})
	addr, cancel, done := startServing(t, a)

	result := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/api/v1/slow")
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()
	<-started
	cancel(errors.New("SIGTERM"))
	waitFor(t, func() bool { return a.Lifecycle.Drain().Draining() })
	close(release)
	if got := <-result; !strings.Contains(got, `"done"`) {
		t.Fatalf("in-flight request must complete: %s", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestForcedCloseAfterTimeoutIsNotAFailure(t *testing.T) {
	cfg := testConfig(t)
	cfg.Server.ShutdownTimeoutSecs = 1
	started := make(chan struct{})
	release := make(chan struct{})
	feat := features.Feature{Name: "stuck", Mount: func(d deps.Deps) error {
		d.Routes.Public.GET("/stuck", func(c *gin.Context) {
			close(started)
			select {
			case <-release:
			case <-c.Request.Context().Done():
			}
		})
		return nil
	}}
	a, logs := build(t, cfg, []features.Feature{feat})
	addr, cancel, done := startServing(t, a)
	defer close(release)

	go func() {
		resp, err := http.Get("http://" + addr + "/api/v1/stuck")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	cancel(errors.New("SIGTERM"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("forced close must still exit successfully: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung")
	}
	if !strings.Contains(logs.String(), "server_drain_timeout_force_close") {
		t.Fatalf("expected forced close warning: %s", logs)
	}
}

func TestStartHookFailureAbortsServe(t *testing.T) {
	cfg := testConfig(t)
	boom := errors.New("bootstrap failed")
	a, _ := build(t, cfg, []features.Feature{{Name: "boot", Mount: func(d deps.Deps) error {
		d.Lifecycle.OnStart("boot", func(context.Context) error { return boom })
		return nil
	}}})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	err := a.Serve(context.Background(), ln)
	if !errors.Is(err, boom) {
		t.Fatalf("err: %v", err)
	}
	if database.Ping(context.Background(), a.DB) == nil {
		t.Fatal("resources must be released after a startup failure")
	}
}

func TestParseFlags(t *testing.T) {
	var sink bytes.Buffer
	if _, err := ParseFlags([]string{"--data-dir", "/x"}, &sink); err == nil || !strings.Contains(err.Error(), "requires --local") {
		t.Fatalf("data-dir without local: %v", err)
	}
	if _, err := ParseFlags([]string{"--local"}, &sink); err == nil || !strings.Contains(err.Error(), "requires --data-dir") {
		t.Fatalf("local without data-dir: %v", err)
	}
	if _, err := ParseFlags([]string{"--bogus"}, &sink); err == nil {
		t.Fatal("unknown flag must fail")
	}
	f, err := ParseFlags([]string{"--local", "--data-dir", "rel/dir"}, &sink)
	if err != nil || !filepath.IsAbs(f.DataDir) || !f.Local {
		t.Fatalf("relative data dir must become absolute: %+v %v", f, err)
	}
	f, err = ParseFlags(nil, &sink)
	if err != nil || f.Local {
		t.Fatalf("no flags: %+v %v", f, err)
	}
}

func TestLocalModeEndToEnd(t *testing.T) {
	for _, k := range []string{"APP_ENV", "DATABASE_DRIVER", "CACHE_DRIVER", "CONFIG_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("CONFIG_FILE", "")
	dataDir := filepath.Join(t.TempDir(), "data")

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderr := &syncBuffer{}

	feat := features.Feature{Name: "grant", Mount: func(d deps.Deps) error {
		d.LocalGrant.Set(func(context.Context) (string, error) { return "one-time-grant", nil })
		d.Routes.Public.GET("/who", func(c *gin.Context) {
			httpx.OK(c, gin.H{"publicAPI": d.Config.Load().Server.PublicAPIBaseURL})
		})
		return nil
	}}

	done := make(chan error, 1)
	go func() {
		done <- RunCLI(context.Background(), []string{"--local", "--data-dir", dataDir},
			Streams{Stdin: stdinR, Stdout: stdoutW, Stderr: stderr}, []features.Feature{feat})
		stdoutW.Close()
	}()

	line := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 512)
		one := make([]byte, 1)
		for {
			if _, err := stdoutR.Read(one); err != nil {
				line <- string(buf)
				return
			}
			if one[0] == '\n' {
				line <- string(buf)
				return
			}
			buf = append(buf, one[0])
		}
	}()

	var ready map[string]any
	select {
	case l := <-line:
		if err := json.Unmarshal([]byte(l), &ready); err != nil {
			t.Fatalf("first stdout line is not JSON: %q (%v)\nstderr: %s", l, err, stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("no ready line\nstderr: %s", stderr)
	}
	origin, _ := ready["origin"].(string)
	if ready["type"] != "ready" || ready["grant"] != "one-time-grant" || !strings.HasPrefix(origin, "http://127.0.0.1:") ||
		ready["pid"] == nil || ready["version"] == nil {
		t.Fatalf("ready line: %v", ready)
	}

	resp, err := http.Get(origin + "/api/v1/who")
	if err != nil {
		t.Fatal(err)
	}
	var env httpx.Envelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	resp.Body.Close()
	if env.Data.(map[string]any)["publicAPI"] != origin {
		t.Fatalf("public URL must be set to the origin: %+v", env.Data)
	}
	resp, _ = http.Get(origin + "/api/v1/capabilities")
	var caps httpx.Envelope
	_ = json.NewDecoder(resp.Body).Decode(&caps)
	resp.Body.Close()
	feats := caps.Data.(map[string]any)["features"].(map[string]any)
	if feats["usageMetering"] != true || feats["multiUser"] != false || feats["sharing"] != false {
		t.Fatalf("local capabilities: %v", feats)
	}

	// closing stdin = parent exit = graceful shutdown with success
	stdinW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error: %v\nstderr: %s", err, stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("did not shut down after stdin EOF")
	}
	if !strings.Contains(stderr.String(), "server_shutting_down signal parent_exit") {
		t.Fatalf("expected parent_exit reason on stderr: %s", stderr)
	}
	if strings.Contains(stderr.String(), "one-time-grant") {
		t.Fatal("the grant must never be logged")
	}
}

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}
