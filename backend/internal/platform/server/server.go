// Package server builds the HTTP engine: the middleware chain, the platform
// routes (/healthz, /readyz, /api/v1/version, /api/v1/capabilities), the three
// /api/v1 route groups handed to feature areas, and the fallback for unmatched
// requests (front-end hosting or the 404 envelope).
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/buildinfo"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

// MaxMultipartMemory is the multipart memory bound; larger parts spill to
// temporary files.
const MaxMultipartMemory = 8 << 20

// Options wires the engine to the rest of the process.
type Options struct {
	Config       *config.Holder
	Logger       *logx.Logger
	Clock        clock.Clock
	DB           *gorm.DB
	Cache        cache.Cache
	Capabilities capability.Provider
	Tokens       middleware.TokenParser
	Build        buildinfo.Info
	Drain        *lifecycle.Drain
}

// Server is the built engine plus the hooks features register into.
type Server struct {
	Engine        *gin.Engine
	Routes        deps.Routes
	Sessions      *middleware.SessionHook
	ErrorRecorder *middleware.RecorderHook
	RateLimit     middleware.RateLimitOptions
}

// New builds the engine. It returns an error only for invalid trusted-proxy
// configuration (startup must fail).
func New(o Options) (*Server, error) {
	if o.Logger == nil {
		o.Logger = logx.Nop()
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	cfg := o.Config.Load()
	proxies, err := middleware.NewProxySet(cfg.TrustedProxyList())
	if err != nil {
		return nil, err
	}

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.MaxMultipartMemory = MaxMultipartMemory
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	_ = engine.SetTrustedProxies(nil) // client IPs are resolved by our own middleware

	s := &Server{
		Engine:        engine,
		Sessions:      &middleware.SessionHook{},
		ErrorRecorder: &middleware.RecorderHook{},
	}
	s.RateLimit = middleware.RateLimitOptions{Config: o.Config, Clock: o.Clock, Logger: o.Logger}
	if o.Cache != nil {
		s.RateLimit.Limiter = o.Cache
	}

	engine.Use(
		middleware.Recovery(o.Logger, s.ErrorRecorder, o.Clock),
		middleware.TrustedProxy(proxies),
		middleware.RequestID(),
		middleware.AccessLog(middleware.AccessLogOptions{Logger: o.Logger, Clock: o.Clock, Recorder: s.ErrorRecorder}),
		middleware.SecurityHeaders(),
		middleware.CORS(o.Config),
		middleware.Gzip(),
	)

	registerPlatformRoutes(engine, o)

	v1 := "/api/v1"
	tokens := o.Tokens
	s.Routes = deps.Routes{
		Public: engine.Group(v1, middleware.PublicRateLimit(s.RateLimit)),
		Auth:   engine.Group(v1, middleware.Authenticate(tokens, s.Sessions), middleware.UserRateLimit(s.RateLimit)),
		Admin:  engine.Group(v1, middleware.Authenticate(tokens, s.Sessions), middleware.AdminOnly()),
	}

	engine.NoRoute(newFallback(o.Config, o.Logger))
	return s, nil
}

func registerPlatformRoutes(engine *gin.Engine, o Options) {
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": o.Build.Version})
	})

	engine.GET("/readyz", readiness(o))

	engine.GET("/api/v1/version", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		httpx.OK(c, gin.H{
			"product":   "Riochat",
			"version":   o.Build.Version,
			"commit":    o.Build.Commit,
			"buildTime": o.Build.BuildTime,
			"buildID":   o.Build.BuildID,
		})
	})

	engine.GET("/api/v1/capabilities", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=300")
		httpx.OK(c, gin.H{"features": o.Capabilities.Features()})
	})
}

// readiness implements GET /readyz.
func readiness(o Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if o.Drain.Draining() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "draining"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		checks := gin.H{}
		healthy := true

		switch {
		case o.DB == nil:
			checks["db"] = "not_configured"
			healthy = false
		default:
			pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
			if err := database.Ping(pctx, o.DB); err != nil {
				checks["db"] = "error"
				healthy = false
			} else {
				checks["db"] = "ok"
			}
			pcancel()
		}

		if o.Cache == nil {
			checks["cache"] = "not_configured"
			healthy = false
		} else {
			pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
			if err := o.Cache.Ping(pctx); err != nil {
				checks[o.Cache.Name()] = "error"
				healthy = false
			} else {
				checks[o.Cache.Name()] = "ok"
			}
			pcancel()
		}

		if healthy {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "checks": checks})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "checks": checks})
	}
}
