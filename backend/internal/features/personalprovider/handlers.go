package personalprovider

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

const auditResource = "personal_providers"

type handlers struct {
	svc   *Service
	audit Auditor
}

// fail answers with the status of a typed error of this area, or 500.
func fail(c *gin.Context, err error) {
	status := statusFor(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	httpx.Fail(c, status, err)
}

func (h *handlers) record(c *gin.Context, action, resourceID string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	h.audit.RecordRequest(c, action, auditResource, resourceID, detail)
}

func userID(c *gin.Context) uint { return middleware.MustPrincipal(c).UserID }

// ---- user endpoints --------------------------------------------------------------

func (h *handlers) access(c *gin.Context) {
	info, err := h.svc.Access(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, info)
}

func (h *handlers) list(c *gin.Context) {
	views, err := h.svc.List(c.Request.Context(), userID(c))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"providers": views})
}

type probeRequest struct {
	Protocol string `json:"protocol" binding:"required,max=64"`
	BaseURL  string `json:"baseURL" binding:"required,max=512"`
	APIKey   string `json:"apiKey" binding:"required,max=512"`
}

func (h *handlers) probe(c *gin.Context) {
	var req probeRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	models, err := h.svc.Probe(c.Request.Context(), userID(c), ProbeInput{Protocol: req.Protocol, BaseURL: req.BaseURL, APIKey: req.APIKey})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"models": models})
}

type createRequest struct {
	Name     string       `json:"name" binding:"max=64"`
	Icon     string       `json:"icon" binding:"max=64"`
	Protocol string       `json:"protocol" binding:"required,max=64"`
	BaseURL  string       `json:"baseURL" binding:"required,max=512"`
	APIKey   string       `json:"apiKey" binding:"required,max=512"`
	Models   []ModelInput `json:"models" binding:"max=200,dive"`
	Source   string       `json:"source" binding:"omitempty,oneof=manual link"`
}

func (h *handlers) create(c *gin.Context) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	p, err := h.svc.Create(c.Request.Context(), userID(c), CreateInput{
		Name: req.Name, Icon: req.Icon, Protocol: req.Protocol, BaseURL: req.BaseURL,
		APIKey: req.APIKey, Models: req.Models, Source: req.Source,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, "personal_provider.create", p.PublicID, map[string]any{
		"host": p.Host, "protocol": p.Protocol, "source": p.Source, "model_count": len(p.Models()),
	})
	httpx.OK(c, gin.H{"provider": viewOf(*p)})
}

type updateRequest struct {
	Name    *string       `json:"name" binding:"omitempty,max=64"`
	Icon    *string       `json:"icon" binding:"omitempty,max=64"`
	APIKey  *string       `json:"apiKey" binding:"omitempty,max=512"`
	Models  *[]ModelInput `json:"models" binding:"omitempty,max=200,dive"`
	Enabled *bool         `json:"enabled"`
}

func (h *handlers) update(c *gin.Context) {
	var req updateRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	res, err := h.svc.Update(c.Request.Context(), userID(c), c.Param("id"), UpdateInput{
		Name: req.Name, Icon: req.Icon, APIKey: req.APIKey, Models: req.Models, Enabled: req.Enabled,
	})
	if err != nil {
		fail(c, err)
		return
	}
	p := res.Provider
	h.record(c, "personal_provider.update", p.PublicID, map[string]any{
		"host": p.Host, "protocol": p.Protocol,
		"key_rotated": res.KeyRotated, "models_changed": res.ModelsChanged, "enabled_changed": res.EnabledChanged,
	})
	httpx.OK(c, gin.H{"provider": viewOf(*p)})
}

func (h *handlers) remove(c *gin.Context) {
	p, err := h.svc.Delete(c.Request.Context(), userID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, "personal_provider.delete", p.PublicID, map[string]any{"host": p.Host, "protocol": p.Protocol})
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *handlers) models(c *gin.Context) {
	models, _, err := h.svc.Models(c.Request.Context(), userID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"models": models})
}

// ---- administrator endpoints ---------------------------------------------------------

func (h *handlers) adminList(c *gin.Context) {
	q := c.Request.URL.Query()
	f := AdminFilter{
		Query:  strings.TrimSpace(q.Get("q")),
		Status: strings.TrimSpace(q.Get("status")),
		Host:   strings.TrimSpace(q.Get("host")),
	}
	if raw := strings.TrimSpace(q.Get("owner_user_id")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			httpx.InvalidQuery(c, "owner_user_id")
			return
		}
		id := uint(n)
		f.OwnerUserID = &id
	}
	rows, total, err := h.svc.AdminList(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, rows)
}

type suspendRequest struct {
	IDs       []string `json:"ids" binding:"required,min=1,max=500"`
	Suspended *bool    `json:"suspended" binding:"required"`
}

// joinIDs renders ids for the audit resource id, truncated to 255 characters.
func joinIDs(ids []string) string {
	s := strings.Join(ids, ",")
	if len(s) > 255 {
		s = s[:255]
	}
	return s
}

func (h *handlers) adminSuspend(c *gin.Context) {
	var req suspendRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	n, ids, err := h.svc.AdminSetSuspended(c.Request.Context(), req.IDs, *req.Suspended)
	if err != nil {
		fail(c, err)
		return
	}
	action := "personal_provider.admin_resume"
	if *req.Suspended {
		action = "personal_provider.admin_suspend"
	}
	h.record(c, action, joinIDs(ids), map[string]any{"affected": n})
	httpx.OK(c, gin.H{"affected": n})
}

type suspendHostRequest struct {
	Host string `json:"host" binding:"required,max=255"`
}

func (h *handlers) adminSuspendHost(c *gin.Context) {
	var req suspendHostRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	n, host, err := h.svc.AdminSuspendHost(c.Request.Context(), req.Host)
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, "personal_provider.admin_suspend_host", host, map[string]any{"affected": n})
	httpx.OK(c, gin.H{"affected": n})
}

type batchDeleteRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=500"`
}

func (h *handlers) adminBatchDelete(c *gin.Context) {
	var req batchDeleteRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	n, ids, err := h.svc.AdminDelete(c.Request.Context(), req.IDs)
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, "personal_provider.admin_delete", joinIDs(ids), map[string]any{"affected": n})
	httpx.OK(c, gin.H{"affected": n})
}
