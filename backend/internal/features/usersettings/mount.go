package usersettings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

// settingValue is a JSON string that, unlike a plain string, refuses null.
type settingValue string

func (v *settingValue) UnmarshalJSON(raw []byte) error {
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(s)}
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	*v = settingValue(s)
	return nil
}

type updateRequest struct {
	Settings map[string]settingValue `json:"settings" binding:"required"`
}

type settingsResponse struct {
	Settings map[string]string `json:"settings"`
}

// Mount registers GET and PATCH /user/settings on the authenticated group.
func Mount(d deps.Deps) error {
	// Without a shared cache there is nothing to notify; writes still succeed.
	var notifier Notifier
	if d.Cache != nil {
		notifier = NewValueCache(d.DB, d.Cache)
	}
	svc := NewService(d.DB, d.Clock, d.Logger, notifier)

	d.Routes.Auth.GET("/user/settings", func(c *gin.Context) {
		out, err := svc.Get(c.Request.Context(), middleware.MustPrincipal(c).UserID)
		if err != nil {
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		httpx.OK(c, settingsResponse{Settings: out})
	})

	d.Routes.Auth.PATCH("/user/settings", func(c *gin.Context) {
		var req updateRequest
		if !httpx.BindJSON(c, &req) {
			return
		}
		entries := make(map[string]string, len(req.Settings))
		for k, v := range req.Settings {
			entries[k] = string(v)
		}
		out, err := svc.Update(c.Request.Context(), middleware.MustPrincipal(c).UserID, entries)
		if err != nil {
			status := http.StatusInternalServerError
			if isClientError(err) {
				status = http.StatusBadRequest
			}
			httpx.Fail(c, status, err)
			return
		}
		httpx.OK(c, settingsResponse{Settings: out})
	})
	return nil
}
