package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

type settingsResponse struct {
	Settings map[string]string `json:"settings"`
}

type patchSettingsRequest struct {
	Settings map[string]string `json:"settings" binding:"required"`
}

func getSettingsHandler(store *SettingsStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		all, err := store.All(c.Request.Context(), middleware.MustPrincipal(c).UserID)
		if err != nil {
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		httpx.OK(c, settingsResponse{Settings: all})
	}
}

func patchSettingsHandler(store *SettingsStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req patchSettingsRequest
		if !httpx.BindJSON(c, &req) {
			return
		}
		all, err := store.Patch(c.Request.Context(), middleware.MustPrincipal(c).UserID, req.Settings)
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, settingsResponse{Settings: all})
	}
}
