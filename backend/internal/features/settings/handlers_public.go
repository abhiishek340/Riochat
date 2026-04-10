package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

type handlers struct {
	svc *Service
	cfg *config.Holder
}

func actorOf(c *gin.Context) Actor {
	p := middleware.MustPrincipal(c)
	st := reqctx.FromRequest(c.Request)
	return Actor{UserID: p.UserID, RequestID: st.RequestID(), ClientIP: st.ClientIP(), UserAgent: c.Request.UserAgent()}
}

// ---- branding ------------------------------------------------------------------------

type brandingView struct {
	Title                 string `json:"title"`
	ShortName             string `json:"shortName"`
	Description           string `json:"description"`
	LogoURL               string `json:"logoURL"`
	FaviconURL            string `json:"faviconURL"`
	PWAIcon192URL         string `json:"pwaIcon192URL"`
	PWAIcon512URL         string `json:"pwaIcon512URL"`
	PWAMaskableIcon512URL string `json:"pwaMaskableIcon512URL"`
	AppleTouchIcon180URL  string `json:"appleTouchIcon180URL"`
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// branding resolves the configured branding with defaults for blank values.
func branding(b config.BrandingConfig) brandingView {
	return brandingView{
		Title:                 orDefault(b.Title, "Riochat"),
		ShortName:             orDefault(b.ShortName, "Riochat"),
		Description:           orDefault(b.Description, "Riochat is a multi-model AI conversation system."),
		LogoURL:               strings.TrimSpace(b.LogoURL),
		FaviconURL:            orDefault(b.FaviconURL, "/favicon.ico"),
		PWAIcon192URL:         orDefault(b.PWAIcon192URL, "/pwa/icon-192.png"),
		PWAIcon512URL:         orDefault(b.PWAIcon512URL, "/pwa/icon-512.png"),
		PWAMaskableIcon512URL: orDefault(b.PWAMaskableIcon512URL, "/pwa/icon-maskable-512.png"),
		AppleTouchIcon180URL:  orDefault(b.AppleTouchIcon180URL, "/pwa/apple-touch-icon.png"),
	}
}

func (h *handlers) branding(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	httpx.OK(c, branding(h.cfg.Load().Branding))
}

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

type webManifest struct {
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	ID              string         `json:"id"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Categories      []string       `json:"categories"`
	Lang            string         `json:"lang"`
	Icons           []manifestIcon `json:"icons"`
}

// resolveAgainst resolves a relative reference against the public web base.
// Absolute references, unparseable values and a base without a host leave the
// value unchanged; an empty value stays empty.
func resolveAgainst(base, v string) string {
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return v
	}
	b, err := url.Parse(base)
	if base == "" || err != nil || b.Hostname() == "" {
		return v
	}
	if strings.HasPrefix(v, "/") {
		return base + v
	}
	return base + "/" + v
}

func buildManifest(cfg *config.Config) webManifest {
	b := branding(cfg.Branding)
	base := strings.TrimRight(strings.TrimSpace(cfg.Server.PublicWebBaseURL), "/")
	return webManifest{
		Name: b.Title, ShortName: b.ShortName, Description: b.Description,
		ID: base + "/", StartURL: base + "/chat", Scope: base + "/",
		Display: "standalone", BackgroundColor: "#ffffff", ThemeColor: "#0f172a",
		Categories: []string{"productivity", "business", "utilities"}, Lang: "en",
		Icons: []manifestIcon{
			{Src: resolveAgainst(base, b.PWAIcon192URL), Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: resolveAgainst(base, b.PWAIcon512URL), Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: resolveAgainst(base, b.PWAMaskableIcon512URL), Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
		},
	}
}

func (h *handlers) manifest(c *gin.Context) {
	raw, err := json.Marshal(buildManifest(h.cfg.Load()))
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "application/manifest+json; charset=utf-8", raw)
}

// ---- public and user policy views ------------------------------------------------------

func (h *handlers) loginPage(c *gin.Context) {
	path, err := h.svc.LoginDefaultNextPath(c.Request.Context())
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"defaultNextPath": path})
}

type modelOptionPolicy struct {
	Mode             string       `json:"mode"`
	AllowedPathsJSON string       `json:"allowedPathsJSON"`
	DeniedPathsJSON  string       `json:"deniedPathsJSON"`
	NativeTools      []NativeTool `json:"nativeTools"`
}

func (h *handlers) modelOptionPolicy(c *gin.Context) {
	ctx := c.Request.Context()
	vals, err := h.svc.NamespaceValues(ctx, "chat")
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	mode := vals["model_option_policy_mode"]
	switch mode {
	case "allowlist", "denylist", "disabled":
	default:
		mode = "allowlist"
	}
	allowed, denied := vals["model_option_allowed_paths"], vals["model_option_denied_paths"]
	if allowed == "" {
		allowed = defaultAllowedPathsJSON
	}
	if denied == "" {
		denied = defaultDeniedPathsJSON
	}
	tools, err := h.svc.nativeTools(ctx)
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, modelOptionPolicy{Mode: mode, AllowedPathsJSON: allowed, DeniedPathsJSON: denied, NativeTools: tools})
}

// nativeTools returns the catalog from the wired source or the built-in one.
func (s *Service) nativeTools(ctx context.Context) ([]NativeTool, error) {
	var tools []NativeTool
	if src := s.port().NativeTools; src != nil {
		var err error
		if tools, err = src.NativeTools(ctx); err != nil {
			return nil, err
		}
	} else {
		tools = builtinNativeTools()
	}
	out := make([]NativeTool, len(tools))
	for i, t := range tools {
		if t.Payload == nil {
			t.Payload = map[string]any{}
		}
		if t.UsageAliases == nil {
			t.UsageAliases = []string{}
		}
		out[i] = t
	}
	return out, nil
}

func (h *handlers) mcpPolicy(c *gin.Context) {
	n := h.svc.Runtime().MCP.MaxSelectedToolsPerMessage
	switch {
	case n <= 0:
		n = 32
	case n > 128:
		n = 128
	}
	httpx.OK(c, gin.H{"maxSelectedToolsPerMessage": n})
}

func (h *handlers) chatContextPolicy(c *gin.Context) {
	httpx.OK(c, gin.H{"contextCompactEnabled": h.svc.Runtime().Chat.ContextCompactEnabled})
}

func (h *handlers) featurePolicy(c *gin.Context) {
	rt := h.svc.Runtime()
	enabled, target := false, ""
	if h.svc.caps != nil && h.svc.caps.Enabled(capability.DesktopDownload) {
		enabled, target = rt.Desktop.DesktopDownloadTarget()
	}
	httpx.OK(c, gin.H{
		"knowledgeBaseEnabled":   rt.KnowledgeBase.Enabled,
		"processTraceEnabled":    rt.Chat.ProcessTraceEnabled,
		"desktopDownloadEnabled": enabled,
		"desktopDownloadURL":     target,
	})
}

// ---- user settings ------------------------------------------------------------------------

type userSettingsRequest struct {
	Settings map[string]string `json:"settings" binding:"required"`
}

func (h *handlers) getUserSettings(c *gin.Context) {
	m, err := h.svc.UserSettings(c.Request.Context(), middleware.MustPrincipal(c).UserID)
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"settings": m})
}

func (h *handlers) patchUserSettings(c *gin.Context) {
	var req userSettingsRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	m, err := h.svc.PatchUserSettings(c.Request.Context(), middleware.MustPrincipal(c).UserID, req.Settings)
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"settings": m})
}
