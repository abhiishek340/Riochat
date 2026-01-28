package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets baseline response headers when a handler has not.
func SecurityHeaders() gin.HandlerFunc {
	defaults := [][2]string{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Permissions-Policy", "camera=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), browsing-topics=()"},
	}
	return func(c *gin.Context) {
		h := c.Writer.Header()
		for _, kv := range defaults {
			if h.Get(kv[0]) == "" {
				h.Set(kv[0], kv[1])
			}
		}
		c.Next()
	}
}
