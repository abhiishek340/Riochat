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
