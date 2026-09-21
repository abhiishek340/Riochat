package contentmoderation

import (
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type configData struct {
	Config struct {
		Enabled        bool   `json:"enabled"`
		BaseURL        string `json:"baseUrl"`
		APIKeyMasked   string `json:"apiKeyMasked"`
		HasAPIKey      bool   `json:"hasAPIKey"`
		Model          string `json:"model"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
		MaxConcurrency int    `json:"maxConcurrency"`
		QueueCapacity  int    `json:"queueCapacity"`
		Policy         struct {
			InputTextCategories   []string `json:"inputTextCategories"`
			OutputTextCategories  []string `json:"outputTextCategories"`
			InputImageCategories  []string `json:"inputImageCategories"`
			OutputImageCategories []string `json:"outputImageCategories"`
			Version               int64    `json:"version"`
		} `json:"policy"`
	} `json:"config"`
	Categories struct {
		Text  []string `json:"text"`
		Image []string `json:"image"`
	} `json:"categories"`
}

func (h *harness) getConfig() configData {
	h.t.Helper()
	w := h.do("GET", "/config", nil)
	testkit.Expect(h.t, w, 200, "")
	var d configData
	testkit.Decode(h.t, w, &d)
	return d
}

func TestConfigDefaultsOnFreshInstall(t *testing.T) {
	h := newHarness(t)
	d := h.getConfig()
	c := d.Config
	if c.Enabled || c.BaseURL != "https://api.openai.com/v1" || c.Model != "omni-moderation-latest" ||
		c.TimeoutSeconds != 10 || c.MaxConcurrency != 4 || c.QueueCapacity != 256 || c.HasAPIKey || c.APIKeyMasked != "" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Policy.Version != 0 {
		t.Fatalf("version %d", c.Policy.Version)
	}
	if len(d.Categories.Text) != 13 || len(d.Categories.Image) != 6 {
		t.Fatalf("catalog: %+v", d.Categories)
	}
	// Category lists must be arrays, never null.
	w := h.do("GET", "/config", nil)
	if strings.Contains(w.Body.String(), "null") {
		t.Fatalf("null in response: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), "apiKeyMasked") {
		t.Fatalf("empty masked key must be omitted: %s", w.Body)
	}
}

func TestConfigRequiresSuperAdmin(t *testing.T) {
	h := newHarness(t)
	admin := h.env.Bearer(testkit.Admin(2))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/config"}, {"PUT", "/config"}, {"POST", "/probe"}, {"GET", "/events"},
		{"GET", "/events/cme_x"}, {"GET", "/events/cme_x/images/0"},
	} {
		w := h.env.Do(tc.method, "/api/v1/admin/content-moderation"+tc.path, admin, map[string]any{})
		testkit.Expect(t, w, 403, "auth.superadmin_required")
	}
	user := h.env.Bearer(testkit.User(3))
	testkit.Expect(t, h.env.Do("GET", "/api/v1/admin/content-moderation/config", user, nil), 403, "auth.admin_required")
	testkit.Expect(t, h.env.Do("GET", "/api/v1/admin/content-moderation/config", nil, nil), 401, "auth.invalid_token")
}

func TestRoleMatchIsCaseInsensitive(t *testing.T) {
	h := newHarness(t)
	claims := superClaims
	claims.Role = "superadmin"
	if !(Actor{Role: "  SuperAdmin "}).isSuperAdmin() {
		t.Fatal("role must be trimmed and case-insensitive")
	}
	if (Actor{Role: "admin"}).isSuperAdmin() || !(Actor{Role: "admin"}).isAdmin() {
		t.Fatal("admin is not super")
	}
	_ = h
}

func TestEnableOnFreshInstallNeedsConfiguration(t *testing.T) {
	h := newHarness(t)
	w := h.do("PUT", "/config", map[string]any{"enabled": true})
	testkit.Expect(t, w, 400, "content_moderation.config_required")
	if d := h.getConfig(); d.Config.Enabled {
		t.Fatal("nothing may be saved when validation fails")
	}
	var n int64
	h.env.DB.Model(&settingRow{}).Where("namespace = ?", "content_moderation").Count(&n)
	if n != 0 {
		t.Fatalf("rows written despite failure: %d", n)
	}
}

func TestSaveNormalizesPolicyAndMasksKey(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"baseUrl": "https://api.example.com", "apiKey": "sk-abcdefgh12345", "model": "m", "enabled": true,
		"policy": map[string]any{"inputTextCategories": []string{"violence", "hate", "violence"}},
	}
	w := h.do("PUT", "/config", body)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Config configData `json:"config"`
	}
	_ = out
	var resp struct {
		Config struct {
			APIKeyMasked string `json:"apiKeyMasked"`
			HasAPIKey    bool   `json:"hasAPIKey"`
			Policy       struct {
				InputTextCategories  []string `json:"inputTextCategories"`
				OutputTextCategories []string `json:"outputTextCategories"`
				Version              int64    `json:"version"`
			} `json:"policy"`
		} `json:"config"`
	}
	testkit.Decode(t, w, &resp)
	if got := resp.Config.Policy.InputTextCategories; len(got) != 2 || got[0] != "hate" || got[1] != "violence" {
		t.Fatalf("normalized: %v", got)
	}
	if resp.Config.Policy.Version != 1 {
		t.Fatalf("version %d", resp.Config.Policy.Version)
	}
	if resp.Config.Policy.OutputTextCategories == nil || !resp.Config.HasAPIKey || resp.Config.APIKeyMasked != "sk-a...2345" {
		t.Fatalf("response: %+v", resp.Config)
	}
	if strings.Contains(w.Body.String(), "sk-abcdefgh12345") {
		t.Fatal("api key leaked")
	}

	// Same categories, different order and padding: version must not move.
	body["policy"] = map[string]any{"inputTextCategories": []string{" violence", "hate "}}
	testkit.Expect(t, h.do("PUT", "/config", body), 200, "")
	if v := h.getConfig().Config.Policy.Version; v != 1 {
		t.Fatalf("version after reorder: %d", v)
	}
	// A real change bumps by exactly one.
	body["policy"] = map[string]any{"inputTextCategories": []string{"hate"}, "outputImageCategories": []string{"sexual"}}
	testkit.Expect(t, h.do("PUT", "/config", body), 200, "")
	if v := h.getConfig().Config.Policy.Version; v != 2 {
		t.Fatalf("version after change: %d", v)
	}
	// Other changes never touch the version.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"model": "other", "timeoutSeconds": 5}), 200, "")
	if v := h.getConfig().Config.Policy.Version; v != 2 {
		t.Fatalf("version after non-policy change: %d", v)
	}
}

func TestStoredKeyIsEncrypted(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	var row settingRow
	if err := h.env.DB.Where(&settingRow{Namespace: "content_moderation", Key: "api_key"}).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Value == "" || strings.Contains(row.Value, "sk-test-key") {
		t.Fatalf("api key stored in clear: %q", row.Value)
	}
	if row.ValueType != "string" {
		t.Fatalf("value type %q", row.ValueType)
	}
	var n int64
	h.env.DB.Model(&settingRow{}).Where("namespace = ?", "content_moderation").Count(&n)
	if n != 9 {
		t.Fatalf("expected the nine setting keys, got %d", n)
	}
}

func TestPolicyValidation(t *testing.T) {
	h := newHarness(t)
	for name, pol := range map[string]map[string]any{
		"text only in image input":  {"inputImageCategories": []string{"hate"}},
		"text only in image output": {"outputImageCategories": []string{"illicit"}},
		"unknown":                   {"inputTextCategories": []string{"bogus"}},
		"unknown output":            {"outputTextCategories": []string{"violence", "nope"}},
	} {
		w := h.do("PUT", "/config", map[string]any{"policy": pol})
		if w.Code != 400 {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		testkit.Expect(t, w, 400, "content_moderation.invalid_config")
	}
	// Blank entries are dropped, not rejected.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"policy": map[string]any{"inputTextCategories": []string{"", "  ", "hate"}}}), 200, "")
	d := h.getConfig()
	if got := d.Config.Policy.InputTextCategories; len(got) != 1 || got[0] != "hate" {
		t.Fatalf("blank handling: %v", got)
	}
	// Providing a policy replaces all four lists at once.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"policy": map[string]any{"outputTextCategories": []string{"violence"}}}), 200, "")
	d = h.getConfig()
	if len(d.Config.Policy.InputTextCategories) != 0 || len(d.Config.Policy.OutputTextCategories) != 1 {
		t.Fatalf("replace semantics: %+v", d.Config.Policy)
	}
	// A client-supplied version is ignored.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"policy": map[string]any{"outputTextCategories": []string{"violence"}, "version": 99}}), 200, "")
	if v := h.getConfig().Config.Policy.Version; v == 99 {
		t.Fatal("client version accepted")
	}
}

func TestNumericLimitsBoundaries(t *testing.T) {
	h := newHarness(t)
	bad := []map[string]any{
		{"timeoutSeconds": 0}, {"timeoutSeconds": 61}, {"timeoutSeconds": -1},
		{"maxConcurrency": 0}, {"maxConcurrency": 65},
		{"queueCapacity": 0}, {"queueCapacity": 4097},
		{"model": "   "},
	}
	for _, b := range bad {
		testkit.Expect(t, h.do("PUT", "/config", b), 400, "content_moderation.invalid_config")
	}
	good := []map[string]any{
		{"timeoutSeconds": 1}, {"timeoutSeconds": 60}, {"maxConcurrency": 1}, {"maxConcurrency": 64},
		{"queueCapacity": 1}, {"queueCapacity": 4096},
	}
	for _, b := range good {
		testkit.Expect(t, h.do("PUT", "/config", b), 200, "")
	}
	c := h.getConfig().Config
	if c.TimeoutSeconds != 60 || c.MaxConcurrency != 64 || c.QueueCapacity != 4096 {
		t.Fatalf("stored: %+v", c)
	}
	// Wrong JSON types are body errors, not config errors.
	testkit.Expect(t, h.do("PUT", "/config", `{"timeoutSeconds":"ten"}`), 400, "request.invalid_body")
	// A number that is not a whole number is an invalid limit.
	testkit.Expect(t, h.do("PUT", "/config", `{"timeoutSeconds":1.5}`), 400, "content_moderation.invalid_config")
	testkit.Expect(t, h.do("PUT", "/config", `{"queueCapacity":1e30}`), 400, "content_moderation.invalid_config")
	testkit.Expect(t, h.do("PUT", "/config", `{"maxConcurrency":2.0}`), 200, "")
}

func TestClearAPIKeyWinsAndBlocksEnabling(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	if !h.getConfig().Config.HasAPIKey {
		t.Fatal("key expected")
	}
	w := h.do("PUT", "/config", map[string]any{"clearAPIKey": true, "apiKey": "x"})
	testkit.Expect(t, w, 400, "content_moderation.config_required") // still enabled, now without a key
	if !h.getConfig().Config.HasAPIKey {
		t.Fatal("failed update must not change the key")
	}
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"enabled": false, "clearAPIKey": true, "apiKey": "x"}), 200, "")
	c := h.getConfig().Config
	if c.HasAPIKey || c.APIKeyMasked != "" || c.Enabled {
		t.Fatalf("after clear: %+v", c)
	}
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"enabled": true}), 400, "content_moderation.config_required")
	// Omitted apiKey keeps the existing key.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"apiKey": "sk-12345678-abcd"}), 200, "")
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"model": "x"}), 200, "")
	if !h.getConfig().Config.HasAPIKey {
		t.Fatal("omitted apiKey must keep the key")
	}
}

func TestShortKeysAreFullyMasked(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "****", "12345678": "****", "123456789": "1234...6789", "  sk-abcdefgh12345 ": "sk-a...2345"} {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisablingKeepsPolicyAndKey(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"enabled": false}), 200, "")
	c := h.getConfig().Config
	if c.Enabled || !c.HasAPIKey || len(c.Policy.InputTextCategories) != 2 || c.Policy.Version != 1 {
		t.Fatalf("after disabling: %+v", c)
	}
}

func TestBaseURLValidationOnSave(t *testing.T) {
	h := newHarness(t)
	for _, u := range []string{"ftp://x", "https://user:pw@host/", "http://", "://x", "https://"} {
		testkit.Expect(t, h.do("PUT", "/config", map[string]any{"baseUrl": u}), 400, "content_moderation.invalid_config")
	}
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"baseUrl": " api.example.com "}), 200, "")
	if got := h.getConfig().Config.BaseURL; got != "api.example.com" {
		t.Fatalf("stored as given (trimmed): %q", got)
	}
	// Empty resets to the default.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"baseUrl": ""}), 200, "")
	if got := h.getConfig().Config.BaseURL; got != DefaultBaseURL {
		t.Fatalf("reset: %q", got)
	}
}

func TestModerationEndpointNormalization(t *testing.T) {
	cases := map[string]string{
		"https://h":                     "https://h/v1/moderations",
		"https://h/":                    "https://h/v1/moderations",
		"https://h/v1":                  "https://h/v1/moderations",
		"https://h/v1/":                 "https://h/v1/moderations",
		"https://h/openai":              "https://h/openai/v1/moderations",
		"https://h/x/moderations?a=1#f": "https://h/x/moderations",
		"https://h/X/Moderations":       "https://h/X/Moderations",
		"api.example.com":               "https://api.example.com/v1/moderations",
		"http://localhost:8080/V1":      "http://localhost:8080/V1/moderations",
		"https://api.openai.com/v1":     "https://api.openai.com/v1/moderations",
		"  https://h/v1//  ":            "https://h/v1/moderations",
		"HTTPS://H.example/path/":       "https://H.example/path/v1/moderations",
	}
	for in, want := range cases {
		got, err := ModerationEndpoint(in)
		if err != nil || got != want {
			t.Errorf("ModerationEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://x", "https://u:p@h/", "https://", "http://:80", "file:///etc/passwd"} {
		if _, err := ModerationEndpoint(bad); err == nil {
			t.Errorf("ModerationEndpoint(%q) accepted", bad)
		}
	}
}

func TestExecutorLimitsFollowSavedConfig(t *testing.T) {
	h := newHarness(t)
	h.enable(map[string]any{"maxConcurrency": 7, "queueCapacity": 33})
	mc, qc := h.svc.exec.limits()
	if mc != 7 || qc != 33 {
		t.Fatalf("limits %d/%d", mc, qc)
	}
}

func TestConfigReadFailsOnCorruptStoredValues(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).Update("value", "{not json")
	testkit.Expect(t, h.do("GET", "/config", nil), 500, "internal.error")
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).
		Update("value", `{"inputImageCategories":["hate"]}`)
	testkit.Expect(t, h.do("GET", "/config", nil), 500, "internal.error")

	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).Update("value", "{}")
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "api_key"}).Update("value", "enc:garbage")
	testkit.Expect(t, h.do("GET", "/config", nil), 500, "internal.error")
}

func TestStoredValuesFallBackToDefaults(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	set := func(k, v string) {
		h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: k}).Update("value", v)
	}
	set("timeout_seconds", "0")
	set("max_concurrency", "abc")
	set("queue_capacity", "-4")
	set("enabled", "maybe")
	set("model", "  ")
	c := h.getConfig().Config
	if c.TimeoutSeconds != 10 || c.MaxConcurrency != 4 || c.QueueCapacity != 256 || c.Enabled || c.Model != DefaultModel {
		t.Fatalf("fallbacks: %+v", c)
	}
}

func TestPolicyVersionFallsBackToSeparateRow(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).
		Update("value", `{"inputTextCategories":["hate"]}`)
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_version"}).Update("value", "7")
	if v := h.getConfig().Config.Policy.Version; v != 7 {
		t.Fatalf("version %d", v)
	}
}

func TestFeatureGateOffInLocalMode(t *testing.T) {
	h := newHarness(t, testkit.WithConfig(func(c *config.Config) {
		c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"}
	}))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/config"}, {"PUT", "/config"}, {"POST", "/probe"}, {"GET", "/stats"}, {"GET", "/events"},
		{"GET", "/events/x"}, {"GET", "/events/x/images/0"},
	} {
		w := h.do(tc.method, tc.path, map[string]any{})
		testkit.Expect(t, w, 404, "feature.disabled")
		env := testkit.Envelope(t, w)
		d, _ := env.Details.(map[string]any)
		if d["feature"] != "contentModeration" {
			t.Fatalf("details: %+v", env.Details)
		}
	}
	// With the capability off nothing is moderated either.
	run := h.svc.BeginRun(t.Context(), RunInfo{RunID: "r"}, RunOptions{})
	if run.Active() {
		t.Fatal("run must be inert while the capability is off")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 2; i++ {
		for _, s := range Migrations() {
			if err := s.Up(t.Context(), h.env.DB); err != nil {
				t.Fatalf("step %s: %v", s.Name, err)
			}
		}
	}
}

func TestTableAndColumnNames(t *testing.T) {
	h := newHarness(t)
	m := h.env.DB.Migrator()
	for _, tc := range []struct {
		model any
		cols  []string
	}{
		{&Event{}, []string{"id", "created_at", "updated_at", "deleted_at", "public_id", "user_id", "conversation_id", "run_id",
			"message_id", "message_public_id", "direction", "modality", "model", "policy_version", "result", "categories_json",
			"category_scores_json", "latency_ms", "error_code", "error_message", "content_location_json", "content_summary",
			"encrypted_text", "image_count", "image_meta_json", "content_expires_at", "metadata_expires_at"}},
		{&DailyStat{}, []string{"stat_date", "direction", "modality", "result", "category", "check_count", "content_items",
			"hit_count", "failure_count", "latency_sum_ms", "latency_count"}},
	} {
		for _, c := range tc.cols {
			if !m.HasColumn(tc.model, c) {
				t.Errorf("missing column %s", c)
			}
		}
	}
	for _, idx := range []string{"idx_content_moderation_events_public_id", "idx_content_moderation_events_run_id",
		"idx_content_moderation_events_result", "idx_content_moderation_events_content_expires_at",
		"idx_content_moderation_events_conversation_id", "idx_content_moderation_events_error_code"} {
		if !m.HasIndex(&Event{}, idx) {
			t.Errorf("missing index %s", idx)
		}
	}
	if (Event{}).TableName() != "content_moderation_events" || (DailyStat{}).TableName() != "content_moderation_daily_stats" {
		t.Fatal("table names")
	}
}
