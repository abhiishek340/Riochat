package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	rateWindow = time.Minute
	rateTTL    = 2 * time.Minute
	apiPrefix  = "/api/v1"
)

// RateLimitOptions wires the rate-limit middlewares.
type RateLimitOptions struct {
	Config  *config.Holder
	Limiter cache.RateLimiter
	Clock   clock.Clock
	Logger  *logx.Logger
}

func (o RateLimitOptions) clock() clock.Clock {
	if o.Clock == nil {
		return clock.System{}
	}
	return o.Clock
}

func (o RateLimitOptions) logger() *logx.Logger {
	if o.Logger == nil {
		return logx.Nop()
	}
	return o.Logger
}

func routeName(c *gin.Context) string {
	route := c.FullPath()
	if route == "" {
		route = c.Request.URL.Path
	}
	if trimmed := strings.TrimPrefix(route, apiPrefix); trimmed != "" {
		return trimmed
	}
	return route
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// UserPolicy picks the policy name and per-minute limit for an authenticated
// request. base is the configured authenticated requests-per-minute.
func UserPolicy(method, route string, base int) (name string, limit int) {
	if base <= 0 {
		base = config.DefaultAuthRPM
	}
	post := method == http.MethodPost
	switch {
	case post && (strings.Contains(route, "/media/images/") || strings.Contains(route, "/media/videos/")):
		return "media_generation", maxInt(base/2, 30)
	case post && (strings.HasSuffix(route, "/messages") || strings.HasSuffix(route, "/messages/stream") || strings.HasSuffix(route, "/cancel")):
		return "message_generation", maxInt(base, 60)
	case post && route == "/files":
		return "file_upload", maxInt(base*2, 120)
	case post && (route == "/files/processing/statuses" || route == "/conversation-runs/statuses" || strings.HasSuffix(route, "/files/processing/statuses")):
		return "polling", maxInt(base*10, 600)
	case method == http.MethodGet && (strings.HasSuffix(route, "/processing") || strings.HasSuffix(route, "/runs") || strings.HasSuffix(route, "/stream")):
		return "polling", maxInt(base*10, 600)
	case method == http.MethodGet:
		return "read", maxInt(base*10, 600)
	case post || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete:
		return "write", maxInt(base*4, 240)
	default:
		return "general", maxInt(base*6, 360)
	}
}

// PublicPolicy picks the policy for an unauthenticated request. base is the
// configured public requests-per-minute.
func PublicPolicy(method, route string, base int) (name string, limit int, denied *apperr.Error) {
	if base <= 0 {
		base = config.DefaultPublicRPM
	}
	switch {
	case route == "/auth/refresh":
		return "auth_refresh", maxInt(base*10, 300), apperr.ErrRefreshLimited
	case (method == http.MethodGet && route == "/auth/login-options") || strings.HasSuffix(route, "/logo"):
		return "public_auth_read", maxInt(base*10, 300), apperr.ErrRateLimited
	case strings.HasPrefix(route, "/auth/"):
		return "public_auth", base, apperr.ErrAuthLimited
	case strings.HasPrefix(route, "/billing/payments/"):
		return "payment_callback", maxInt(base*20, 600), apperr.ErrRateLimited
	case route == "/settings/login-page" || strings.HasPrefix(route, "/shared-conversations/"):
		return "public_read", maxInt(base*20, 600), apperr.ErrRateLimited
	default:
		return "public", maxInt(base*10, 300), apperr.ErrRateLimited
	}
}

func deny(c *gin.Context, now time.Time, limit int, e *apperr.Error) {
	c.Header("Retry-After", "60")
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", "0")
	c.Header("X-RateLimit-Reset", strconv.FormatInt(now.Add(rateWindow).Unix(), 10))
	httpx.Fail(c, http.StatusTooManyRequests, e)
}

// UserRateLimit limits authenticated requests per user with a sliding window
// (key ratelimit:user:<userID>:<policy>). It is a no-op unless the dynamic
// rate-limit switch is on; administrators and unauthenticated contexts are
// exempt; limiter storage errors allow the request.
func UserRateLimit(o RateLimitOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := o.Config.Load().Dynamic.Auth
		if !cfg.RateLimitEnabled || o.Limiter == nil {
			c.Next()
			return
		}
		p, ok := reqctx.FromRequest(c.Request).Principal()
		if !ok || p.IsAdmin() {
			c.Next()
			return
		}
		policy, limit := UserPolicy(c.Request.Method, routeName(c), cfg.RateLimitAuthRPM)
		key := fmt.Sprintf("ratelimit:user:%d:%s", p.UserID, policy)
		allowed, err := o.Limiter.AllowSliding(c.Request.Context(), key, limit, rateWindow, rateTTL)
		if err != nil {
			o.logger().Warn("rate_limit_storage_error", logx.F("policy", policy), logx.Err(err))
			c.Next()
			return
		}
		if !allowed {
			deny(c, o.clock().Now(), limit, apperr.ErrRateLimited)
			return
		}
		c.Next()
	}
}

// PublicRateLimit limits unauthenticated requests per client IP with a fixed
// window (key ratelimit:public:<policy>:<route>:ip:<ip>; the refresh policy
// uses the shared key ratelimit:token-refresh:ip:<ip>).
func PublicRateLimit(o RateLimitOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := o.Config.Load().Dynamic.Auth
		if !cfg.RateLimitEnabled || o.Limiter == nil {
			c.Next()
			return
		}
		route := routeName(c)
		policy, limit, denied := PublicPolicy(c.Request.Method, route, cfg.RateLimitPublicRPM)
		ip := reqctx.FromRequest(c.Request).ClientIP()
		if ip == "" {
			ip = "unknown"
		}
		key := fmt.Sprintf("ratelimit:public:%s:%s:ip:%s", policy, route, ip)
		if policy == "auth_refresh" {
			key = "ratelimit:token-refresh:ip:" + ip
		}
		allowed, err := o.Limiter.AllowFixed(c.Request.Context(), []string{key}, limit, rateTTL)
		if err != nil {
			o.logger().Warn("rate_limit_storage_error", logx.F("policy", policy), logx.Err(err))
			c.Next()
			return
		}
		if !allowed {
			deny(c, o.clock().Now(), limit, denied)
			return
		}
		c.Next()
	}
}

// UpstreamProbeLimit protects endpoints that make the server call
// user-supplied addresses (testing or saving a personal provider). Per user,
// sliding window, key ratelimit:user:<userID>:upstream_probe. It ignores the
// global enable switch and applies to administrators too; it is skipped only
// when unauthenticated or when no limiter exists.
func UpstreamProbeLimit(o RateLimitOptions, perMinute int) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := reqctx.FromRequest(c.Request).Principal()
		if !ok || o.Limiter == nil {
			c.Next()
			return
		}
		key := fmt.Sprintf("ratelimit:user:%d:upstream_probe", p.UserID)
		allowed, err := o.Limiter.AllowSliding(c.Request.Context(), key, perMinute, rateWindow, rateTTL)
		if err != nil {
			o.logger().Warn("rate_limit_storage_error", logx.F("policy", "upstream_probe"), logx.Err(err))
			c.Next()
			return
		}
		if !allowed {
			deny(c, o.clock().Now(), perMinute, apperr.ErrRateLimited)
			return
		}
		c.Next()
	}
}
