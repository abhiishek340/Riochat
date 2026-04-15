package settings_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestBrandingDefaults(t *testing.T) {
	h := newHarness(t, settings.Ports{}, testkit.WithConfig(func(c *config.Config) { c.Branding = config.BrandingConfig{} }))
	w := h.do("GET", "/api/v1/branding", nil, nil)
	testkit.Expect(t, w, 200, "")
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("cache control: %q", w.Header().Get("Cache-Control"))
	}
	m := decodeMap(t, w)
	want := map[string]string{
		"title": "Riochat", "shortName": "Riochat", "description": "Riochat is a multi-model AI conversation system.",
		"logoURL": "", "faviconURL": "/favicon.ico", "pwaIcon192URL": "/pwa/icon-192.png", "pwaIcon512URL": "/pwa/icon-512.png",
		"pwaMaskableIcon512URL": "/pwa/icon-maskable-512.png", "appleTouchIcon180URL": "/pwa/apple-touch-icon.png",
	}
	if len(m) != len(want) {
		t.Fatalf("fields: %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v want %q", k, m[k], v)
		}
	}
}

func TestBrandingFromConfig(t *testing.T) {
	h := newHarness(t, settings.Ports{}, testkit.WithConfig(func(c *config.Config) {
		c.Branding = config.BrandingConfig{Title: " Acme Chat ", ShortName: "Acme", Description: "Our assistant", LogoURL: "  /logo.svg ",
			FaviconURL: "/f.ico", PWAIcon192URL: "/i192.png", PWAIcon512URL: "/i512.png", PWAMaskableIcon512URL: "/m.png", AppleTouchIcon180URL: "/a.png"}
	}))
	m := decodeMap(t, h.do("GET", "/api/v1/branding", nil, nil))
	if m["title"] != "Acme Chat" || m["logoURL"] != "/logo.svg" || m["pwaMaskableIcon512URL"] != "/m.png" || m["shortName"] != "Acme" {
		t.Fatalf("branding: %v", m)
	}
}

func TestManifest(t *testing.T) {
	h := newHarness(t, settings.Ports{}, testkit.WithConfig(func(c *config.Config) {
		c.Server.PublicWebBaseURL = "https://app.example.com/"
		c.Branding = config.BrandingConfig{Title: "Acme", PWAMaskableIcon512URL: "https://cdn.example.com/m.png"}
	}))
	for _, path := range []string{"/api/v1/branding/manifest.webmanifest", "/manifest.webmanifest"} {
		w := h.do("GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/manifest+json; charset=utf-8" {
			t.Errorf("%s content type %q", path, ct)
		}
		if w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s cache control %q", path, w.Header().Get("Cache-Control"))
		}
		var m struct {
			Name            string   `json:"name"`
			ShortName       string   `json:"short_name"`
			Description     string   `json:"description"`
			ID              string   `json:"id"`
			StartURL        string   `json:"start_url"`
			Scope           string   `json:"scope"`
			Display         string   `json:"display"`
			BackgroundColor string   `json:"background_color"`
			ThemeColor      string   `json:"theme_color"`
			Lang            string   `json:"lang"`
			Categories      []string `json:"categories"`
			Icons           []struct{ Src, Sizes, Type, Purpose string }
		}
		raw := w.Body.Bytes()
		if _, wrapped := mustMap(t, raw)["data"]; wrapped {
			t.Fatalf("%s: manifest must not be wrapped in the envelope", path)
		}
		// field names of a web manifest
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(raw, &keys)
		for _, k := range []string{"name", "short_name", "description", "id", "start_url", "scope", "display", "background_color", "theme_color", "categories", "lang", "icons"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("%s: missing %s", path, k)
			}
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m.Name != "Acme" || m.ShortName != "Riochat" || m.ID != "https://app.example.com/" || m.StartURL != "https://app.example.com/chat" ||
			m.Scope != "https://app.example.com/" || m.Display != "standalone" || m.BackgroundColor != "#ffffff" || m.ThemeColor != "#0f172a" || m.Lang != "en" {
			t.Errorf("%s manifest: %+v", path, m)
		}
		if strings.Join(m.Categories, ",") != "productivity,business,utilities" {
			t.Errorf("categories: %v", m.Categories)
		}
		if len(m.Icons) != 3 {
			t.Fatalf("icons: %+v", m.Icons)
		}
		if m.Icons[0].Src != "https://app.example.com/pwa/icon-192.png" || m.Icons[0].Sizes != "192x192" || m.Icons[0].Purpose != "any" ||
			m.Icons[1].Src != "https://app.example.com/pwa/icon-512.png" || m.Icons[1].Sizes != "512x512" ||
			m.Icons[2].Src != "https://cdn.example.com/m.png" || m.Icons[2].Purpose != "maskable" || m.Icons[2].Type != "image/png" {
			t.Errorf("icons: %+v", m.Icons)
		}
	}
}

func mustMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestWithoutPublicBase(t *testing.T) {
	h := newHarness(t, settings.Ports{}, testkit.WithConfig(func(c *config.Config) { c.Server.PublicWebBaseURL = "" }))
	m := mustMap(t, h.do("GET", "/manifest.webmanifest", nil, nil).Body.Bytes())
	if m["id"] != "/" || m["start_url"] != "/chat" || m["scope"] != "/" {
		t.Fatalf("relative manifest: %v", m)
	}
	icons := m["icons"].([]any)
	if icons[0].(map[string]any)["src"] != "/pwa/icon-192.png" {
		t.Fatalf("icons stay as configured: %v", icons)
	}
}

func TestLoginPage(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	get := func() string {
		w := h.do("GET", "/api/v1/settings/login-page", nil, nil)
		testkit.Expect(t, w, 200, "")
		return decodeMap(t, w)["defaultNextPath"].(string)
	}
	if get() != "/chat" {
		t.Fatal("default")
	}
	h.mustPatch(set("auth", "login_default_next_path", "/library"))
	if get() != "/library" {
		t.Fatal("configured path")
	}
	for _, bad := range []string{"//evil.com", "evil.com", "", "   "} {
		h.setRaw("auth", "login_default_next_path", bad)
		if got := get(); got != "/chat" {
			t.Errorf("stored %q should fall back, got %q", bad, got)
		}
	}
	h.setRaw("auth", "login_default_next_path", " /padded ")
	if get() != "/padded" {
		t.Error("trimmed path")
	}
}

func TestMCPAndChatContextPolicies(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	check := func(want float64) {
		t.Helper()
		w := h.do("GET", "/api/v1/settings/mcp-policy", h.user, nil)
		testkit.Expect(t, w, 200, "")
		if got := decodeMap(t, w)["maxSelectedToolsPerMessage"]; got != want {
			t.Fatalf("maxSelectedToolsPerMessage = %v want %v", got, want)
		}
	}
	check(32)
	for raw, want := range map[string]float64{"500": 128, "0": 32, "-4": 32, "128": 128, "7": 7} {
		h.setRaw("mcp", "mcp_max_selected_tools_per_message", raw)
		if err := h.svc.Reload(ctxBG()); err != nil {
			t.Fatal(err)
		}
		check(want)
	}

	w := h.do("GET", "/api/v1/settings/chat-context-policy", h.user, nil)
	testkit.Expect(t, w, 200, "")
	if decodeMap(t, w)["contextCompactEnabled"] != false {
		t.Fatal("default off")
	}
	h.mustPatch(set("chat", "context_compact_enabled", "true"))
	if decodeMap(t, h.do("GET", "/api/v1/settings/chat-context-policy", h.user, nil))["contextCompactEnabled"] != true {
		t.Fatal("switched on")
	}
}

func TestFeaturePolicyServerMode(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	get := func() map[string]any {
		w := h.do("GET", "/api/v1/settings/feature-policy", h.user, nil)
		testkit.Expect(t, w, 200, "")
		return decodeMap(t, w)
	}
	m := get()
	if m["knowledgeBaseEnabled"] != true || m["processTraceEnabled"] != true || m["desktopDownloadEnabled"] != true ||
		m["desktopDownloadURL"] != settings.RegistryDefault("desktop", "download_url") {
		t.Fatalf("defaults: %v", m)
	}
	h.mustPatch(set("knowledgebase", "enabled", "false"), set("chat", "process_trace_enabled", "false"),
		set("desktop", "download_url", "  https://example.com/get  "))
	m = get()
	if m["knowledgeBaseEnabled"] != false || m["processTraceEnabled"] != false || m["desktopDownloadURL"] != "https://example.com/get" {
		t.Fatalf("changed: %v", m)
	}
	h.mustPatch(set("desktop", "download_enabled", "false"))
	m = get()
	if m["desktopDownloadEnabled"] != false || m["desktopDownloadURL"] != "" {
		t.Fatalf("disabled entry must hide the url: %v", m)
	}
}

func TestFeaturePolicyHidesInvalidURL(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.setRaw("desktop", "download_url", "https://u:p@x.com/")
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
	// the runtime falls back to the default for an invalid stored url, which is valid
	m := decodeMap(t, h.do("GET", "/api/v1/settings/feature-policy", h.user, nil))
	if m["desktopDownloadURL"] != settings.RegistryDefault("desktop", "download_url") {
		t.Fatalf("fallback url expected: %v", m)
	}
}

func TestFeaturePolicyLocalMode(t *testing.T) {
	h := newHarness(t, settings.Ports{}, localMode())
	m := decodeMap(t, h.do("GET", "/api/v1/settings/feature-policy", h.user, nil))
	if m["desktopDownloadEnabled"] != false || m["desktopDownloadURL"] != "" {
		t.Fatalf("local mode hides the desktop entry: %v", m)
	}
	if m["knowledgeBaseEnabled"] != true {
		t.Fatalf("other fields unaffected: %v", m)
	}
}

type fakeCatalog struct {
	tools []settings.NativeTool
	err   error
}

func (f fakeCatalog) NativeTools(context.Context) ([]settings.NativeTool, error) {
	return f.tools, f.err
}

func TestModelOptionPolicy(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil)
	testkit.Expect(t, w, 200, "")
	m := decodeMap(t, w)
	if m["mode"] != "allowlist" {
		t.Fatalf("mode: %v", m["mode"])
	}
	allowed, _ := m["allowedPathsJSON"].(string)
	denied, _ := m["deniedPathsJSON"].(string)
	if allowed != settings.MarshalDoc(settings.DefaultAllowedDoc()) {
		t.Fatalf("allowed: %s", allowed)
	}
	if !strings.Contains(denied, `"previous_response_id"`) || !strings.Contains(denied, `"systemInstruction"`) {
		t.Fatalf("denied: %s", denied)
	}
	tools, _ := m["nativeTools"].([]any)
	if len(tools) < 20 {
		t.Fatalf("native tools: %d", len(tools))
	}
	first := tools[0].(map[string]any)
	for _, k := range []string{"protocol", "provider", "type", "toolKey", "label", "description", "payload", "defaultEnabled", "billable",
		"billingUnit", "priceNanousd", "priceLabel", "riskLevel", "usageAliases"} {
		if _, ok := first[k]; !ok {
			t.Errorf("native tool lacks %s", k)
		}
	}

	h.mustPatch(set("chat", "model_option_policy_mode", "denylist"), set("chat", "model_option_denied_paths", `{"default":["model","x"]}`))
	m = decodeMap(t, h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil))
	if m["mode"] != "denylist" || m["deniedPathsJSON"] != `{"default":["model","x"]}` {
		t.Fatalf("custom policy: %v", m)
	}
	// blank stored values fall back to the built-in documents
	h.setRaw("chat", "model_option_policy_mode", "")
	h.setRaw("chat", "model_option_allowed_paths", "")
	m = decodeMap(t, h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil))
	if m["mode"] != "allowlist" || m["allowedPathsJSON"] != allowed {
		t.Fatalf("blank fallback: %v", m)
	}
}

func TestModelOptionPolicyUsesWiredCatalog(t *testing.T) {
	custom := settings.NativeTool{Protocol: "openai_responses", Provider: "Custom", Type: "thing", ToolKey: "custom.thing", Label: "Thing"}
	h := newHarness(t, settings.Ports{NativeTools: fakeCatalog{tools: []settings.NativeTool{custom}}})
	m := decodeMap(t, h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil))
	tools := m["nativeTools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["toolKey"] != "custom.thing" || tool["payload"] == nil || tool["usageAliases"] == nil {
		t.Fatalf("tool: %v", tool)
	}

	h = newHarness(t, settings.Ports{NativeTools: fakeCatalog{err: errBoom}})
	testkit.Expect(t, h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil), 500, "internal.error")
}

func TestLateWiredPorts(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	testkit.Expect(t, h.do("GET", "/api/v1/admin/settings/embedding/status", h.admin, nil), 503, "embedding.service_unavailable")
	h.svc.WirePorts(settings.Ports{Embedding: &fakeEmbedding{status: settings.EmbeddingStatus{ReadyCount: 3}}})
	w := h.do("GET", "/api/v1/admin/settings/embedding/status", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	if decodeMap(t, w)["readyCount"] != float64(3) {
		t.Fatalf("status: %s", w.Body)
	}
	// wiring other members keeps what is wired
	h.svc.WirePorts(settings.Ports{SuperAdmins: fakeAdmins{}})
	if h.svc.Ports().Embedding == nil {
		t.Fatal("earlier port lost")
	}
}
