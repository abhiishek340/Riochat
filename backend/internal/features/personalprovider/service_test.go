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

func TestCreateMissingRequiredFields(t *testing.T) {
	h := newHarness(t).enable()
	for _, field := range []string{"protocol", "baseURL", "apiKey"} {
		body := h.createBody(nil)
		delete(body, field)
		testkit.Expect(t, h.do("POST", mp, h.user(1), body), 400, "request.invalid_body")
	}
	testkit.Expect(t, h.do("POST", mp, h.user(1), "{not json"), 400, "request.invalid_body")
}

func TestCreateLegacyStringModelsAndDedupe(t *testing.T) {
	h := newHarness(t).enable()
	h.lister.set(func(ListRequest) ([]string, error) { return []string{"m1", "m2", "m1", " m3 ", "bad name", "z\n"}, nil })
	p := h.create(1, map[string]any{"models": []any{"m1", map[string]any{"name": "m2"}, "m1"}})
	if len(p.Models) != 2 || p.Models[0].Name != "m1" || p.Models[1].Name != "m2" {
		t.Fatalf("%+v", p.Models)
	}
	for _, m := range p.Models {
		if len(m.Protocols) != 1 || m.Protocols[0] != "openai_chat_completions" {
			t.Fatalf("%+v", m)
		}
	}
}

func TestCreateMediaCompanionProtocols(t *testing.T) {
	h := newHarness(t).enable()
	h.lister.set(func(ListRequest) ([]string, error) { return []string{"gpt-image-1", "grok-imagine-video"}, nil })
	p := h.create(1, map[string]any{"models": []any{
		"gpt-image-1",
		map[string]any{"name": "grok-imagine-video", "protocols": []string{"XAI_Video ", "xai_video_extensions"}},
	}})
	if got := strings.Join(p.Models[0].Protocols, ","); got != "openai_image_generations,openai_image_edits" {
		t.Fatalf("suggested %s", got)
	}
	if got := strings.Join(p.Models[1].Protocols, ","); got != "xai_video,xai_video_extensions" {
		t.Fatalf("explicit %s", got)
	}
}

func TestCreateNoModelsAllowed(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	if p.Models == nil || len(p.Models) != 0 {
		t.Fatalf("models %+v", p.Models)
	}
	w := h.do("GET", mp, h.user(1), nil)
	if !strings.Contains(w.Body.String(), `"models":[]`) {
		t.Fatalf("models must serialize as []: %s", w.Body.String())
	}
}

func TestCreateTooManyModels(t *testing.T) {
	h := newHarness(t).enable()
	models := make([]string, 201)
	for i := range models {
		models[i] = fmt.Sprintf("m%d", i)
	}
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"models": models})), 400, "request.invalid_body")
}

func TestDuplicateDetection(t *testing.T) {
	h := newHarness(t).enable()
	h.create(1, nil)
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(nil)), 409, "personal_provider.duplicate")
	// Trailing slash normalizes to the same address.
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"baseURL": "https://API.example.com/v1/"})), 409, "personal_provider.duplicate")
	// Same URL, different key.
	h.create(1, map[string]any{"apiKey": "sk-another-key-value"})
	// Same key, different URL.
	h.create(1, map[string]any{"baseURL": "https://other.example.com/v1"})
	// Another user may add the same pair.
	h.create(2, nil)
}

func TestLimitReachedCountsAllStatuses(t *testing.T) {
	h := newHarness(t).enable()
	h.set(SettingMaxPerUser, "2")
	a := h.create(1, map[string]any{"apiKey": "sk-key-number-one-xx"})
	h.create(1, map[string]any{"apiKey": "sk-key-number-two-xx"})
	testkit.Expect(t, h.do("PATCH", mp+"/"+a.ID, h.user(1), map[string]any{"enabled": false}), 200, "")
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"apiKey": "sk-key-number-three"})), 409, "personal_provider.limit_reached")
	// Other users have their own quota.
	h.create(2, nil)
}

func TestLimitCheckedBeforeDuplicateAndUpstream(t *testing.T) {
	h := newHarness(t).enable()
	h.set(SettingMaxPerUser, "1")
	h.create(1, nil)
	before := h.lister.count()
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(nil)), 409, "personal_provider.limit_reached")
	if h.lister.count() != before {
		t.Fatal("upstream must not be called when limit is reached")
	}
}

func TestUpstreamErrors(t *testing.T) {
	h := newHarness(t).enable()
	probeBody := map[string]any{"protocol": "openai_chat_completions", "baseURL": "https://api.example.com/v1", "apiKey": "sk-abcdefghijWXYZ"}
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"401", &UpstreamStatusError{Status: 401}, "personal_provider.upstream_rejected"},
		{"403", &UpstreamStatusError{Status: 403}, "personal_provider.upstream_rejected"},
		{"500", &UpstreamStatusError{Status: 500}, "personal_provider.upstream_unavailable"},
		{"404", &UpstreamStatusError{Status: 404}, "personal_provider.upstream_unavailable"},
		{"network", errors.New("dial tcp: secret-upstream-text sk-abcdefghijWXYZ"), "personal_provider.upstream_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.lister.set(func(ListRequest) ([]string, error) { return nil, tc.err })
			for _, call := range []func() (int, string){
				func() (int, string) {
					w := h.do("POST", mp+"/probe", h.user(1), probeBody)
					testkit.Expect(t, w, 502, tc.code)
					return w.Code, w.Body.String()
				},
				func() (int, string) {
					w := h.do("POST", mp, h.user(1), h.createBody(nil))
					testkit.Expect(t, w, 502, tc.code)
					return w.Code, w.Body.String()
				},
			} {
				_, body := call()
				if strings.Contains(body, "secret-upstream-text") || strings.Contains(body, "abcdefghij") {
					t.Fatalf("leak: %s", body)
				}
			}
		})
	}
	if len(h.rows()) != 0 {
		t.Fatal("failed creates must not save")
	}
}

func TestProbeReturnsSortedCatalogWithSuggestions(t *testing.T) {
	h := newHarness(t).enable()
	h.lister.set(func(ListRequest) ([]string, error) {
		return []string{"zeta", "alpha", "alpha", " beta ", "bad name", "", "gpt-image-1", strings.Repeat("x", 101)}, nil
	})
	w := h.do("POST", mp+"/probe", h.user(1), map[string]any{
		"protocol": " OpenAI_Chat_Completions ", "baseURL": "https://api.example.com/v1/", "apiKey": " sk-abcdefghijWXYZ "})
	testkit.Expect(t, w, 200, "")
	var out struct{ Models []CatalogModel }
	testkit.Decode(t, w, &out)
	var names []string
	for _, m := range out.Models {
		names = append(names, m.Name)
		if len(m.SuggestedProtocols) == 0 {
			t.Fatalf("no suggestions for %s", m.Name)
		}
	}
	if got := strings.Join(names, ","); got != "alpha,beta,gpt-image-1,zeta" {
		t.Fatalf("names %s", got)
	}
	if got := h.lister.last(); got.APIKey != "sk-abcdefghijWXYZ" || got.Protocol != "openai_chat_completions" {
		t.Fatalf("%+v", got)
	}
	if len(h.rows()) != 0 {
		t.Fatal("probe must not save")
	}
	if got := strings.Join(out.Models[2].SuggestedProtocols, ","); got != "openai_image_generations,openai_image_edits" {
		t.Fatalf("image suggestion %s", got)
	}
}

func TestProbeValidation(t *testing.T) {
	h := newHarness(t).enable()
	w := h.do("POST", mp+"/probe", h.user(1), map[string]any{"protocol": "openai_chat_completions", "baseURL": "https://localhost", "apiKey": "k"})
	testkit.Expect(t, w, 400, "personal_provider.blocked_host")
	w = h.do("POST", mp+"/probe", h.user(1), map[string]any{"protocol": "bad", "baseURL": "https://api.example.com", "apiKey": "k"})
	testkit.Expect(t, w, 400, "personal_provider.invalid_protocol")
	w = h.do("POST", mp+"/probe", h.user(1), map[string]any{"protocol": "openai_chat_completions", "baseURL": "https://api.example.com"})
	testkit.Expect(t, w, 400, "request.invalid_body")
}

func TestDenyListBlocksCreate(t *testing.T) {
	h := newHarness(t).enable()
	h.set(SettingBlockedHosts, "evil.com,*.bad.org")
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"baseURL": "https://evil.com/v1"})), 400, "personal_provider.blocked_host")
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"baseURL": "https://a.b.bad.org"})), 400, "personal_provider.blocked_host")
	h.create(1, map[string]any{"baseURL": "https://api.evil.com/v1"})
	h.create(1, map[string]any{"baseURL": "https://bad.org/v1"})
	h.create(1, map[string]any{"baseURL": "https://notbad.org/v1"})
}

func TestModelsFetchRecordsOutcome(t *testing.T) {
	h := newHarness(t).enable()
	h.strict = true
	p := h.create(1, nil)
	h.env.Clock.Advance(3600e9)

	h.lister.set(func(ListRequest) ([]string, error) { return nil, &UpstreamStatusError{Status: 401} })
	testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 502, "personal_provider.upstream_rejected")
	row := h.rows()[0]
	if row.LastError != "personal_provider.upstream_rejected" || row.LastCheckedAt == nil || !row.LastCheckedAt.Equal(h.env.Clock.Now()) {
		t.Fatalf("after failure: %q %v", row.LastError, row.LastCheckedAt)
	}

	h.lister.set(func(ListRequest) ([]string, error) { return []string{"b", "a"}, nil })
	w := h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Models []CatalogModel }
	testkit.Decode(t, w, &out)
	if len(out.Models) != 2 || out.Models[0].Name != "a" {
		t.Fatalf("%+v", out.Models)
	}
	if got := h.lister.last(); got.APIKey != "sk-abcdefghijWXYZ" {
		t.Fatalf("stored key not used: %+v", got)
	}
	if row := h.rows()[0]; row.LastError != "" {
		t.Fatalf("last error should be cleared, got %q", row.LastError)
	}

	h.lister.set(func(ListRequest) ([]string, error) { return nil, errors.New("boom") })
	testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 502, "personal_provider.upstream_unavailable")
	w = h.do("GET", mp, h.user(1), nil)
	if !strings.Contains(w.Body.String(), `"lastError":"personal_provider.upstream_unavailable"`) {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestModelsFetchRechecksDenyList(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	h.set(SettingBlockedHosts, "api.example.com")
	before := h.lister.count()
	testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 400, "personal_provider.blocked_host")
	if h.lister.count() != before {
		t.Fatal("blocked host must not be contacted")
	}
}

func TestUpdateEnabledToggle(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	w := h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": false})
	testkit.Expect(t, w, 200, "")
	var out struct{ Provider providerJSON }
	testkit.Decode(t, w, &out)
	if out.Provider.Status != "disabled" || len(out.Provider.Models) != 1 {
		t.Fatalf("%+v", out.Provider)
	}
	w = h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": true})
	testkit.Decode(t, w, &out)
	if out.Provider.Status != "active" {
		t.Fatalf("%+v", out.Provider)
	}
	e := h.audit.all()
	last := e[len(e)-1]
	if last.Action != "personal_provider.update" || last.Detail["enabled_changed"] != true ||
		last.Detail["key_rotated"] != false || last.Detail["models_changed"] != false || last.Detail["host"] != "api.example.com" {
		t.Fatalf("%+v", last)
	}
}

func TestUpdateNameAndIcon(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"icon": "openai"})
	before := h.lister.count()
	w := h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": " Renamed ", "icon": ""})
	testkit.Expect(t, w, 200, "")
	var out struct{ Provider providerJSON }
	testkit.Decode(t, w, &out)
	if out.Provider.Name != "Renamed" || out.Provider.Icon != "" {
		t.Fatalf("%+v", out.Provider)
	}
	if h.lister.count() != before {
		t.Fatal("name/icon update must not probe")
	}
	// Empty name on update is invalid, as is a bad icon; nothing changes.
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": "   "}), 400, "personal_provider.invalid_name")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"icon": "-x"}), 400, "personal_provider.invalid_icon")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": strings.Repeat("n", 65)}), 400, "request.invalid_body")
	if h.rows()[0].Name != "Renamed" {
		t.Fatal("name changed by failed update")
	}
}

func TestUpdateEmptyBodyIsNoop(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	w := h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{})
	testkit.Expect(t, w, 200, "")
}

func TestUpdateModelsReplacesAndClears(t *testing.T) {
	h := newHarness(t).enable()
	h.strict = true
	h.lister.set(func(ListRequest) ([]string, error) { return []string{"a", "b", "c"}, nil })
	p := h.create(1, map[string]any{"models": []string{"a"}})
	h.env.Clock.Advance(60e9)
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"models": []string{"b", "c"}}), 200, "")
	row := h.rows()[0]
	if names := modelNames(row.Models()); names != "b,c" {
		t.Fatalf("models %s", names)
	}
	if !row.LastCheckedAt.Equal(h.env.Clock.Now()) {
		t.Fatalf("lastChecked %v", row.LastCheckedAt)
	}
	if h.lister.last().APIKey != "sk-abcdefghijWXYZ" {
		t.Fatal("stored key should probe when only models change")
	}
	e := h.audit.all()
	if e[len(e)-1].Detail["models_changed"] != true {
		t.Fatalf("%+v", e[len(e)-1])
	}

	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"models": []string{}}), 200, "")
	if n := len(h.rows()[0].Models()); n != 0 {
		t.Fatalf("models should be cleared, got %d", n)
	}
}

func TestUpdateModelsFailureSavesNothing(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"models": []string{"nope"}, "name": "X", "enabled": false}), 400, "personal_provider.invalid_models")
	h.lister.set(func(ListRequest) ([]string, error) { return nil, errors.New("down") })
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"models": []string{"a"}}), 502, "personal_provider.upstream_unavailable")
	row := h.rows()[0]
	if row.Name == "X" || row.Status != "active" || modelNames(row.Models()) != "a" {
		t.Fatalf("%+v", row)
	}
}

func TestUpdateKeyRotation(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []any{map[string]any{"name": "a", "protocols": []string{"openai_responses"}}}})
	oldCipher := h.rows()[0].APIKeyEncrypted

	w := h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"apiKey": "sk-brand-new-key-ABCD"})
	testkit.Expect(t, w, 200, "")
	if strings.Contains(w.Body.String(), "brand-new") {
		t.Fatalf("leak %s", w.Body.String())
	}
	var out struct{ Provider providerJSON }
	testkit.Decode(t, w, &out)
	if out.Provider.KeyHint != "sk-••••ABCD" {
		t.Fatalf("hint %q", out.Provider.KeyHint)
	}
	if h.lister.last().APIKey != "sk-brand-new-key-ABCD" {
		t.Fatalf("probe used %q", h.lister.last().APIKey)
	}
	row := h.rows()[0]
	if row.APIKeyEncrypted == oldCipher {
		t.Fatal("ciphertext unchanged")
	}
	if key, err := h.svc.decryptKey(&row); err != nil || key != "sk-brand-new-key-ABCD" {
		t.Fatalf("%q %v", key, err)
	}
	// Saved models are re-validated with their saved protocols.
	if m := row.Models(); len(m) != 1 || m[0].Protocols[0] != "openai_responses" {
		t.Fatalf("%+v", m)
	}
	e := h.audit.all()
	if last := e[len(e)-1]; last.Detail["key_rotated"] != true {
		t.Fatalf("%+v", last)
	}
	route, err := h.svc.Resolve(t.Context(), ResolveRequest{UserID: 1, Reference: FormatReference(p.ID, "a")})
	if err != nil || route.APIKey != "sk-brand-new-key-ABCD" {
		t.Fatalf("%v", err)
	}
}

func TestUpdateKeyValidation(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	for _, k := range []string{"", "   ", "a b", "bad\nkey"} {
		testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"apiKey": k}), 400, "personal_provider.invalid_api_key")
	}
}

func TestUpdateKeyRejectedUpstreamSavesNothing(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	h.lister.set(func(r ListRequest) ([]string, error) {
		if r.APIKey == "sk-wrong-key-value-1" {
			return nil, &UpstreamStatusError{Status: 401}
		}
		return []string{"a"}, nil
	})
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"apiKey": "sk-wrong-key-value-1"}), 502, "personal_provider.upstream_rejected")
	if h.rows()[0].KeyHint != "sk-••••WXYZ" {
		t.Fatal("key hint changed")
	}
}

func TestUpdateSuspendedIsSticky(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	testkit.Expect(t, h.do("POST", "/admin/model-providers/suspend", h.admin(99), map[string]any{"ids": []string{p.ID}, "suspended": true}), 200, "")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": true}), 409, "personal_provider.suspended")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": false}), 409, "personal_provider.suspended")
	// Renaming a suspended provider is still allowed.
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": "ok"}), 200, "")
	testkit.Expect(t, h.do("POST", "/admin/model-providers/suspend", h.admin(99), map[string]any{"ids": []string{p.ID}, "suspended": false}), 200, "")
	w := h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": true})
	testkit.Expect(t, w, 200, "")
	if h.rows()[0].Status != "active" {
		t.Fatal("status")
	}
}

func TestOwnershipIsolation(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	for _, tc := range []struct{ method, path string }{
		{"PATCH", mp + "/" + p.ID},
		{"DELETE", mp + "/" + p.ID},
		{"GET", mp + "/" + p.ID + "/models"},
	} {
		testkit.Expect(t, h.do(tc.method, tc.path, h.user(2), map[string]any{"name": "x"}), 404, "personal_provider.not_found")
	}
	if len(h.rows()) != 1 {
		t.Fatal("foreign delete removed the row")
	}
	// Malformed ids behave like missing ones.
	for _, id := range []string{"short", "UPPERCASE123", "has-hyphen-12345", strings.Repeat("a", 33), "nonexistent1"} {
		testkit.Expect(t, h.do("DELETE", mp+"/"+id, h.user(1), nil), 404, "personal_provider.not_found")
		testkit.Expect(t, h.do("PATCH", mp+"/"+id, h.user(1), map[string]any{"name": "x"}), 404, "personal_provider.not_found")
		testkit.Expect(t, h.do("GET", mp+"/"+id+"/models", h.user(1), nil), 404, "personal_provider.not_found")
	}
	// The other user sees an empty list.
	w := h.do("GET", mp, h.user(2), nil)
	if !strings.Contains(w.Body.String(), `"providers":[]`) {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestDeleteAudit(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	testkit.Expect(t, h.do("DELETE", mp+"/"+p.ID, h.user(1), nil), 200, "")
	e := h.audit.all()
	last := e[len(e)-1]
	if last.Action != "personal_provider.delete" || last.ResourceID != p.ID || last.Detail["host"] != "api.example.com" {
		t.Fatalf("%+v", last)
	}
	testkit.Expect(t, h.do("DELETE", mp+"/"+p.ID, h.user(1), nil), 404, "personal_provider.not_found")
}

func TestProbeRateLimit(t *testing.T) {
	h := newHarness(t).enable()
	h.strict = true
	p := h.create(1, nil) // 1 of 10
	for i := 0; i < 9; i++ {
		testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 200, "")
	}
	testkit.Expect(t, h.do("GET", mp+"/"+p.ID+"/models", h.user(1), nil), 429, "")
	testkit.Expect(t, h.do("POST", mp+"/probe", h.user(1), map[string]any{"protocol": "openai_chat_completions", "baseURL": "https://api.example.com", "apiKey": "k"}), 429, "")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"name": "n"}), 429, "")
	testkit.Expect(t, h.do("POST", mp, h.user(1), h.createBody(map[string]any{"apiKey": "sk-another-key-value"})), 429, "")
	// Unlimited endpoints.
	testkit.Expect(t, h.do("GET", mp, h.user(1), nil), 200, "")
	testkit.Expect(t, h.do("GET", mp+"/access", h.user(1), nil), 200, "")
	// Per user, and admins are limited too.
	testkit.Expect(t, h.do("GET", mp+"/"+h.create(2, nil).ID+"/models", h.user(2), nil), 200, "")
	testkit.Expect(t, h.do("DELETE", mp+"/"+p.ID, h.user(1), nil), 200, "")
	// The window slides.
	h.env.Clock.Advance(61e9)
	testkit.Expect(t, h.do("POST", mp+"/probe", h.user(1), map[string]any{"protocol": "openai_chat_completions", "baseURL": "https://api.example.com", "apiKey": "k"}), 200, "")
}

func modelNames(ms []EnabledModel) string {
	var n []string
	for _, m := range ms {
		n = append(n, m.Name)
	}
	return strings.Join(n, ",")
}
