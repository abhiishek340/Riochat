package personalprovider

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestAccessWhenFeatureOff(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", mp+"/access", h.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var info AccessInfo
	testkit.Decode(t, w, &info)
	if info.Enabled {
		t.Fatal("enabled should be false")
	}
	if info.MaxPerUser != 10 {
		t.Fatalf("maxPerUser = %d", info.MaxPerUser)
	}
	if len(info.Protocols) != 8 || len(info.ModelProtocols) != 16 {
		t.Fatalf("lists: %v %v", info.Protocols, info.ModelProtocols)
	}
	if info.Protocols[0] != "openai_chat_completions" || info.ModelProtocols[15] != "openrouter_images" {
		t.Fatalf("order: %v %v", info.Protocols, info.ModelProtocols)
	}
}

func TestAccessRequiresAuth(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", mp+"/access", nil, nil)
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestAccessMaxPerUserFallback(t *testing.T) {
	h := newHarness(t).enable()
	h.set(SettingMaxPerUser, "0")
	w := h.do("GET", mp+"/access", h.user(1), nil)
	var info AccessInfo
	testkit.Decode(t, w, &info)
	if !info.Enabled || info.MaxPerUser != 10 {
		t.Fatalf("%+v", info)
	}
	h.set(SettingMaxPerUser, "3")
	w = h.do("GET", mp+"/access", h.user(1), nil)
	testkit.Decode(t, w, &info)
	if info.MaxPerUser != 3 {
		t.Fatalf("%+v", info)
	}
}

func TestFeatureOffBlocksEverythingButDelete(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	h.set(SettingEnabled, "false")

	testkit.Expect(t, h.do("GET", mp, h.user(1), nil), 403, "personal_provider.disabled")
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(nil)), 403, "personal_provider.disabled")
	testkit.Expect(t, h.do("POST", mp+"/probe", h.user(1), map[string]any{
		"protocol": "openai_chat_completions", "baseURL": "https://api.example.com/v1", "apiKey": "sk-abcdefghijWXYZ"}), 403, "personal_provider.disabled")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": "x"}), 403, "personal_provider.disabled")
	testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 403, "personal_provider.disabled")

	w := h.do("DELETE", mp+"/"+p.ID, h.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Deleted bool }
	testkit.Decode(t, w, &out)
	if !out.Deleted {
		t.Fatal("deleted should be true")
	}
	if len(h.rows()) != 0 {
		t.Fatal("row should be hard deleted")
	}
}

func TestListEmptyAndOrder(t *testing.T) {
	h := newHarness(t).enable()
	w := h.do("GET", mp, h.user(1), nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"providers":[]`) {
		t.Fatalf("body %s", w.Body.String())
	}
	a := h.create(1, map[string]any{"apiKey": "sk-aaaaaaaaaaaaaaaa"})
	b := h.create(1, map[string]any{"apiKey": "sk-bbbbbbbbbbbbbbbb"})
	h.create(2, nil)
	w = h.do("GET", mp, h.user(1), nil)
	var out struct{ Providers []providerJSON }
	testkit.Decode(t, w, &out)
	if len(out.Providers) != 2 || out.Providers[0].ID != a.ID || out.Providers[1].ID != b.ID {
		t.Fatalf("order: %+v", out.Providers)
	}
}

func TestCreateValid(t *testing.T) {
	h := newHarness(t).enable()
	w := h.do("POST", mp, h.user(7), h.createBody(map[string]any{
		"name": "  My Provider ", "icon": "OpenAI ", "models": []string{"a"},
	}))
	testkit.Expect(t, w, 200, "")
	body := w.Body.String()
	if strings.Contains(body, "sk-abcdefghijWXYZ") || strings.Contains(strings.ToLower(body), "apikey") {
		t.Fatalf("key leaked: %s", body)
	}
	var out struct{ Provider providerJSON }
	testkit.Decode(t, w, &out)
	p := out.Provider
	if len(p.ID) < 8 || len(p.ID) > 32 || !validPublicID(p.ID) {
		t.Fatalf("id %q", p.ID)
	}
	if p.Name != "My Provider" || p.Icon != "openai" || p.Protocol != "openai_chat_completions" {
		t.Fatalf("%+v", p)
	}
	if p.BaseURL != "https://api.example.com/v1" || p.Host != "api.example.com" {
		t.Fatalf("%+v", p)
	}
	if p.KeyHint != "sk-••••WXYZ" || p.Status != "active" || p.Source != "manual" || p.LastError != "" {
		t.Fatalf("%+v", p)
	}
	if len(p.Models) != 1 || p.Models[0].Name != "a" || len(p.Models[0].Protocols) != 1 || p.Models[0].Protocols[0] != "openai_chat_completions" {
		t.Fatalf("models %+v", p.Models)
	}
	if p.LastCheckedAt == nil || p.CreatedAt == "" || p.UpdatedAt == "" {
		t.Fatalf("%+v", p)
	}
	if got := h.lister.last(); got.APIKey != "sk-abcdefghijWXYZ" || got.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("lister saw %+v", got)
	}

	rows := h.rows()
	if len(rows) != 1 {
		t.Fatalf("rows %d", len(rows))
	}
	r := rows[0]
	if strings.Contains(r.APIKeyEncrypted, "sk-abcdefghijWXYZ") || r.APIKeyEncrypted == "" {
		t.Fatalf("ciphertext %q", r.APIKeyEncrypted)
	}
	key, err := h.svc.decryptKey(&r)
	if err != nil || key != "sk-abcdefghijWXYZ" {
		t.Fatalf("decrypt %q %v", key, err)
	}
	if r.UserID != 7 {
		t.Fatalf("owner %d", r.UserID)
	}
}

func TestCreateAuditHasNoKey(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(3, map[string]any{"models": []string{"a", "b"}, "source": "link"})
	if p.Source != "link" {
		t.Fatalf("source %q", p.Source)
	}
	entries := h.audit.all()
	if len(entries) != 1 {
		t.Fatalf("entries %+v", entries)
	}
	e := entries[0]
	if e.Action != "personal_provider.create" || e.Resource != "personal_providers" || e.ResourceID != p.ID {
		t.Fatalf("%+v", e)
	}
	if e.Detail["host"] != "api.example.com" || e.Detail["protocol"] != "openai_chat_completions" ||
		e.Detail["source"] != "link" || e.Detail["model_count"] != float64(2) {
		t.Fatalf("detail %+v", e.Detail)
	}
	raw := fmt.Sprint(e.Detail)
	for _, frag := range []string{"abcdefghij", "WXYZ", "sk-"} {
		if strings.Contains(raw, frag) {
			t.Fatalf("audit leaks %q: %s", frag, raw)
		}
	}
}

func TestCreateWritesRealAuditRow(t *testing.T) {
	h := newHarness(t, harnessOpts{realAudit: true}).enable()
	h.create(3, nil)
	var n int64
	h.env.DB.Table("audit_logs").Where("action = ? AND actor_user_id = 3", "personal_provider.create").Count(&n)
	if n != 1 {
		t.Fatalf("audit rows %d", n)
	}
}

func TestCreateDefaultsNameToHost(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"baseURL": "HTTPS://API.Example.COM/v1///"})
	if p.Name != "api.example.com" || p.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("%+v", p)
	}
	if p.Icon != "" {
		t.Fatalf("icon %q", p.Icon)
	}
}

func TestCreateValidationErrors(t *testing.T) {
	h := newHarness(t).enable()
	cases := []struct {
		name  string
		extra map[string]any
		code  string
	}{
		{"unknown model", map[string]any{"models": []string{"zzz"}}, "personal_provider.invalid_models"},
		{"bad model name", map[string]any{"models": []string{"has space"}}, "personal_provider.invalid_models"},
		{"model leading space", map[string]any{"models": []string{" a"}}, "personal_provider.invalid_models"},
		{"model trailing newline", map[string]any{"models": []string{"a\n"}}, "personal_provider.invalid_models"},
		{"ipv6 loopback", map[string]any{"baseURL": "https://[::1]"}, "personal_provider.blocked_host"},
		{"ipv4 compatible v6", map[string]any{"baseURL": "https://[::7f00:1]"}, "personal_provider.blocked_host"},
		{"key newline", map[string]any{"apiKey": "sk-abc\ndef"}, "personal_provider.invalid_api_key"},
		{"key space", map[string]any{"apiKey": "sk abc"}, "personal_provider.invalid_api_key"},
		{"key blank", map[string]any{"apiKey": "   "}, "personal_provider.invalid_api_key"},
		{"key non-ascii", map[string]any{"apiKey": "sk-ключ"}, "personal_provider.invalid_api_key"},
		{"protocol", map[string]any{"protocol": "openai_image_generations"}, "personal_provider.invalid_protocol"},
		{"protocol junk", map[string]any{"protocol": "nope"}, "personal_provider.invalid_protocol"},
		{"http", map[string]any{"baseURL": "http://api.example.com"}, "personal_provider.invalid_base_url"},
		{"ftp", map[string]any{"baseURL": "ftp://api.example.com"}, "personal_provider.invalid_base_url"},
		{"query", map[string]any{"baseURL": "https://api.example.com?x=1"}, "personal_provider.invalid_base_url"},
		{"empty query", map[string]any{"baseURL": "https://api.example.com?"}, "personal_provider.invalid_base_url"},
		{"userinfo", map[string]any{"baseURL": "https://u:p@api.example.com"}, "personal_provider.invalid_base_url"},
		{"fragment", map[string]any{"baseURL": "https://api.example.com#f"}, "personal_provider.invalid_base_url"},
		{"port 0", map[string]any{"baseURL": "https://api.example.com:0"}, "personal_provider.invalid_base_url"},
		{"port big", map[string]any{"baseURL": "https://api.example.com:70000"}, "personal_provider.invalid_base_url"},
		{"localhost", map[string]any{"baseURL": "https://localhost"}, "personal_provider.blocked_host"},
		{"loopback", map[string]any{"baseURL": "https://127.0.0.1"}, "personal_provider.blocked_host"},
		{"metadata", map[string]any{"baseURL": "https://169.254.169.254"}, "personal_provider.blocked_host"},
		{"private", map[string]any{"baseURL": "https://10.0.0.5"}, "personal_provider.blocked_host"},
		{"icon url", map[string]any{"icon": "http://x/y.png"}, "personal_provider.invalid_icon"},
		{"icon dash", map[string]any{"icon": "-bad"}, "personal_provider.invalid_icon"},
		{"icon long", map[string]any{"icon": strings.Repeat("a", 65)}, "400"},
		{"name control", map[string]any{"name": "a\x01b"}, "personal_provider.invalid_name"},
		{"model protocol unknown", map[string]any{"models": []any{map[string]any{"name": "a", "protocols": []string{"bogus"}}}}, "personal_provider.invalid_model_protocol"},
		{"model protocol non-companion", map[string]any{"models": []any{map[string]any{"name": "a", "protocols": []string{"openai_chat_completions", "openai_responses"}}}}, "personal_provider.invalid_model_protocol"},
		{"model protocols 3", map[string]any{"models": []any{map[string]any{"name": "a", "protocols": []string{"openai_chat_completions", "openai_responses", "anthropic_messages"}}}}, "400"},
		{"model protocol long", map[string]any{"models": []any{map[string]any{"name": "a", "protocols": []string{strings.Repeat("x", 65)}}}}, "400"},
		{"bad source", map[string]any{"source": "import"}, "400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := h.do("POST", mp, h.user(1), h.createBody(tc.extra))
			if tc.code == "400" {
				testkit.Expect(t, w, 400, "request.invalid_body")
				return
			}
			testkit.Expect(t, w, 400, tc.code)
		})
	}
	if len(h.rows()) != 0 {
		t.Fatalf("nothing should have been saved, got %d", len(h.rows()))
	}
}
