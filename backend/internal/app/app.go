// Package app wires the platform together: configuration, logging, secrets,
// database, migrations, cache, router, feature areas, startup hooks, the HTTP
// server and the phased graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/buildinfo"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
	"github.com/riochat/riochat/backend/internal/platform/server"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

// Options configures Build.
type Options struct {
	// Config is the validated static configuration.
	Config *config.Config
	// Features are the areas to migrate and mount (registry.Registered() in
	// production).
	Features []features.Feature
	// LogWriter receives the JSON log lines (stdout in server mode, stderr in
	// local mode). Defaults to io.Discard.
	LogWriter io.Writer
	// Clock overrides the time source (tests). Defaults to the system clock.
	Clock clock.Clock
	// Build overrides the build metadata (tests). Defaults to buildinfo.Resolve.
	Build *buildinfo.Info
	// Stdout receives the single ready line in local mode. Defaults to io.Discard.
	Stdout io.Writer
}

// App is a fully wired, not yet serving process.
type App struct {
	Config    *config.Holder
	Logger    *logx.Logger
	DB        *gorm.DB
	Cache     cache.Cache
	Clock     clock.Clock
	Deps      deps.Deps
	Server    *server.Server
	Lifecycle *lifecycle.Manager
	Build     buildinfo.Info

	grant  *deps.LocalGrantHook
	stdout io.Writer
}

// Build validates configuration, opens infrastructure, runs migrations, builds
// the router and mounts the feature areas. On error everything opened so far is
// closed.
func Build(ctx context.Context, o Options) (_ *App, err error) {
	cfg := o.Config
	if cfg == nil {
		return nil, errors.New("app: nil config")
	}
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := features.Validate(o.Features); err != nil {
		return nil, err
	}
	w := o.LogWriter
	if w == nil {
		w = io.Discard
	}
	level := logx.LevelDebug
	if cfg.IsProd() {
		level = logx.LevelInfo
	}
	log := logx.New(w, level)
	clk := o.Clock
	if clk == nil {
		clk = clock.System{}
	}
	build := buildinfo.Resolve()
	if o.Build != nil {
		build = *o.Build
	}

	for _, warning := range config.SecurityWarnings(cfg) {
		log.Warn("security_config_warning", logx.F("detail", warning))
	}

	keys, err := secretbox.NewKeyRing(cfg.Security.DataEncryptionKey, cfg.Security.DataEncryptionKeysPrev)
	if err != nil {
		return nil, fmt.Errorf("key ring: %w", err)
	}

	life := lifecycle.NewManager(log)
	var closers []func()
	defer func() {
		if err != nil {
			for i := len(closers) - 1; i >= 0; i-- {
				closers[i]()
			}
		}
	}()

	db, err := database.Open(ctx, cfg, log, clk)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	closers = append(closers, func() { _ = database.Close(db) })

	cch, err := cache.Open(ctx, cfg, clk)
	if err != nil {
		return nil, fmt.Errorf("open cache: %w", err)
	}
	closers = append(closers, func() { _ = cch.Close() })

	if err := migrate.Run(ctx, db, log, features.MigrationSets(o.Features)...); err != nil {
		return nil, err
	}

	// Stop hooks run in reverse registration order. The infrastructure closers are
	// registered before any feature, so feature hooks run first, then the cache
	// closes, then the database.
	life.OnStop("database", func(context.Context) error { return database.Close(db) })
	life.OnStop("cache", func(context.Context) error { return cch.Close() })

	holder := config.NewHolder(cfg)
	caps := capability.Server()
	if cfg.IsLocal() {
		caps = capability.Local()
	}
	tokens := token.NewManager(func() string { return holder.Load().Security.JWTSecret }, clk)

	srv, err := server.New(server.Options{
		Config:       holder,
		Logger:       log,
		Clock:        clk,
		DB:           db,
		Cache:        cch,
		Capabilities: caps,
		Tokens:       tokens,
		Build:        build,
		Drain:        life.Drain(),
	})
	if err != nil {
		return nil, err
	}

	grant := &deps.LocalGrantHook{}
	d := deps.Deps{
		Routes:        srv.Routes,
		DB:            db,
		Config:        holder,
		Logger:        log,
		Cache:         cch,
		Clock:         clk,
		Secrets:       keys,
		Tokens:        tokens,
		Capabilities:  caps,
		Lifecycle:     life,
		Sessions:      srv.Sessions,
		ErrorRecorder: srv.ErrorRecorder,
		LocalGrant:    grant,
		RateLimit:     srv.RateLimit,
	}
	if err := features.MountAll(d, o.Features); err != nil {
		return nil, err
	}

	stdout := o.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	return &App{
		Config: holder, Logger: log, DB: db, Cache: cch, Clock: clk, Deps: d,
		Server: srv, Lifecycle: life, Build: build, grant: grant, stdout: stdout,
	}, nil
}

// shutdownSettings reads the effective timeouts with the documented fallbacks.
func httpTimeouts(c *config.Config) (readHeader, read, idle time.Duration, maxHeader int, shutdown time.Duration) {
	pos := func(v, def int) int {
		if v <= 0 {
			return def
		}
		return v
	}
	return time.Duration(pos(c.Server.ReadHeaderTimeoutSecs, 10)) * time.Second,
		time.Duration(pos(c.Server.ReadTimeoutSecs, 120)) * time.Second,
		time.Duration(pos(c.Server.IdleTimeoutSecs, 120)) * time.Second,
		pos(c.Server.MaxHeaderBytes, 1<<20),
		time.Duration(pos(c.Server.ShutdownTimeoutSecs, 10)) * time.Second
}

// NewHTTPServer builds the net/http server for the current configuration. There
// is deliberately no write timeout: streaming responses must not be cut.
func (a *App) NewHTTPServer() *http.Server {
	cfg := a.Config.Load()
	rh, r, idle, mh, _ := httpTimeouts(cfg)
	return &http.Server{
		Handler:           a.Server.Engine,
		ReadHeaderTimeout: rh,
		ReadTimeout:       r,
		IdleTimeout:       idle,
		MaxHeaderBytes:    mh,
	}
}

// Listen binds the configured address.
func (a *App) Listen() (net.Listener, error) {
	return net.Listen("tcp", a.Config.Load().ListenAddress())
}

// Close releases everything Build opened without having served: workers are
// stopped and the cache and database are closed. Run/Serve do this themselves
// at shutdown; Close is for tests and for callers that never serve.
func (a *App) Close() {
	a.Lifecycle.Drain().Begin()
	a.shutdownResources(context.Background())
}
