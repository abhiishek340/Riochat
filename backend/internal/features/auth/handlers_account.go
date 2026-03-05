package auth

import (
	"strconv"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

type patchMeReq struct {
	AvatarURL             *string `json:"avatarURL" binding:"omitempty,max=2048"`
	DisplayName           *string `json:"displayName" binding:"omitempty,max=16"`
	Timezone              *string `json:"timezone" binding:"omitempty,max=64"`
	Locale                *string `json:"locale" binding:"omitempty,max=16"`
	ProfilePreferences    *string `json:"profilePreferences" binding:"omitempty,max=1024"`
	AppearancePreferences *string `json:"appearancePreferences" binding:"omitempty,max=2048"`
}

type usernameReq struct {
	Username string `json:"username" binding:"required,min=3,max=16"`
}

type onboardingReq struct {
	NewPassword string `json:"newPassword" binding:"omitempty,min=8,max=128"`
}

type methodReq struct {
	VerificationMethod string `json:"verificationMethod" binding:"omitempty,oneof=none two_factor email"`
}

type deleteReq struct {
	VerificationMethod string `json:"verificationMethod" binding:"omitempty,oneof=none two_factor email"`
	Code               string `json:"code" binding:"required,min=6,max=32"`
}

type passwordChangeReq struct {
	CurrentPassword    string `json:"currentPassword" binding:"max=128"`
	NewPassword        string `json:"newPassword" binding:"required,min=8,max=128"`
	VerificationMethod string `json:"verificationMethod" binding:"omitempty,oneof=none two_factor email"`
	Code               string `json:"code" binding:"omitempty,min=6,max=32"`
}

type emailOnlyReq struct {
	Email string `json:"email" binding:"required,max=128,email"`
}

type bootstrapCompleteReq struct {
	Email string `json:"email" binding:"required,max=128,email"`
	Code  string `json:"code" binding:"omitempty,len=6"`
}

type codeOnlyReq struct {
	Code string `json:"code" binding:"required,len=6"`
}

type emailChangeCompleteReq struct {
	Email                     string `json:"email" binding:"required,max=128,email"`
	CurrentVerificationMethod string `json:"currentVerificationMethod" binding:"omitempty,oneof=none two_factor email"`
	CurrentCode               string `json:"currentCode" binding:"omitempty,min=6,max=32"`
	NewCode                   string `json:"newCode" binding:"omitempty,len=6"`
}

type mfaCodeReq struct {
	Code string `json:"code" binding:"required,min=6,max=32"`
}

func (h *handlers) userEnvelope(c *gin.Context, u *User) {
	view, err := h.svc.viewUser(reqCtx(c), u)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"user": view})
}

func (h *handlers) me(c *gin.Context) {
	u, ok := h.caller(c)
	if !ok {
		return
	}
	h.userEnvelope(c, u)
}

func (h *handlers) patchMe(c *gin.Context) {
	var req patchMeReq
	if !httpx.BindJSONPlain(c, &req) {
		return
	}
	if req.DisplayName != nil {
		if n := utf8.RuneCountInString(*req.DisplayName); n > 0 && n < 3 {
			invalidField(c, "displayName", "min", "3", "displayName must be at least 3")
			return
		}
	}
	p := middleware.MustPrincipal(c)
	ctx := reqCtx(c)
	u, fields, err := h.svc.UpdateProfile(ctx, p.UserID, profileInput{
		AvatarURL: req.AvatarURL, DisplayName: req.DisplayName, Timezone: req.Timezone, Locale: req.Locale,
		ProfilePreferences: req.ProfilePreferences, AppearancePreferences: req.AppearancePreferences,
	})
	if err != nil {
		fail(c, err)
		return
	}
	if fields == nil {
		fields = []string{}
	}
	h.svc.audit(ctx, u.ID, "update_profile", "user", strconv.FormatUint(uint64(u.ID), 10), map[string]any{"fields": fields})
	h.userEnvelope(c, u)
}

func (h *handlers) patchUsername(c *gin.Context) {
	var req usernameReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	p := middleware.MustPrincipal(c)
	ctx := reqCtx(c)
	u, err := h.svc.UpdateUsername(ctx, p.UserID, req.Username)
	if err != nil {
		fail(c, err)
		return
	}
	h.svc.audit(ctx, u.ID, "update_username", "user", strconv.FormatUint(uint64(u.ID), 10), map[string]any{"username": u.Username})
	h.userEnvelope(c, u)
}

func (h *handlers) onboardingComplete(c *gin.Context) {
	var req onboardingReq
	if !bindOptional(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	changed, err := h.svc.OnboardingComplete(reqCtx(c), u, req.NewPassword)
	if err != nil {
		fail(c, err)
		return
	}
	if changed {
		h.clearCookieForBrowsers(c)
	}
	h.userEnvelope(c, u)
}

// ---- deletion ---------------------------------------------------------------------

func (h *handlers) deleteStart(c *gin.Context) {
	var req methodReq
	if !bindOptional(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.AccountDeleteStart(reqCtx(c), u, req.VerificationMethod)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) deleteAccount(c *gin.Context) {
	var req deleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	ctx := reqCtx(c)
	if err := h.svc.AccountDelete(ctx, u, req.VerificationMethod, req.Code); err != nil {
		fail(c, err)
		return
	}
	h.svc.audit(ctx, u.ID, "delete_account", "user", u.PublicID, map[string]any{"deleted": true})
	h.clearCookieForBrowsers(c)
	httpx.OK(c, gin.H{"deleted": true})
}

// ---- password change ---------------------------------------------------------------

func (h *handlers) passwordChangeStart(c *gin.Context) {
	var req methodReq
	if !bindOptional(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.PasswordChangeStart(reqCtx(c), u, req.VerificationMethod)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) passwordChangeComplete(c *gin.Context) {
	var req passwordChangeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	err := h.svc.PasswordChangeComplete(reqCtx(c), u, passwordChangeInput{
		CurrentPassword: req.CurrentPassword, NewPassword: req.NewPassword,
		Method: req.VerificationMethod, Code: req.Code,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.clearCookieForBrowsers(c)
	httpx.OK(c, gin.H{"changed": true})
}

// ---- email management --------------------------------------------------------------------

func (h *handlers) emailBootstrapStart(c *gin.Context) {
	var req emailOnlyReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.EmailBootstrapStart(reqCtx(c), u, req.Email)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) emailBootstrapComplete(c *gin.Context) {
	var req bootstrapCompleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	fresh, err := h.svc.EmailBootstrapComplete(reqCtx(c), u, req.Email, req.Code)
	if err != nil {
		fail(c, err)
		return
	}
	h.userEnvelope(c, fresh)
}

func (h *handlers) emailVerifyCurrentStart(c *gin.Context) {
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.EmailVerifyCurrentStart(reqCtx(c), u)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) emailVerifyCurrentComplete(c *gin.Context) {
	var req codeOnlyReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	fresh, err := h.svc.EmailVerifyCurrentComplete(reqCtx(c), u, req.Code)
	if err != nil {
		fail(c, err)
		return
	}
	h.userEnvelope(c, fresh)
}

func (h *handlers) emailChangeStartCurrent(c *gin.Context) {
	var req methodReq
	if !bindOptional(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.EmailChangeStartCurrent(reqCtx(c), u, req.VerificationMethod)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) emailChangeStartNew(c *gin.Context) {
	var req emailOnlyReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.EmailChangeStartNew(reqCtx(c), u, req.Email)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) emailChangeComplete(c *gin.Context) {
	var req emailChangeCompleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	u, ok := h.caller(c)
	if !ok {
		return
	}
	fresh, err := h.svc.EmailChangeComplete(reqCtx(c), u, emailChangeInput{
		Email: req.Email, CurrentMethod: req.CurrentVerificationMethod,
		CurrentCode: req.CurrentCode, NewCode: req.NewCode,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.userEnvelope(c, fresh)
}

// ---- two-factor management -----------------------------------------------------------------

func (h *handlers) mfaStatus(c *gin.Context) {
	out, err := h.svc.MFAStatus(reqCtx(c), middleware.MustPrincipal(c).UserID)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) mfaSetupStart(c *gin.Context) {
	u, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.SetupStart(reqCtx(c), u)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) mfaSetupConfirm(c *gin.Context) {
	var req mfaCodeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.SetupConfirm(reqCtx(c), middleware.MustPrincipal(c).UserID, req.Code)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) mfaSetupCancel(c *gin.Context) {
	if err := h.svc.CancelSetup(reqCtx(c), middleware.MustPrincipal(c).UserID); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"canceled": true})
}

func (h *handlers) mfaRegenerate(c *gin.Context) {
	var req mfaCodeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.RegenerateRecovery(reqCtx(c), middleware.MustPrincipal(c).UserID, req.Code)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) mfaDisable(c *gin.Context) {
	var req mfaCodeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := h.svc.DisableMFA(reqCtx(c), middleware.MustPrincipal(c).UserID, req.Code); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"disabled": true})
}
