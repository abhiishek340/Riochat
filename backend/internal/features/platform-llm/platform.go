// Package platformllm is the outbound-integration layer shared by the feature
// areas: the model-provider client, outbound HTTP safety, object storage,
// embedding and extraction clients, tracing and the background job runner.
//
// The area owns no tables and no routes. Mount builds one Platform from the
// live configuration, wires tracing into the lifecycle and publishes the
// Platform through Current; areas that cannot import this package at build time
// define a small interface in their own package and let the integrator adapt.
package platformllm

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/dockerx"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/embedding"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/extract"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/geoip"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/jobs"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/mediadl"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/objectstore"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Platform bundles the clients. All members are safe for concurrent use.
type Platform struct {
	// Trusted is the deployment outbound policy (with allowlists); Strict has
	// no allowlists. Both enforce only in production with SSRF protection on.
	Trusted *outbound.Policy
	Strict  *outbound.Policy
	// HTTP is the per-origin client pool every outbound call goes through.
	HTTP *outbound.Pool
	// LLM is the uniform model-provider transport.
	LLM *llm.Client
	// Embeddings turns text and images into vectors.
	Embeddings *embedding.Client
	// Media downloads provider-returned artifacts.
	Media *mediadl.Downloader
	// Storage is the object store, resolved from live configuration on use.
	Storage *objectstore.Lazy
	// Tracer records spans; a disabled tracer still propagates context.
	Tracer *trace.Tracer
	// Jobs runs background workers through the lifecycle manager.
	Jobs *jobs.Runner
	// GeoIP resolves client locations; nil when the provider is disabled or
	// unusable, in which case lookups are skipped silently.
	GeoIP *geoip.Resolver
	// Docker runs the local docker client for managed sidecars.
	Docker *dockerx.Client

	cfg    *config.Holder
	logger *logx.Logger
}

// Params are the collaborators New needs.
type Params struct {
	Config  *config.Holder
	Logger  *logx.Logger
	Spawner jobs.Spawner
	// Getenv defaults to os.Getenv (proxy and Kubernetes variables).
	Getenv func(string) string
	// Proxy overrides proxy selection (tests); nil reads the environment.
	Proxy func(*http.Request) (*url.URL, error)
	// Resolver overrides DNS (tests).
	Resolver outbound.Resolver
}

// New builds a Platform from the live configuration. A tracing configuration
// that is enabled without an endpoint is an error.
func New(p Params) (*Platform, error) {
	cfg := p.Config.Load()
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	log := p.Logger
	if log == nil {
		log = logx.Nop()
	}

	enforce := cfg.IsProd() && cfg.Security.SSRFProtectionEnabled
	trusted, strict := outbound.FromConfig(enforce, cfg.Security.SSRFAllowedHosts, cfg.Security.SSRFAllowedCIDRs)

	ts, err := trace.ParseSettings(cfg.Tracing.Enabled, cfg.Tracing.Endpoint, cfg.Tracing.Headers, cfg.Tracing.Insecure,
		cfg.Tracing.Protocol, cfg.Tracing.SamplingRate, cfg.App.Name, getenv)
	if err != nil {
		return nil, err
	}
	if ts.Enabled && ts.Protocol == "grpc" {
		log.Warn("otel_grpc_exporter_unavailable", logx.F("detail", "traces are exported over OTLP/HTTP; set the protocol to http and use the HTTP endpoint"))
	}
	tracer := trace.NewFromSettings(ts, nil)

	var resolver outbound.Resolver = net.DefaultResolver
	if p.Resolver != nil {
		resolver = p.Resolver
	}
	pool := outbound.NewPool(outbound.PoolOptions{Trusted: trusted, Strict: strict, Resolver: resolver, Tracer: tracer, Proxy: p.Proxy})

	rep := outbound.ReportProxy(getenv, enforce)
	fields := make([]logx.Field, 0, len(rep.Fields))
	for k, v := range rep.Fields {
		fields = append(fields, logx.F(k, v))
	}
	if rep.Warn {
		log.Warn(rep.Event, fields...)
	} else {
		log.Info(rep.Event, fields...)
	}

	plat := &Platform{
		Trusted: trusted, Strict: strict, HTTP: pool, Tracer: tracer, cfg: p.Config, logger: log,
		LLM:        llm.NewClient(llm.ClientOptions{HTTP: pool, Tracer: tracer}),
		Embeddings: &embedding.Client{HTTP: pool, Tracer: tracer},
		Media:      &mediadl.Downloader{HTTP: pool, Tracer: tracer},
		Docker:     &dockerx.Client{},
	}
	plat.Storage = objectstore.NewLazy(func() objectstore.Settings { return objectstore.SettingsFrom(p.Config.Load().Storage) }, tracer)
	if p.Spawner != nil {
		plat.Jobs = jobs.NewRunner(p.Spawner, log, jobs.Options{})
	}
	plat.startGeoIP(cfg.GeoIP, p.Spawner)
	return plat, nil
}

// startGeoIP builds the resolver from configuration. GeoIP is optional: any
// problem is logged and leaves the resolver absent. With the mmdb provider and
// a download URL, a refresher keeps the database file current.
func (p *Platform) startGeoIP(g config.GeoIPConfig, spawn jobs.Spawner) {
	timeout := time.Duration(g.TimeoutMS) * time.Millisecond
	res, err := geoip.New(geoip.Settings{
		Provider: g.Provider, BaseURL: g.BaseURL, Token: g.Token, Timeout: timeout, DatabasePath: g.DatabasePath,
	}, p.HTTP)
	if err != nil {
		p.logger.Warn("geoip_disabled", logx.F("detail", err.Error()))
		return
	}
	p.GeoIP = res
	if res == nil || strings.ToLower(strings.TrimSpace(g.Provider)) != geoip.ProviderMMDB || strings.TrimSpace(g.DatabaseURL) == "" || spawn == nil {
		return
	}
	f := &geoip.Refresher{
		S: geoip.RefreshSettings{
			URL: strings.TrimSpace(g.DatabaseURL), Path: g.DatabasePath, MaxBytes: g.DatabaseMaxBytes,
			Interval: time.Duration(g.RefreshIntervalHours) * time.Hour,
		},
		HTTP: p.HTTP, Resolver: res,
		Log: func(msg string, err error) { p.logger.Warn(msg, logx.F("error", err.Error())) },
	}
	spawn.Go("geoip-refresh", f.Run)
}

// Extractor builds an extraction engine for the given kind and settings.
func (p *Platform) Extractor(ctx context.Context, kind string, c extract.Config) (extract.Engine, error) {
	return extract.New(ctx, kind, c, extract.Deps{HTTP: p.HTTP, LLM: p.LLM})
}

// Shutdown flushes tracing and releases pooled connections.
func (p *Platform) Shutdown(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	err := p.Tracer.Shutdown(ctx)
	p.HTTP.CloseIdle()
	return err
}

var current atomic.Pointer[Platform]

// Current returns the Platform published by Mount.
func Current() (*Platform, bool) {
	p := current.Load()
	return p, p != nil
}
