package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	refreshCookieName = "riochat_refresh_token"
	refreshCookiePath = "/api/v1/auth"
	platformHeader    = "X-Client-Platform"
)

// handlers carries the service into gin handlers.
type handlers struct{ svc *Service }

// reqCtx returns the request context with the client info attached so service
// code can record events with the right address and agent.
func reqCtx(c *gin.Context) context.Context {
	ci := clientInfo{
		AuditContext: middleware.AuditFromRequest(c.Request),
		RequestID:    reqctx.FromRequest(c.Request).RequestID(),
	}
	return withClient(c.Request.Context(), ci)
}

// fail answers with the status hint of a typed error (500 otherwise) and adds
// Retry-After for lock errors.
func fail(c *gin.Context, err error) {
	var le *lockedError
	if asLocked(err, &le) && le.retryAfter > 0 {
		c.Header("Retry-After", itoa(le.retryAfter))
	}
	httpx.FailErr(c, err)
}

func failStatus(c *gin.Context, status int, err error) { httpx.Fail(c, status, err) }

// ---- body binding ------------------------------------------------------------------

// bindOptional binds a JSON body but accepts a missing or empty one.
func bindOptional(c *gin.Context, dst any) bool {
	if c.Request.Body == nil {
		return true
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithCause(err).WithDetails(map[string]any{"reason": "invalid"}))
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return httpx.BindJSON(c, dst)
}

// invalidField fails with request.invalid_body for one field rule.
func invalidField(c *gin.Context, field, rule, param, message string) {
	fe := httpx.FieldError{Field: field, Rule: rule, Param: param, Message: message}
	httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.
		WithMessage("invalid request body: "+message).
		WithDetails(map[string]any{"reason": "validation", "fieldErrors": []httpx.FieldError{fe}}))
}

// ---- client platform and cookie -----------------------------------------------------------

// isWebOrigin reports whether origin is an http(s) page origin other than the
// desktop webview host.
func isWebOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return !strings.EqualFold(u.Hostname(), "tauri.localhost")
}

// isNativeClient: the platform header names desktop or mobile and the request
// does not come from a web page.
func isNativeClient(c *gin.Context) bool {
	p := strings.ToLower(strings.TrimSpace(c.GetHeader(platformHeader)))
	if p != "desktop" && p != "mobile" {
		return false
	}
	return !isWebOrigin(c.GetHeader("Origin"))
}

func (h *handlers) cookieSecure(c *gin.Context) bool {
	if c.Request.TLS != nil || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https") {
		return true
	}
	return h.svc.cfg().IsProd()
}

func (h *handlers) setRefreshCookie(c *gin.Context, value string, expires time.Time) {
	maxAge := int(expires.Sub(h.svc.now()).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: refreshCookieName, Value: value, Path: refreshCookiePath, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure(c), Expires: expires, MaxAge: maxAge,
	})
}

func (h *handlers) clearRefreshCookie(c *gin.Context) {
	// A negative MaxAge is rendered by net/http as "Max-Age=0".
	http.SetCookie(c.Writer, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: refreshCookiePath, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure(c), MaxAge: -1,
		Expires: time.Unix(0, 0).UTC(),
	})
}

// clearCookieForBrowsers clears the refresh cookie unless the client is native.
func (h *handlers) clearCookieForBrowsers(c *gin.Context) {
	if !isNativeClient(c) {
		h.clearRefreshCookie(c)
	}
}

// deliver writes a sign-in answer, handing the refresh token to the client the
// way its platform expects.
func (h *handlers) deliver(c *gin.Context, out signIn) {
	p := out.Payload
	if out.Refresh != "" {
		if isNativeClient(c) {
			p.RefreshToken = out.Refresh
		} else {
			h.setRefreshCookie(c, out.Refresh, p.RefreshExpiresAt)
		}
	}
	httpx.OK(c, p)
}

// readRefreshToken extracts the refresh token for the client platform: the body
// for native clients, the cookie for browsers, never the other source.
func readRefreshToken(c *gin.Context) string {
	if isNativeClient(c) {
		if c.Request.Body == nil {
			return ""
		}
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<16))
		if err != nil {
			return ""
		}
		var body struct {
			RefreshToken string `json:"refreshToken"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return ""
		}
		return strings.TrimSpace(body.RefreshToken)
	}
	v, err := c.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// ---- caller ------------------------------------------------------------------------------------

// caller returns the signed-in user row, answering 401 when it is gone.
func (h *handlers) caller(c *gin.Context) (*User, bool) {
	p, ok := middleware.Principal(c)
	if !ok || p.UserID == 0 {
		failStatus(c, http.StatusUnauthorized, ErrUnauthorized)
		return nil, false
	}
	u, err := h.svc.userByID(reqCtx(c), p.UserID)
	if err != nil {
		if isNotFoundErr(err) {
			failStatus(c, http.StatusUnauthorized, ErrUnauthorized)
			return nil, false
		}
		fail(c, err)
		return nil, false
	}
	return u, true
}
