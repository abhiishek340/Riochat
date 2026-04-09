package settings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestAccessControl(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/settings"}, {"GET", "/api/v1/admin/settings/auth"}, {"PATCH", "/api/v1/admin/settings"},
		{"GET", "/api/v1/admin/settings/embedding/status"}, {"GET", "/api/v1/admin/settings/tika/runtime"},
	} {
		testkit.Expect(t, h.do(p.method, p.path, nil, nil), 401, "auth.invalid_token")
		testkit.Expect(t, h.do(p.method, p.path, h.user, nil), 403, "auth.admin_required")
	}
	for _, path := range []string{"/api/v1/settings/model-option-policy", "/api/v1/settings/mcp-policy",
		"/api/v1/settings/chat-context-policy", "/api/v1/settings/feature-policy", "/api/v1/user/settings"} {
		testkit.Expect(t, h.do("GET", path, nil, nil), 401, "auth.invalid_token")
		testkit.Expect(t, h.do("GET", path, h.user, nil), 200, "")
	}
	testkit.Expect(t, h.do("GET", "/api/v1/settings/login-page", nil, nil), 200, "")
	testkit.Expect(t, h.do("GET", "/api/v1/branding", nil, nil), 200, "")
}

func TestPatchBodyValidation(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	for name, body := range map[string]any{
		"empty body":      "",
		"not json":        "{",
		"no items":        map[string]any{},
		"empty items":     map[string]any{"items": []any{}},
		"null items":      `{"items":null}`,
		"missing key":     map[string]any{"items": []any{map[string]any{"namespace": "auth"}}},
		"value not text":  map[string]any{"items": []any{map[string]any{"namespace": "auth", "key": "token_ttl_hours", "value": 5}}},
		"clear not bool":  map[string]any{"items": []any{map[string]any{"namespace": "auth", "key": "smtp_password", "clear": "yes"}}},
		"items not array": map[string]any{"items": "x"},
	} {
		w := h.do("PATCH", "/api/v1/admin/settings", h.admin, body)
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

// An item without a namespace is an unknown namespace, not a malformed body.
func TestPatchMissingNamespaceIsInvalidNamespace(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.do("PATCH", "/api/v1/admin/settings", h.admin, map[string]any{"items": []any{map[string]any{"key": "token_ttl_hours"}}})
	if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "settings.invalid_namespace" {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestPatchResponseIsFullListing(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("auth", "token_ttl_hours", "48"))
	testkit.Expect(t, w, 200, "")
	var out map[string][]item
	testkit.Decode(t, w, &out)
	if len(out) != len(settings.Namespaces()) {
		t.Fatalf("namespaces in response: %d", len(out))
	}
	for _, it := range out["auth"] {
		if it.Key == "token_ttl_hours" && it.Value != "48" {
			t.Fatalf("fresh value missing: %+v", it)
		}
	}
}

func TestPatchValueValidationTable(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	cases := []struct {
		name              string
		item              map[string]any
		code, field, rule string
		param             string
	}{
		{"ttl zero", set("auth", "token_ttl_hours", "0"), "settings.invalid_value", "auth:token_ttl_hours", "integer_range", "1,168"},
		{"ttl text", set("auth", "token_ttl_hours", "abc"), "settings.invalid_value", "auth:token_ttl_hours", "integer", "-"},
		{"ttl over", set("auth", "token_ttl_hours", "169"), "settings.invalid_value", "auth:token_ttl_hours", "integer_range", "1,168"},
		{"refresh over", set("auth", "refresh_token_ttl_hours", "8761"), "settings.invalid_value", "auth:refresh_token_ttl_hours", "integer_range", "1,8760"},
		{"rpm zero", set("auth", "rate_limit_rpm", "0"), "settings.invalid_value", "auth:rate_limit_rpm", "integer_range", "1,100000"},
		{"bool bad", set("auth", "rate_limit_enabled", "maybe"), "settings.invalid_value", "auth:rate_limit_enabled", "bool", "-"},
		{"next path", set("auth", "login_default_next_path", "//evil.com"), "settings.invalid_value", "auth:login_default_next_path", "local_path", "-"},
		{"next path empty", set("auth", "login_default_next_path", "  "), "settings.invalid_value", "auth:login_default_next_path", "required", "-"},
		{"next path long", set("auth", "login_default_next_path", "/"+strings.Repeat("a", 120)), "settings.invalid_value", "auth:login_default_next_path", "max_length", "120"},
		{"smtp host long", set("auth", "smtp_host", strings.Repeat("h", 256)), "settings.smtp_invalid", "auth:smtp_host", "max_length", "255"},
		{"smtp port", set("auth", "smtp_port", "70000"), "settings.smtp_invalid", "auth:smtp_port", "integer_range", "1,65535"},
		{"smtp password long", set("auth", "smtp_password", strings.Repeat("p", 256)), "settings.smtp_invalid", "auth:smtp_password", "max_length", "255"},
		{"domains", set("auth", "email_registration_allowed_domains", "localhost"), "settings.invalid_value", "auth:email_registration_allowed_domains", "domain", "-"},
		{"turnstile key long", set("auth", "turnstile_site_key", strings.Repeat("k", 513)), "settings.invalid_value", "auth:turnstile_site_key", "max_length", "512"},
		{"billing mode", set("billing", "mode", "free"), "settings.invalid_value", "billing:mode", "enum", "self,period,usage"},
		{"prepaid", set("billing", "prepaid_amount_usd", "-1"), "settings.invalid_value", "billing:prepaid_amount_usd", "float_range", "0,1000000"},
		{"rate", set("billing", "usd_to_cny_rate", "0"), "settings.invalid_value", "billing:usd_to_cny_rate", "float_range", "0.000001,1000"},
		{"currency", set("billing", "display_currency", "EUR"), "settings.invalid_value", "billing:display_currency", "enum", "USD,CNY"},
		{"providers", set("billing", "payment_providers", "stripe,paypal"), "settings.billing_payment_invalid", "billing:payment_providers", "payment_provider", "stripe,epay"},
		{"stripe key long", set("billing", "stripe_publishable_key", strings.Repeat("k", 513)), "settings.billing_payment_invalid", "billing:stripe_publishable_key", "max_length", "512"},
		{"epay url", set("billing", "epay_gateway_url", "pay.example.com"), "settings.billing_payment_invalid", "billing:epay_gateway_url", "epay_url", "-"},
		{"epay not array", set("billing", "epay_types", `{"a":1}`), "settings.billing_payment_invalid", "billing:epay_types", "json_array", "-"},
		{"pricing unknown", set("billing", "native_tool_pricing_json", `{"nope.tool":{"priceNanousd":1}}`), "settings.invalid_value", "billing:native_tool_pricing_json", "native_tool_pricing", "-"},
		{"pricing blank", set("billing", "native_tool_pricing_json", " "), "settings.invalid_value", "billing:native_tool_pricing_json", "required", "-"},
		{"context fallback", set("chat", "context_window_fallback_tokens", "100"), "settings.invalid_value", "chat:context_window_fallback_tokens", "integer_range", "16384,16000000"},
		{"trigger", set("chat", "context_compact_trigger_percent", "5"), "settings.invalid_value", "chat:context_compact_trigger_percent", "integer_range", "10,95"},
		{"similarity", set("chat", "rag_min_similarity", "1.5"), "settings.invalid_value", "chat:rag_min_similarity", "float_range", "0.000001,1"},
		{"policy mode", set("chat", "model_option_policy_mode", "open"), "settings.model_option_policy_invalid", "chat:model_option_policy_mode", "enum", "allowlist,denylist,disabled"},
		{"default prompt long", set("chat", "default_system_prompt", strings.Repeat("x", 20001)), "settings.invalid_value", "chat:default_system_prompt", "max_length", "20000"},
		{"allowed bogus", set("chat", "model_option_allowed_paths", `{"bogus":["a"]}`), "settings.model_option_policy_invalid", "chat:model_option_allowed_paths", "model_option_protocol", "-"},
		{"allowed segments", set("chat", "model_option_allowed_paths", `{"default":["a..b"]}`), "settings.model_option_policy_invalid", "chat:model_option_allowed_paths", "model_option_path", "segments"},
		{"allowed whitespace", set("chat", "model_option_allowed_paths", `{"default":["a b"]}`), "settings.model_option_policy_invalid", "chat:model_option_allowed_paths", "model_option_path", "whitespace"},
		{"allowed characters", set("chat", "model_option_allowed_paths", `{"default":["a$"]}`), "settings.model_option_policy_invalid", "chat:model_option_allowed_paths", "model_option_path", "characters"},
		{"denied not object", set("chat", "model_option_denied_paths", `[]`), "settings.model_option_policy_invalid", "chat:model_option_denied_paths", "json_object", "-"},
		{"kb flag", set("knowledgebase", "enabled", "2"), "settings.invalid_value", "knowledgebase:enabled", "bool", "-"},
		{"per user", set("personal_provider", "max_per_user", "51"), "settings.invalid_value", "personal_provider:max_per_user", "integer_range", "1,50"},
		{"blocked hosts", set("personal_provider", "blocked_hosts", "http://x.com"), "settings.invalid_value", "personal_provider:blocked_hosts", "domain", "-"},
		{"download js", set("desktop", "download_url", "javascript:alert(1)"), "settings.invalid_value", "desktop:download_url", "download_url", "-"},
		{"download creds", set("desktop", "download_url", "https://u:p@x.com/"), "settings.invalid_value", "desktop:download_url", "download_url", "-"},
		{"download empty", set("desktop", "download_url", ""), "settings.invalid_value", "desktop:download_url", "required", "-"},
		{"download long", set("desktop", "download_url", "https://example.com/"+strings.Repeat("a", 190)), "settings.invalid_value", "desktop:download_url", "max_length", "200"},
		{"quota", set("storage", "user_storage_quota_bytes", "-1"), "settings.invalid_value", "storage:user_storage_quota_bytes", "integer_min", "0"},
		{"max upload", set("storage", "max_upload_file_bytes", "0"), "settings.invalid_value", "storage:max_upload_file_bytes", "integer_min", "1"},
		{"message files", set("storage", "max_message_files", "51"), "settings.invalid_value", "storage:max_message_files", "integer_range", "1,50"},
		{"image dim", set("file", "image_max_dimension", "9000"), "settings.invalid_value", "file:image_max_dimension", "integer_range", "0,8192"},
		{"full tokens", set("file", "full_context_max_tokens", "64"), "settings.invalid_value", "file:full_context_max_tokens", "optional_integer_range", "128,1000000"},
		{"full bytes", set("file", "file_full_context_max_bytes", "-5"), "settings.invalid_value", "file:file_full_context_max_bytes", "optional_integer_min", "0"},
		{"image bytes", set("file", "image_max_bytes", "0"), "settings.invalid_value", "file:image_max_bytes", "optional_integer_min", "1"},
		{"pdf pages", set("file", "full_context_pdf_max_pages", "501"), "settings.invalid_value", "file:full_context_pdf_max_pages", "optional_integer_range", "1,500"},
		{"mime", set("file", "allowed_mime_types", "image/png,plain"), "settings.invalid_value", "file:allowed_mime_types", "mime", "-"},
		{"mime empty", set("file", "allowed_mime_types", ""), "settings.invalid_value", "file:allowed_mime_types", "required", "-"},
		{"embedding host junk", set("file", "embedding_host", "not a url"), "settings.embedding_invalid", "file:embedding_host", "trusted_http_url", "-"},
		{"embedding protocol", set("file", "embedding_protocol", "cohere"), "settings.embedding_invalid", "file:embedding_protocol", "enum", "openai,gemini,voyage,jina"},
		{"embedding timeout", set("file", "embedding_timeout_seconds", "601"), "settings.embedding_invalid", "file:embedding_timeout_seconds", "integer_range", "1,600"},
		{"embedding dims", set("file", "embedding_output_dimensions", "63"), "settings.embedding_invalid", "file:embedding_output_dimensions", "integer_range", "64,4096"},
		{"embedding policy", set("file", "embedding_dimensions_policy", "x"), "settings.invalid_value", "file:embedding_dimensions_policy", "enum", "send,omit"},
		{"embed chunk", set("file", "embed_chunk_size_tokens", "big"), "settings.embedding_invalid", "file:embed_chunk_size_tokens", "integer", "-"},
		{"rag top k", set("file", "rag_top_k", "51"), "settings.invalid_value", "file:rag_top_k", "integer_range", "1,50"},
		{"extract engine", set("extract", "engine", "word"), "settings.extract_invalid", "extract:engine", "enum", "builtin,tika,docling,mineru"},
		{"ocr engine", set("extract", "ocr_engine", "x"), "settings.extract_invalid", "extract:ocr_engine", "enum", "rapidocr,tesseract,paddle,tencent,aliyun,mistral,llm"},
		{"tika url", set("extract", "tika_base_url", "ftp://x"), "settings.extract_invalid", "extract:tika_base_url", "http_url", "-"},
		{"tika timeout", set("extract", "tika_timeout_seconds", "121"), "settings.extract_invalid", "extract:tika_timeout_seconds", "integer_range", "1,120"},
		{"docling timeout", set("extract", "docling_timeout_seconds", "601"), "settings.extract_invalid", "extract:docling_timeout_seconds", "integer_range", "1,600"},
		{"mineru types", set("extract", "mineru_file_types", "pdf,video"), "settings.extract_invalid", "extract:mineru_file_types", "file_type", "-"},
		{"mineru source", set("extract", "mineru_source", "saas"), "settings.extract_invalid", "extract:mineru_source", "enum", "cloud,self_hosted"},
		{"mistral url", set("extract", "mistral_ocr_base_url", "mistral"), "settings.extract_invalid", "extract:mistral_ocr_base_url", "trusted_http_url", "-"},
		{"tencent region", set("extract", "tencent_ocr_region", strings.Repeat("r", 256)), "settings.extract_invalid", "extract:tencent_ocr_region", "max_length", "255"},
		{"mcp timeout", set("mcp", "mcp_tool_timeout_seconds", "1801"), "settings.invalid_value", "mcp:mcp_tool_timeout_seconds", "integer_range", "0,1800"},
		{"mcp retry", set("mcp", "mcp_tool_retry_count", "6"), "settings.invalid_value", "mcp:mcp_tool_retry_count", "integer_range", "0,5"},
		{"mcp llm calls", set("mcp", "mcp_max_llm_calls_per_run", "1"), "settings.invalid_value", "mcp:mcp_max_llm_calls_per_run", "integer_range", "2,32"},
		{"mcp prompt", set("mcp", "mcp_tool_prompt", strings.Repeat("x", 20001)), "settings.invalid_value", "mcp:mcp_tool_prompt", "max_length", "20000"},
		{"circuit", set("circuit", "channel_failure_threshold", "x"), "settings.invalid_value", "circuit:channel_failure_threshold", "integer", "-"},
	}
	for _, c := range cases {
		w := h.patch(c.item)
		h.expectInvalid(w, c.code, c.field, c.rule, c.param)
	}
	// Nothing above may have written anything.
	if h.val("auth", "token_ttl_hours") != "24" || h.val("desktop", "download_url") != settings.RegistryDefault("desktop", "download_url") {
		t.Fatal("failed batches must not change anything")
	}
}

func TestValidationNeverEchoesTheValue(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	secret := "very-secret-value-0123456789" + strings.Repeat("z", 300)
	w := h.patch(set("auth", "smtp_password", secret))
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	if strings.Contains(w.Body.String(), "very-secret-value") {
		t.Fatalf("validation error echoed the value: %s", w.Body)
	}
	w = h.patch(set("auth", "token_ttl_hours", "ZZZ-LEAK"))
	if strings.Contains(w.Body.String(), "ZZZ-LEAK") {
		t.Fatalf("validation error echoed the value: %s", w.Body)
	}
	msg := testkit.Envelope(t, w).ErrorMsg
	if msg != "invalid setting value" {
		t.Fatalf("generic message expected, got %q", msg)
	}
}

func TestPatchAcceptsValidEdgeValues(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.advance(time.Minute)
	h.mustPatch(
		set("auth", "token_ttl_hours", "168"),
		set("auth", "login_max_failures", "0"),
		set("file", "full_context_max_tokens", ""),
		set("file", "file_full_context_max_bytes", "0"),
		set("file", "image_max_bytes", ""),
		set("file", "embedding_host", "http://10.0.0.5:8080"),
		set("chat", "context_compact_trigger_percent", ""),
		set("desktop", "download_url", "https://example.com/dl"),
		set("mcp", "mcp_tool_timeout_seconds", "0"),
		set("storage", "user_storage_quota_bytes", "0"),
		set("billing", "usd_to_cny_rate", "0.000001"),
		set("personal_provider", "blocked_hosts", "*.evil.com\nother.org, third.net"),
		set("auth", "email_registration_allowed_domains", "@a.com B.org"),
	)
	if h.val("auth", "token_ttl_hours") != "168" || h.val("file", "embedding_host") != "http://10.0.0.5:8080" {
		t.Fatal("values not stored")
	}
}

func TestInvalidNamespaceAndKey(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.expectInvalid(h.patch(set("foo", "x", "1")), "settings.invalid_namespace", "foo:x", "invalid_namespace", "-")
	h.expectInvalid(h.patch(set("auth", "nope", "1")), "settings.invalid_key", "auth:nope", "invalid_key", "-")
	h.expectInvalid(h.patch(set("content_moderation", "enabled", "true")), "settings.invalid_namespace", "", "invalid_namespace", "-")

	w := h.do("GET", "/api/v1/admin/settings/foo", h.admin, nil)
	testkit.Expect(t, w, 400, "settings.invalid_namespace")
	testkit.Expect(t, h.do("GET", "/api/v1/admin/settings/embedding", h.admin, nil), 400, "settings.invalid_namespace")
}

func TestPatchIsAtomic(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("auth", "token_ttl_hours", "100"), set("auth", "rate_limit_rpm", "0"))
	testkit.Expect(t, w, 400, "settings.invalid_value")
	if h.val("auth", "token_ttl_hours") != "24" {
		t.Fatal("the valid item of a rejected batch must not be applied")
	}
	// A dependency failure after validation also applies nothing.
	w = h.patch(set("chat", "default_system_prompt", "hello"), set("chat", "rag_enabled", "true"))
	testkit.Expect(t, w, 400, "settings.embedding_invalid")
	if h.val("chat", "default_system_prompt") != "" {
		t.Fatal("batch rejected by a rule must not be partially applied")
	}
}

func TestClearSemantics(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.expectInvalid(h.patch(clearItem("auth", "smtp_host")), "settings.smtp_invalid", "auth:smtp_host", "clear_not_allowed", "-")
	h.expectInvalid(h.patch(clearItem("auth", "token_ttl_hours")), "settings.invalid_value", "auth:token_ttl_hours", "clear_not_allowed", "-")

	h.mustPatch(set("auth", "smtp_password", "hunter2"))
	if !h.find("auth", "smtp_password").Configured {
		t.Fatal("password should be configured")
	}
	h.mustPatch(clearItem("auth", "smtp_password"))
	if it := h.find("auth", "smtp_password"); it.Configured || it.Value != "" || !it.Sensitive {
		t.Fatalf("cleared item: %+v", it)
	}
	if h.stored("auth", "smtp_password") != "" {
		t.Fatal("cleared secret must be stored blank")
	}
}

func TestSensitiveValuesAreMasked(t *testing.T) {
	audit := &fakeAudit{}
	h := newHarness(t, settings.Ports{Audit: audit})
	h.mustPatch(set("billing", "stripe_secret_key", "sk_x"))

	it := h.find("billing", "stripe_secret_key")
	if it.Value != "" || !it.Sensitive || !it.Configured {
		t.Fatalf("masked item: %+v", it)
	}
	raw := h.stored("billing", "stripe_secret_key")
	if raw == "" || raw == "sk_x" || strings.Contains(raw, "sk_x") {
		t.Fatalf("stored value must be ciphertext: %q", raw)
	}
	vals, err := h.svc.NamespaceValues(ctxBG(), "billing")
	if err != nil || vals["stripe_secret_key"] != "sk_x" {
		t.Fatalf("runtime read: %v %v", vals["stripe_secret_key"], err)
	}
	// namespace listing masks too
	w := h.do("GET", "/api/v1/admin/settings/billing", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	if strings.Contains(w.Body.String(), "sk_x") || strings.Contains(w.Body.String(), raw) {
		t.Fatal("namespace listing leaked a secret")
	}

	entries := audit.all()
	if len(entries) != 1 {
		t.Fatalf("audit entries: %d", len(entries))
	}
	e := entries[0]
	if e.Action != "settings.update" || e.Resource != "system_settings" || e.ActorUserID != 1 || e.RequestID == "" {
		t.Fatalf("audit entry: %+v", e)
	}
	if strings.Contains(toJSON(t, e.Detail), "sk_x") || !strings.Contains(toJSON(t, e.Detail), "[REDACTED]") {
		t.Fatalf("audit detail not redacted: %s", toJSON(t, e.Detail))
	}
}

func TestBlankSensitiveValueLeavesSecretUntouched(t *testing.T) {
	audit := &fakeAudit{}
	h := newHarness(t, settings.Ports{Audit: audit})
	h.mustPatch(set("auth", "smtp_password", "secret1"))
	before := h.stored("auth", "smtp_password")

	for _, v := range []string{"", "   "} {
		w := h.patch(set("auth", "smtp_password", v))
		testkit.Expect(t, w, 200, "")
	}
	if h.stored("auth", "smtp_password") != before {
		t.Fatal("blank sensitive value must not change the secret")
	}
	if !h.find("auth", "smtp_password").Configured {
		t.Fatal("still configured")
	}
	// A blank sensitive item also takes no part in dependency checks.
	w := h.patch(set("auth", "email_verification_enabled", "true"), set("auth", "smtp_password", ""))
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	// audit keeps blanks blank
	last := audit.all()[len(audit.all())-1]
	if strings.Contains(toJSON(t, last.Detail), "REDACTED") {
		t.Fatal("blank values are not redacted")
	}
}

func TestStoredValueKeepsSubmittedWhitespace(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("chat", "default_system_prompt", "  padded prompt \n"), set("auth", "smtp_host", " mail.example.com "))
	if h.stored("chat", "default_system_prompt") != "  padded prompt \n" || h.stored("auth", "smtp_host") != " mail.example.com " {
		t.Fatal("stored value must be what was submitted")
	}
	vals, _ := h.svc.NamespaceValues(ctxBG(), "chat")
	if vals["default_system_prompt"] != "padded prompt" {
		t.Fatalf("runtime reads are trimmed: %q", vals["default_system_prompt"])
	}
	if h.svc.Runtime().Auth.SMTPHost != "mail.example.com" {
		t.Fatalf("runtime host: %q", h.svc.Runtime().Auth.SMTPHost)
	}
	// validation sees the trimmed value
	h.mustPatch(set("auth", "token_ttl_hours", " 30 "))
	if h.svc.Runtime().Auth.TokenTTLHours != 30 {
		t.Fatal("trimmed integer should parse")
	}
}

func TestDuplicateKeysInOneBatch(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("auth", "token_ttl_hours", "10"), set("auth", "token_ttl_hours", "20"))
	if h.val("auth", "token_ttl_hours") != "20" {
		t.Fatalf("later item wins: %s", h.val("auth", "token_ttl_hours"))
	}
	if h.rowCount("auth", "token_ttl_hours") != 1 {
		t.Fatal("no duplicate rows")
	}
}

func TestNamespaceListingSortedAndUnfiltered(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	now := time.Now()
	h.env.DB.Create(&settings.SystemSetting{Namespace: "chat", Key: "stale_key", Value: "x", ValueType: "string", CreatedAt: now, UpdatedAt: now})

	w := h.do("GET", "/api/v1/admin/settings/chat", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	var items []item
	testkit.Decode(t, w, &items)
	prev := ""
	stale := false
	for _, it := range items {
		if it.Key < prev {
			t.Fatalf("not sorted by key: %s after %s", it.Key, prev)
		}
		prev = it.Key
		if it.Key == "stale_key" {
			stale = true
		}
	}
	if !stale {
		t.Fatal("per-namespace listing keeps unregistered rows")
	}
	for _, it := range h.listAll()["chat"] {
		if it.Key == "stale_key" {
			t.Fatal("full listing must hide unregistered rows")
		}
	}
}

func TestAuditRecordedForEveryPatch(t *testing.T) {
	audit := &fakeAudit{}
	h := newHarness(t, settings.Ports{Audit: audit})
	h.mustPatch(set("auth", "token_ttl_hours", "30"), clearItem("auth", "smtp_password"))
	e := audit.all()[0]
	items, _ := e.Detail["items"].([]map[string]any)
	if len(items) != 2 || items[0]["key"] != "token_ttl_hours" || items[0]["value"] != "30" || items[1]["clear"] != true {
		t.Fatalf("audit items: %+v", e.Detail)
	}
	// rejected batches are not audited
	h.patch(set("auth", "token_ttl_hours", "0"))
	if len(audit.all()) != 1 {
		t.Fatal("failed patch must not be audited")
	}
}

func TestPatchValuesAndLoadBilling(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	ctx := ctxBG()
	if err := h.svc.PatchValues(ctx, "billing", map[string]string{"mode": "usage", "display_currency": "CNY"}); err != nil {
		t.Fatal(err)
	}
	vals, err := h.svc.LoadBilling(ctx)
	if err != nil || vals["mode"] != "usage" || vals["display_currency"] != "CNY" || vals["usd_to_cny_rate"] != "7.2" {
		t.Fatalf("billing values: %v %v", vals, err)
	}
	if err := h.svc.PatchValues(ctx, "billing", map[string]string{"mode": "bad"}); err == nil {
		t.Fatal("invalid value must be rejected")
	}
	if _, err := h.svc.NamespaceValues(ctx, "nope"); err == nil {
		t.Fatal("unknown namespace must fail")
	}
}
