package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// desktopOrigins are always allowed; browsers cannot forge these schemes.
var desktopOrigins = []string{"tauri://localhost", "http://tauri.localhost"}

// CORS enforces the origin allowlist from the live configuration.
//
// An empty allowlist means "*". A request carrying an Origin that matches
// nothing is answered 403 cors.origin_forbidden. OPTIONS requests are answered
// 204 without reaching handlers.
func CORS(cfg *config.Holder) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed := normalizeOrigins(cfg.Load().CORSOrigins())
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID,X-Client-Platform")
		h.Set("Access-Control-Expose-Headers", "X-Request-ID")
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Access-Control-Max-Age", "86400")

		if origin := c.GetHeader("Origin"); origin != "" {
			echo, ok := matchOrigin(allowed, origin)
			if !ok {
				for _, k := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers",
					"Access-Control-Expose-Headers", "Access-Control-Allow-Credentials", "Access-Control-Max-Age"} {
					h.Del(k)
				}
				httpx.Fail(c, http.StatusForbidden, apperr.ErrOriginForbidden)
				return
			}
			h.Set("Access-Control-Allow-Origin", echo)
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func normalizeOrigins(entries []string) []string {
	out := make([]string, 0, len(entries)+len(desktopOrigins))
	for _, e := range entries {
		if e = strings.TrimRight(strings.TrimSpace(e), "/"); e != "" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = append(out, "*")
	}
	return append(out, desktopOrigins...)
}

// matchOrigin returns the value for Access-Control-Allow-Origin.
func matchOrigin(allowed []string, origin string) (string, bool) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	for _, a := range allowed {
		if a == "*" {
			return origin, true
		}
		if strings.EqualFold(a, origin) {
			return a, true
		}
	}
	return "", false
}
