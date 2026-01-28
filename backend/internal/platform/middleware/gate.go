package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// FeatureGate guards a route group behind a capability flag. When the flag is
// off the group answers 404 feature.disabled with details {"feature": flag}.
// The flag is read on every request. An unknown flag name panics at
// registration time, so a misspelling aborts startup instead of silently
// disabling a route group.
func FeatureGate(caps capability.Provider, flag string) gin.HandlerFunc {
	capability.MustBeKnown(flag)
	return func(c *gin.Context) {
		if !caps.Enabled(flag) {
			httpx.Fail(c, http.StatusNotFound, apperr.ErrFeatureDisabled.WithDetails(map[string]any{"feature": flag}))
			return
		}
		c.Next()
	}
}
