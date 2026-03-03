package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

type registerStartReq struct {
	Email          string `json:"email" binding:"required,max=128,email"`
	TurnstileToken string `json:"turnstileToken" binding:"max=2048"`
}

type registerCompleteReq struct {
	Email          string `json:"email" binding:"required,max=128,email"`
	Password       string `json:"password" binding:"required,min=8,max=128"`
	Code           string `json:"code" binding:"omitempty,len=6"`
	TurnstileToken string `json:"turnstileToken" binding:"max=2048"`
}

type resetStartReq struct {
	Email string `json:"email" binding:"required,max=128,email"`
}

type resetCompleteReq struct {
	Email       string `json:"email" binding:"required,max=128,email"`
	Code        string `json:"code" binding:"required,len=6"`
	NewPassword string `json:"newPassword" binding:"required,min=8,max=128"`
}

func (h *handlers) registerStart(c *gin.Context) {
	var req registerStartReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.RegisterStart(reqCtx(c), req.Email, req.TurnstileToken)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) registerComplete(c *gin.Context) {
	var req registerCompleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.RegisterComplete(reqCtx(c), registerInput{
		Email: req.Email, Password: req.Password, Code: req.Code, Turnstile: req.TurnstileToken,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.deliver(c, out)
}

func (h *handlers) resetStart(c *gin.Context) {
	var req resetStartReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.PasswordResetStart(reqCtx(c), req.Email)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) resetComplete(c *gin.Context) {
	var req resetCompleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := h.svc.PasswordResetComplete(reqCtx(c), req.Email, req.Code, req.NewPassword); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"changed": true})
}
