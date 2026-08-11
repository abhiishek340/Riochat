package mcp

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// Mount registers the user and administrator routes with default ports.
func Mount(d deps.Deps) error {
	_, err := MountService(d, Ports{})
	return err
}

// MountService is Mount with explicit ports; it returns the service so the
// integrator can hand it to the chat runtime.
func MountService(d deps.Deps, p Ports) (*Service, error) {
	svc := NewService(d, p)
	h := &handlers{svc: svc}

	d.Routes.Auth.GET("/mcp/tools", h.listEnabled)
	d.Routes.Auth.GET("/settings/mcp-policy", h.policy)

	a := d.Routes.Admin
	a.GET("/admin/mcp/servers", h.listServers)
	a.POST("/admin/mcp/servers", h.createServer)
	a.PATCH("/admin/mcp/servers/order", h.reorder)
	a.PATCH("/admin/mcp/servers/:id", h.updateServer)
	a.DELETE("/admin/mcp/servers/:id", h.deleteServer)
	a.GET("/admin/mcp/servers/:id/tools", h.listServerTools)
	a.PATCH("/admin/mcp/servers/:id/tools/status", h.setToolsStatus)
	a.POST("/admin/mcp/servers/:id/sync", h.syncServer)
	a.PATCH("/admin/mcp/tools/:id", h.updateTool)

	if d.Lifecycle != nil {
		d.Lifecycle.OnStop("mcp-client", func(context.Context) error {
			svc.Close()
			return nil
		})
	}
	return svc, nil
}

// Close releases idle connections to remote servers.
func (s *Service) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

type handlers struct{ svc *Service }

// ---- wire shapes ----

type serverView struct {
	ID                                   uint       `json:"id"`
	Name                                 string     `json:"name"`
	BaseURL                              string     `json:"baseURL"`
	HeadersJSON                          string     `json:"headersJSON"`
	Status                               string     `json:"status"`
	SortOrder                            int        `json:"sortOrder"`
	ToolCount                            int        `json:"toolCount"`
	ActiveToolCount                      int        `json:"activeToolCount"`
	RequiresToolMetadataSyncConfirmation bool       `json:"requiresToolMetadataSyncConfirmation"`
	LastSyncedAt                         *time.Time `json:"lastSyncedAt"`
	LastError                            string     `json:"lastError"`
	CreatedAt                            time.Time  `json:"createdAt"`
	UpdatedAt                            time.Time  `json:"updatedAt"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.Local()
	return &v
}

func toServerView(r ServerRow) serverView {
	return serverView{
		ID: r.ID, Name: r.Name, BaseURL: r.BaseURL, HeadersJSON: redactHeaders(r.HeadersJSON),
		Status: r.Status, SortOrder: r.SortOrder, ToolCount: r.ToolCount, ActiveToolCount: r.ActiveToolCount,
		RequiresToolMetadataSyncConfirmation: r.RequiresToolMetadataSyncConfirmation,
		LastSyncedAt:                         utcPtr(r.LastSyncedAt), LastError: r.LastError,
		CreatedAt: r.CreatedAt.Local(), UpdatedAt: r.UpdatedAt.Local(),
	}
}

type toolView struct {
	ID                       uint      `json:"id"`
	ServerID                 uint      `json:"serverID"`
	ServerName               string    `json:"serverName"`
	Name                     string    `json:"name"`
	DisplayName              string    `json:"displayName"`
	Description              string    `json:"description"`
	InputSchemaJSON          string    `json:"inputSchemaJSON"`
	AttachmentInputMode      string    `json:"attachmentInputMode"`
	AttachmentArgument       string    `json:"attachmentArgument"`
	AttachmentEncoding       string    `json:"attachmentEncoding"`
	AttachmentPromptArgument string    `json:"attachmentPromptArgument"`
	PriceNanoUSD             int64     `json:"priceNanousd"`
	Status                   string    `json:"status"`
	SortOrder                int       `json:"sortOrder"`
	CreatedAt                time.Time `json:"createdAt"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

func toToolView(t Tool, serverName string) toolView {
	return toolView{
		ID: t.ID, ServerID: t.ServerID, ServerName: serverName, Name: t.Name, DisplayName: t.DisplayName,
		Description: t.Description, InputSchemaJSON: t.InputSchemaJSON, AttachmentInputMode: t.AttachmentInputMode,
		AttachmentArgument: t.AttachmentArgument, AttachmentEncoding: t.AttachmentEncoding,
		AttachmentPromptArgument: t.AttachmentPromptArgument, PriceNanoUSD: t.PriceNanoUSD, Status: t.Status,
		SortOrder: t.SortOrder, CreatedAt: t.CreatedAt.Local(), UpdatedAt: t.UpdatedAt.Local(),
	}
}

func toolViews(tools []Tool) []toolView {
	out := make([]toolView, 0, len(tools))
	for _, t := range tools {
		out = append(out, toToolView(t, ""))
	}
	return out
}

func fail(c *gin.Context, err error) { httpx.Fail(c, statusFor(err), err) }

func pathID(c *gin.Context, bad error) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || n > math.MaxInt64 {
		httpx.Fail(c, http.StatusBadRequest, bad)
		return 0, false
	}
	return uint(n), true
}

// ---- user endpoints ----

func (h *handlers) listEnabled(c *gin.Context) {
	rows, err := h.svc.ListEnabledTools(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]toolView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toToolView(r.Tool, r.ServerName))
	}
	httpx.OK(c, gin.H{"results": out})
}

func (h *handlers) policy(c *gin.Context) {
	httpx.OK(c, gin.H{"maxSelectedToolsPerMessage": h.svc.runtime(c.Request.Context()).MaxSelectedToolsPerMessage})
}

// ---- administrator: servers ----

func (h *handlers) listServers(c *gin.Context) {
	rows, err := h.svc.ListServers(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]serverView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toServerView(r))
	}
	httpx.OK(c, gin.H{"results": out})
}
