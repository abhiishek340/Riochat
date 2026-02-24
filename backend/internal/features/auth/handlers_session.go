package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

type loginReq struct {
	Username string `json:"username" binding:"required,min=3,max=128"`
	Password string `json:"password" binding:"required,min=6,max=128"`
}

type challengeStartReq struct {
	ChallengeToken string `json:"challengeToken" binding:"required,min=20,max=4096"`
}

type twoFactorVerifyReq struct {
	ChallengeToken     string `json:"challengeToken" binding:"required,min=20,max=4096"`
	VerificationMethod string `json:"verificationMethod" binding:"omitempty,oneof=two_factor email"`
	Code               string `json:"code" binding:"required,min=6,max=32"`
}

type locationReq struct {
	Latitude       float64  `json:"latitude" binding:"required"`
	Longitude      float64  `json:"longitude" binding:"required"`
	AccuracyMeters *float64 `json:"accuracyMeters" binding:"omitempty,min=0,max=1000000"`
	Timezone       string   `json:"timezone" binding:"max=64"`
}

type localExchangeReq struct {
	Grant string `json:"grant" binding:"required"`
}

func (h *handlers) loginOptions(c *gin.Context) {
	out, err := h.svc.LoginOptions(reqCtx(c))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) login(c *gin.Context) {
	var req loginReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	ctx := reqCtx(c)
	out, err := h.svc.Login(ctx, req.Username, req.Password)
	if err != nil {
		fail(c, err)
		return
	}
	if !out.Payload.TwoFactorRequired {
		h.svc.audit(ctx, out.Payload.User.ID, "login", "user", req.Username, map[string]any{"event": "user_login"})
	}
	h.deliver(c, out)
}

func (h *handlers) twoFactorEmailStart(c *gin.Context) {
	var req challengeStartReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.TwoFactorEmailStart(reqCtx(c), req.ChallengeToken)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) twoFactorVerify(c *gin.Context) {
	var req twoFactorVerifyReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.TwoFactorVerify(reqCtx(c), req.ChallengeToken, req.VerificationMethod, req.Code)
	if err != nil {
		fail(c, err)
		return
	}
	h.deliver(c, out)
}

func (h *handlers) refresh(c *gin.Context) {
	native := isNativeClient(c)
	out, err := h.svc.Refresh(reqCtx(c), readRefreshToken(c))
	if err != nil {
		if isErr(err, ErrInvalidRefreshToken) && !native {
			h.clearRefreshCookie(c)
		}
		fail(c, err)
		return
	}
	h.deliver(c, out)
}

func (h *handlers) logout(c *gin.Context) {
	p, ok := middleware.Principal(c)
	if !ok || p.UserID == 0 || p.SessionID == 0 {
		failStatus(c, http.StatusUnauthorized, ErrUnauthorized)
		return
	}
	if err := h.svc.Logout(reqCtx(c), p.UserID, p.SessionID); err != nil {
		fail(c, err)
		return
	}
	h.clearCookieForBrowsers(c)
	httpx.OK(c, gin.H{"revoked": true})
}

func (h *handlers) logoutAll(c *gin.Context) {
	p := middleware.MustPrincipal(c)
	if err := h.svc.LogoutAll(reqCtx(c), p.UserID); err != nil {
		fail(c, err)
		return
	}
	h.clearCookieForBrowsers(c)
	httpx.OK(c, gin.H{"revoked": true})
}

func (h *handlers) logoutSession(c *gin.Context) {
	p := middleware.MustPrincipal(c)
	id := strings.TrimSpace(c.Param("session_id"))
	if err := h.svc.LogoutSession(reqCtx(c), p.UserID, id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"revoked": true})
}

func (h *handlers) listSessions(c *gin.Context) {
	p := middleware.MustPrincipal(c)
	rows, err := h.svc.ListSessions(reqCtx(c), p.UserID, p.SessionID)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, int64(len(rows)), rows)
}

func (h *handlers) updateLocation(c *gin.Context) {
	var req locationReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	p := middleware.MustPrincipal(c)
	out, err := h.svc.UpdateLocation(reqCtx(c), p.UserID, p.SessionID, locationInput{
		Latitude: req.Latitude, Longitude: req.Longitude, AccuracyMeters: req.AccuracyMeters, Timezone: req.Timezone,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) localExchange(c *gin.Context) {
	var req localExchangeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.LocalExchange(reqCtx(c), req.Grant)
	if err != nil {
		fail(c, err)
		return
	}
	h.deliver(c, out)
}
