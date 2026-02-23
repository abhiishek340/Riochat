// Package deps defines Deps, the single value handed to every feature area's
// Mount function. It carries everything an area may use; areas must not reach
// for process globals.
package deps

import (
	"context"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

// Routes are the three route groups mounted under /api/v1. They differ only in
// the middleware already attached:
//
//	Public  per-IP rate limit; no authentication
//	Auth    bearer token + session validation + per-user rate limit
//	Admin   bearer token + session validation + administrator role
//
// Register routes with paths relative to /api/v1 (for example
// "/announcements"). Each area owns the URL prefixes listed in its spec.
type Routes struct {
	Public *gin.RouterGroup
	Auth   *gin.RouterGroup
	Admin  *gin.RouterGroup
}

// Deps is what an area receives. All fields are safe for concurrent use.
type Deps struct {
	// Routes are the /api/v1 route groups.
	Routes Routes
	// DB is the open, migrated database handle.
	DB *gorm.DB
	// Config publishes the live configuration. Call Config.Load() per use and do
	// not cache the snapshot: dynamic settings change without a restart.
	Config *config.Holder
	// Logger is scoped to the area (it carries an "area" field).
	Logger *logx.Logger
	// Cache is the shared cache (memory or Redis).
	Cache cache.Cache
	// Clock is the time source; use it instead of time.Now so tests can control
	// time.
	Clock clock.Clock

	// Secrets encrypts and decrypts stored secrets with the rotatable key ring.
	Secrets *secretbox.KeyRing
	// Tokens issues and parses access/refresh tokens.
	Tokens *token.Manager
	// Capabilities reports the feature flags (read per request).
	Capabilities capability.Provider
	// Lifecycle runs startup hooks, workers, detached tasks and stop hooks.
	Lifecycle *lifecycle.Manager

	// Sessions is where the auth area registers its session validator.
	Sessions *middleware.SessionHook
	// ErrorRecorder is where the error-log area registers its recorder.
	ErrorRecorder *middleware.RecorderHook
	// LocalGrant is where the auth area registers the desktop-sidecar login
	// grant issuer (used only in local mode).
	LocalGrant *LocalGrantHook
	// RateLimit configures limiter middlewares an area may add itself.
	RateLimit middleware.RateLimitOptions
}

// ForArea returns a copy whose logger carries the area name.
func (d Deps) ForArea(name string) Deps {
	d.Logger = d.Logger.With(logx.F("area", name))
	return d
}

// Gated returns route groups that answer 404 feature.disabled while the
// capability flag is off. The flag name must be one of capability.Names();
// an unknown name panics so a typo aborts startup.
func (d Deps) Gated(flag string) Routes {
	gate := middleware.FeatureGate(d.Capabilities, flag)
	return Routes{
		Public: d.Routes.Public.Group("", gate),
		Auth:   d.Routes.Auth.Group("", gate),
		Admin:  d.Routes.Admin.Group("", gate),
	}
}

// ProbeLimit returns the per-user limiter for endpoints that make the server
// call user-supplied addresses (see middleware.UpstreamProbeLimit).
func (d Deps) ProbeLimit(perMinute int) gin.HandlerFunc {
	return middleware.UpstreamProbeLimit(d.RateLimit, perMinute)
}

// LocalGrantFunc issues the one-time credential printed on the desktop
// sidecar's ready line. The auth area registers it.
type LocalGrantFunc func(ctx context.Context) (string, error)

// LocalGrantHook holds the registered LocalGrantFunc.
type LocalGrantHook struct {
	v atomic.Pointer[LocalGrantFunc]
}

// Set registers the function (last writer wins).
func (h *LocalGrantHook) Set(f LocalGrantFunc) { h.v.Store(&f) }

// Issue calls the registered function; ok is false when none is registered.
func (h *LocalGrantHook) Issue(ctx context.Context) (grant string, ok bool, err error) {
	if h == nil {
		return "", false, nil
	}
	p := h.v.Load()
	if p == nil || *p == nil {
		return "", false, nil
	}
	grant, err = (*p)(ctx)
	return grant, true, err
}
