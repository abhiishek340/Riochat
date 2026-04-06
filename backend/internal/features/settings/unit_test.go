package settings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegistryShape(t *testing.T) {
	if len(reg.list) < 150 {
		t.Fatalf("registry unexpectedly small: %d", len(reg.list))
	}
	seen := map[string]bool{}
	for _, d := range reg.list {
		id := d.ns + "/" + d.key
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
		switch d.typ {
		case typeString, typeInt, typeBool, typeJSON:
		default:
			t.Errorf("%s: bad type %q", id, d.typ)
		}
		if len(d.ns) > 32 || len(d.key) > 128 || len(d.desc) > 255 {
			t.Errorf("%s: column limits exceeded", id)
		}
		// A default must satisfy the key's own validator (sensitive blanks are
		// stored without validation).
		if d.check != nil && !(d.sensitive && d.def == "") {
			if v := d.check(strings.TrimSpace(d.def)); v != nil && !(d.def == "" && d.typ != typeBool) {
				t.Errorf("%s: default %q fails rule %s", id, d.def, v.rule)
			}
		}
	}
}

func TestSensitiveSet(t *testing.T) {
	want := map[string]bool{
		"auth/smtp_password": true, "auth/turnstile_secret_key": true,
		"billing/stripe_secret_key": true, "billing/stripe_webhook_secret": true, "billing/epay_key": true,
		"file/embedding_key":                   true,
		"extract/tika_auth_token":              true,
		"extract/docling_auth_token":           true,
		"extract/tesseract_ocr_auth_token":     true,
		"extract/rapidocr_auth_token":          true,
		"extract/paddle_ocr_auth_token":        true,
		"extract/mineru_auth_token":            true,
		"extract/mistral_ocr_auth_token":       true,
		"extract/llm_ocr_auth_token":           true,
		"extract/tencent_ocr_secret_key":       true,
		"extract/aliyun_ocr_access_key_secret": true,
	}
	got := map[string]bool{}
	for _, k := range SensitiveKeys() {
		got[k.Namespace+"/"+k.Key] = true
	}
	if len(got) != len(want) {
		t.Fatalf("sensitive keys: got %d want %d: %v", len(got), len(want), got)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing sensitive key %s", k)
		}
	}
	if !IsSensitive(" auth ", " smtp_password ") || IsSensitive("auth", "smtp_host") || IsSensitive("x", "y") {
		t.Error("IsSensitive misreports")
	}
}

func TestNamespaces(t *testing.T) {
	for _, ns := range []string{"auth", "billing", "chat", "knowledgebase", "personal_provider", "desktop", "storage", "file", "extract", "mcp", "circuit"} {
		if !ValidNamespace(ns) || !ValidNamespace(" "+ns+" ") {
			t.Errorf("namespace %s should be valid", ns)
		}
	}
	if ValidNamespace("foo") || ValidNamespace("") || ValidNamespace("content_moderation") {
		t.Error("unknown namespace accepted")
	}
}

func TestGroupCodes(t *testing.T) {
	cases := []struct {
		ns, key string
		want    codeGroup
	}{
		{"auth", "smtp_host", groupSMTP},
		{"auth", "smtp_password", groupSMTP},
		{"auth", "token_ttl_hours", groupValue},
		{"billing", "payment_providers", groupBilling},
		{"billing", "epay_types", groupBilling},
		{"billing", "stripe_secret_key", groupBilling},
		{"billing", "mode", groupValue},
		{"chat", "model_option_allowed_paths", groupModelOption},
		{"chat", "model_option_policy_mode", groupModelOption},
		{"chat", "rag_enabled", groupValue},
		{"file", "embedding_host", groupEmbedding},
		{"file", "embed_batch_size", groupEmbedding},
		{"file", "rag_top_k", groupValue},
		{"file", "rag_model", groupValue},
		{"file", "image_max_dimension", groupValue},
		{"extract", "engine", groupExtract},
		{"extract", "tika_base_url", groupExtract},
		{"desktop", "download_url", groupValue},
	}
	for _, c := range cases {
		if got := groupOf(c.ns, c.key); got != c.want {
			t.Errorf("%s/%s: group %d want %d", c.ns, c.key, got, c.want)
		}
	}
}

func TestChecks(t *testing.T) {
	type tc struct {
		name  string
		c     check
		in    string
		rule  string
		param string
	}
	cases := []tc{
		{"int range ok", intRange(1, 168), "168", "", ""},
		{"int range low", intRange(1, 168), "0", "integer_range", "1,168"},
		{"int range high", intRange(1, 168), "169", "integer_range", "1,168"},
		{"int range text", intRange(1, 168), "abc", "integer", ""},
		{"int range empty", intRange(1, 168), "", "integer", ""},
		{"int min ok", intMin(0), "0", "", ""},
		{"int min bad", intMin(1), "0", "integer_min", "1"},
		{"opt range empty", optIntRange(128, 1000000), "", "", ""},
		{"opt range zero", optIntRange(128, 1000000), "0", "", ""},
		{"opt range edge", optIntRange(128, 1000000), "128", "", ""},
		{"opt range low", optIntRange(128, 1000000), "64", "optional_integer_range", "128,1000000"},
		{"opt range text", optIntRange(128, 1000000), "x", "optional_integer_range", "128,1000000"},
		{"opt min empty", optIntMin(1), "", "", ""},
		{"opt min bad", optIntMin(1), "0", "optional_integer_min", "1"},
		{"float ok", floatRange(0.000001, 1), "0.45", "", ""},
		{"float zero", floatRange(0.000001, 1), "0", "float_range", "0.000001,1"},
		{"float nan", floatRange(0, 10), "NaN", "float_range", "0,10"},
		{"float inf", floatRange(0, 10), "Inf", "float_range", "0,10"},
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		{"max chars multibyte", maxChars(3), "\u4f60\u597d\u5417", "", ""},
		{"max chars over", maxChars(3), "abcd", "max_length", "3"},
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		{"max bytes multibyte", maxBytes(3), "\u4f60\u597d", "max_length", "3"},
		{"enum ok", oneOf("a", "b"), "b", "", ""},
		{"enum bad", oneOf("a", "b"), "c", "enum", "a,b"},
		{"bool t", isBool(), "T", "", ""},
		{"bool 0", isBool(), "0", "", ""},
		{"bool bad", isBool(), "yes", "bool", ""},
		{"http empty", optHTTPURL(), "", "", ""},
		{"http ok", optHTTPURL(), "http://127.0.0.1:9998", "", ""},
		{"http bad scheme", optHTTPURL(), "ftp://x.com", "http_url", ""},
		{"http no host", optHTTPURL(), "http://", "http_url", ""},
		{"trusted private", trustedHTTPURL(), "http://10.0.0.5:8080", "", ""},
		{"trusted junk", trustedHTTPURL(), "not a url", "trusted_http_url", ""},
		{"trusted creds", trustedHTTPURL(), "https://u:p@x.com", "trusted_http_url", ""},
		{"download js", downloadURLCheck(), "javascript:alert(1)", "download_url", ""},
		{"download creds", downloadURLCheck(), "https://u:p@x.com/", "download_url", ""},
		{"download empty", downloadURLCheck(), "", "required", ""},
		{"download ok", downloadURLCheck(), "https://example.com/dl", "", ""},
		{"download long", downloadURLCheck(), "https://example.com/" + strings.Repeat("a", 200), "max_length", "200"},
		{"login path ok", loginPathCheck(), "/chat", "", ""},
		{"login path double slash", loginPathCheck(), "//evil.com", "local_path", ""},
		{"login path relative", loginPathCheck(), "chat", "local_path", ""},
		{"login path empty", loginPathCheck(), "", "required", ""},
		{"login path long", loginPathCheck(), "/" + strings.Repeat("a", 120), "max_length", "120"},
		{"domains ok", domainListCheck(), "a.com, @b.org\nC.NET", "", ""},
		{"domains empty", domainListCheck(), "", "", ""},
		{"domains no dot", domainListCheck(), "localhost", "domain", ""},
		{"domains at", domainListCheck(), "x@y.com", "domain", ""},
		{"domains scheme", domainListCheck(), "https://a.com", "domain", ""},
		{"hosts ok", hostListCheck(), "*.evil.com, other.org", "", ""},
		{"hosts scheme", hostListCheck(), "http://x.com", "domain", ""},
		{"hosts port", hostListCheck(), "x.com:80", "domain", ""},
		{"hosts path", hostListCheck(), "x.com/a", "domain", ""},
		{"mime ok", mimeListCheck(), "image/png,text/plain", "", ""},
		{"mime bad", mimeListCheck(), "image/png,plain", "mime", ""},
		{"mime empty", mimeListCheck(), "", "required", ""},
		{"file types ok", fileTypeListCheck(), "PDF, word,,excel", "", ""},
		{"file types bad", fileTypeListCheck(), "pdf,video", "file_type", ""},
		{"providers ok", providerListCheck(), "Stripe, epay, stripe, disabled", "", ""},
		{"providers bad", providerListCheck(), "stripe,paypal", "payment_provider", "stripe,epay"},
		{"epay url ok", epayGatewayCheck(), "https://pay.example.com", "", ""},
		{"epay url empty", epayGatewayCheck(), "", "", ""},
		{"epay url bad", epayGatewayCheck(), "pay.example.com", "epay_url", ""},
		{"trigger empty", triggerPercentCheck(), "", "", ""},
		{"trigger zero", triggerPercentCheck(), "0", "", ""},
		{"trigger low", triggerPercentCheck(), "5", "integer_range", "10,95"},
		{"trigger high", triggerPercentCheck(), "96", "integer_range", "10,95"},
		{"trigger ok", triggerPercentCheck(), "95", "", ""},
	}
	for _, c := range cases {
		v := c.c(c.in)
		switch {
		case c.rule == "" && v != nil:
			t.Errorf("%s: unexpected failure %+v", c.name, *v)
		case c.rule != "" && v == nil:
			t.Errorf("%s: expected rule %s", c.name, c.rule)
		case v != nil && (v.rule != c.rule || v.param != c.param):
			t.Errorf("%s: got %s/%q want %s/%q", c.name, v.rule, v.param, c.rule, c.param)
		}
	}
}

func TestEpayTypes(t *testing.T) {
	mk := func(n int) string {
		var items []map[string]string
		for i := 0; i < n; i++ {
			items = append(items, map[string]string{"name": "n", "type": "t" + string(rune('a'+i))})
		}
		b, _ := json.Marshal(items)
		return string(b)
	}
	cases := []struct {
		name, in, rule, param string
	}{
		{"default", defaultEpayTypes, "", ""},
		{"not json", "nope", "json_array", ""},
		{"object", `{"a":1}`, "json_array", ""},
		{"null", `null`, "json_array", ""},
		{"empty list", `[]`, "payment_count", "1,10"},
		{"eleven", mk(11), "payment_count", "1,10"},
		{"ten", mk(10), "", ""},
		{"duplicate", `[{"name":"a","type":"x"},{"name":"b","type":"x"}]`, "payment_type_unique", ""},
		{"missing name", `[{"type":"x"}]`, "payment_fields", ""},
		{"blank name", `[{"name":" ","type":"x"}]`, "payment_fields", ""},
		{"item not object", `["x"]`, "payment_fields", ""},
		{"name type wrong", `[{"name":1,"type":"x"}]`, "payment_fields", ""},
		{"long name", `[{"name":"` + strings.Repeat("a", 65) + `","type":"x"}]`, "payment_item_length", "name,64"},
		{"long type", `[{"name":"a","type":"` + strings.Repeat("a", 33) + `"}]`, "payment_item_length", "type,32"},
		{"bad chars", `[{"name":"a","type":"a b"}]`, "payment_type_chars", ""},
		{"bad chars dollar", `[{"name":"a","type":"a$"}]`, "payment_type_chars", ""},
	}
	for _, c := range cases {
		v := epayTypesCheck()(c.in)
		switch {
		case c.rule == "" && v != nil:
			t.Errorf("%s: unexpected %+v", c.name, *v)
		case c.rule != "" && (v == nil || v.rule != c.rule || v.param != c.param):
			t.Errorf("%s: got %+v want %s/%s", c.name, v, c.rule, c.param)
		}
	}
}

func TestModelOptionDocCheck(t *testing.T) {
	cases := []struct {
		name, in, rule, param string
	}{
		{"default allow", defaultAllowedPathsJSON, "", ""},
		{"default deny", defaultDeniedPathsJSON, "", ""},
		{"blank", "  ", "required", ""},
		{"too long", `{"default":["` + strings.Repeat("a", 20000) + `"]}`, "max_length", "20000"},
		{"not json", "nope", "json_object", ""},
		{"array", `[]`, "json_object", ""},
		{"null", `null`, "json_object", ""},
		{"not string arrays", `{"default":"a"}`, "json_object", ""},
		{"number in array", `{"default":["a",1]}`, "json_object", ""},
		{"bogus protocol", `{"bogus":["a"]}`, "model_option_protocol", ""},
		{"protocol with spaces ok", `{" default ":["a"]}`, "", ""},
		{"empty path", `{"default":[""]}`, "model_option_path", "empty"},
		{"segments", `{"default":["a..b"]}`, "model_option_path", "segments"},
		{"leading dot", `{"default":[".a"]}`, "model_option_path", "segments"},
		{"trailing dot", `{"default":["a."]}`, "model_option_path", "segments"},
		{"whitespace", `{"default":["a b"]}`, "model_option_path", "whitespace"},
		{"characters", `{"default":["a$"]}`, "model_option_path", "characters"},
		{"nested ok", `{"gemini_generate_content":["generationConfig.thinkingConfig.thinkingLevel"],"xai_video":["a-b_c"]}`, "", ""},
		{"empty object ok", `{}`, "", ""},
		{"empty list ok", `{"default":[]}`, "", ""},
	}
	for _, c := range cases {
		v := modelOptionDocCheck()(c.in)
		switch {
		case c.rule == "" && v != nil:
			t.Errorf("%s: unexpected %+v", c.name, *v)
		case c.rule != "" && (v == nil || v.rule != c.rule || v.param != c.param):
			t.Errorf("%s: got %+v want %s/%s", c.name, v, c.rule, c.param)
		}
	}
}

func TestDefaultDocsCoverEveryProtocol(t *testing.T) {
	var doc map[string][]string
	if err := json.Unmarshal([]byte(defaultAllowedPathsJSON), &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range modelOptionProtocols {
		if len(doc[p]) == 0 {
			t.Errorf("default allow document has no paths for %s", p)
		}
	}
	if len(doc) != len(modelOptionProtocols) {
		t.Errorf("document has %d protocols, want %d", len(doc), len(modelOptionProtocols))
	}
	var deny map[string][]string
	if err := json.Unmarshal([]byte(defaultDeniedPathsJSON), &deny); err != nil || len(deny) != 1 || len(deny["default"]) != 14 {
		t.Errorf("deny document: %v %v", deny, err)
	}
	interactions := strings.Join(doc["gemini_interactions"], ",")
	for _, want := range []string{"generation_config.thinking_summaries", "response_format.schema", "generation_config.video_config.task"} {
		if !strings.Contains(interactions, want) {
			t.Errorf("gemini_interactions lacks %s", want)
		}
	}
}

func TestHistoricalAllowedDocs(t *testing.T) {
	current := builtinAllowedDoc()
	if isHistoricalAllowedDoc(marshalDoc(current)) {
		t.Error("the current default must not count as historical")
	}
	noVideo := builtinAllowedDoc()
	delete(noVideo, "xai_video")
	delete(noVideo, "xai_video_extensions")
	if !isHistoricalAllowedDoc(marshalDoc(noVideo)) {
		t.Error("default minus video keys should be historical")
	}
	onlyVideo := builtinAllowedDoc()
	delete(onlyVideo, "xai_video")
	if !isHistoricalAllowedDoc(marshalDoc(onlyVideo)) {
		t.Error("default minus xai_video should be historical")
	}
	custom := builtinAllowedDoc()
	delete(custom, "xai_video")
	custom["default"] = append(custom["default"], "my_param")
	if isHistoricalAllowedDoc(marshalDoc(custom)) {
		t.Error("a document with an extra custom path must be kept")
	}
	if isHistoricalAllowedDoc("not json") || isHistoricalAllowedDoc("") || isHistoricalAllowedDoc("null") {
		t.Error("unparseable documents must be kept")
	}
	// order-insensitive
	reordered := builtinAllowedDoc()
	delete(reordered, "xai_video")
	p := reordered["default"]
	p[0], p[1] = p[1], p[0]
	if !isHistoricalAllowedDoc(marshalDoc(reordered)) {
		t.Error("comparison must ignore order")
	}
	// camelCase interactions naming and missing summaries/thinking paths
	old := builtinAllowedDoc()
	delete(old, "xai_video")
	delete(old, "xai_video_extensions")
	old["gemini_generate_content"] = without(old["gemini_generate_content"],
		"generationConfig.thinkingConfig.includeThoughts", "generationConfig.thinkingConfig.thinkingLevel")
	var gi []string
	for _, x := range old["gemini_interactions"] {
		if x == "generation_config.thinking_summaries" {
			continue
		}
		gi = append(gi, strings.Replace(x, "response_format.", "responseFormat.", 1))
	}
	old["gemini_interactions"] = gi
	old["xai_responses"] = []string{"reasoning.effort"}
	if !isHistoricalAllowedDoc(marshalDoc(old)) {
		t.Error("oldest generation should be recognised")
	}
}

func TestNativeToolPricing(t *testing.T) {
	if v := nativeToolPricingCheck()(defaultNativeToolPricingJSON); v != nil {
		t.Fatalf("default pricing document rejected: %+v", *v)
	}
	var doc map[string]pricingEntry
	if err := json.Unmarshal([]byte(defaultNativeToolPricingJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openai.web_search"].PriceNanoUSD != 25_000_000 || doc["anthropic.web_search_20260209"].PriceNanoUSD != 10_000_000 ||
		doc["xai.x_search"].PriceNanoUSD != 5_000_000 {
		t.Errorf("default prices: %+v", doc)
	}
	if _, ok := doc["xai.attachment_search"]; ok || len(doc) != 20 {
		t.Errorf("default document lists %d keys (usage-only keys must be absent)", len(doc))
	}
	if doc["openai.shell"].PriceLabel != "" {
		t.Error("default price labels are empty")
	}
	if v := nativeToolPricingCheck()(`{"xai.attachment_search":{"priceNanousd":1}}`); v != nil {
		t.Error("usage-only keys must be accepted")
	}
	if doc["google.google_search"].Billable || doc["openai.shell"].Billable {
		t.Error("unmetered tools must not be billable")
	}
	cases := []struct {
		in string
		ok bool
	}{
		{`{}`, true},
		{`{"openai.web_search":{"priceNanousd":1}}`, true},
		{`{"unknown.tool":{"priceNanousd":1}}`, false},
		{`{"openai.web_search":{"priceNanousd":-1}}`, false},
		{`{"openai.web_search":"x"}`, false},
		{`[]`, false},
		{``, false},
		{`null`, false},
	}
	for _, c := range cases {
		if got := nativeToolPricingCheck()(c.in) == nil; got != c.ok {
			t.Errorf("%q: valid=%v want %v", c.in, got, c.ok)
		}
	}
}

func TestNativeToolCatalog(t *testing.T) {
	tools := builtinNativeTools()
	seen := map[string]bool{}
	for _, tl := range tools {
		id := tl.Protocol + "/" + tl.ToolKey
		if seen[id] {
			t.Errorf("duplicate %s", id)
		}
		seen[id] = true
		if tl.Payload == nil || tl.Label == "" || tl.BillingUnit == "" || !tl.DefaultEnabled {
			t.Errorf("incomplete entry %+v", tl)
		}
		if tl.Billable != (tl.PriceNanoUSD > 0) {
			t.Errorf("%s: billable/price mismatch", id)
		}
		if !tl.Billable && tl.PriceLabel != priceNotMetered && tl.PriceLabel != priceIncluded {
			t.Errorf("%s: unmetered tool needs a label", id)
		}
	}
	for _, id := range []string{"openai_responses/openai.shell", "anthropic_messages/anthropic.web_search_20260209", "gemini_interactions/google.url_context"} {
		if !seen[id] {
			t.Errorf("catalog lacks %s", id)
		}
	}
}

func TestRuntimeDefaultsAndNormalize(t *testing.T) {
	rt := defaultRuntime()
	if rt.Auth.TokenTTLHours != 24 || rt.Auth.RefreshTokenTTLHours != 720 || !rt.Auth.EmailLoginEnabled || rt.Auth.SMTPPort != 587 {
		t.Errorf("auth defaults: %+v", rt.Auth)
	}
	if rt.Chat.ContextWindowFallbackTokens != 128000 || rt.Chat.ContextCompactTriggerPercent != 80 || rt.Chat.RAGMinSimilarity != 0.45 {
		t.Errorf("chat defaults: %+v", rt.Chat)
	}
	if rt.Storage.MaxUploadFileBytes != 20971520 || rt.File.ImageMaxBytes != 0 || rt.File.FullContextPDFMaxPages != 20 {
		t.Errorf("storage/file defaults: %+v %+v", rt.Storage, rt.File)
	}
	if rt.Chat.ModelOptionAllowedPaths != defaultAllowedPathsJSON || rt.Desktop.DownloadURL != defaultDesktopURL {
		t.Error("defaults not projected")
	}

	r := defaultRuntime()
	r.Auth.EmailLoginEnabled = false
	r.Auth.EmailRegistrationEnabled = true
	r.Auth.TurnstileRegistrationEnabled = true
	r.Auth.EmailVerificationEnabled = true
	r.Auth.PasswordResetEnabled = true
	r.normalize()
	if r.Auth.EmailRegistrationEnabled || r.Auth.TurnstileRegistrationEnabled {
		t.Error("email login off must force registration and turnstile off")
	}
	if !r.Auth.PasswordResetEnabled {
		t.Error("password reset stays on while username login and verification are on")
	}
	r.Auth.UsernameLoginEnabled = false
	r.normalize()
	if r.Auth.PasswordResetEnabled {
		t.Error("password reset needs a login method")
	}

	r = defaultRuntime()
	r.Chat.RAGEnabled, r.Chat.MessageEmbeddingEnabled, r.Chat.SemanticContextEnabled = true, true, true
	r.File.EmbeddingEnabled, r.File.EmbeddingHost = true, "http://e"
	r.normalize()
	if !r.Chat.RAGEnabled || !r.Chat.SemanticContextEnabled {
		t.Error("embedding fully configured keeps the switches")
	}
	r.File.EmbeddingHost = " "
	r.normalize()
	if r.Chat.RAGEnabled || r.Chat.MessageEmbeddingEnabled || r.Chat.SemanticContextEnabled {
		t.Error("blank host must force switches off")
	}
	r = defaultRuntime()
	r.File.EmbeddingEnabled, r.File.EmbeddingHost = true, "http://e"
	r.Chat.SemanticContextEnabled, r.Chat.MessageEmbeddingEnabled = true, false
	r.normalize()
	if r.Chat.SemanticContextEnabled {
		t.Error("semantic context needs message embedding")
	}

	r = defaultRuntime()
	r.Auth.TokenTTLHours, r.Auth.RefreshTokenTTLHours = 0, -1
	r.Chat.ModelOptionPolicyMode = "weird"
	r.Chat.ModelOptionAllowedPaths, r.Chat.ModelOptionDeniedPaths = " ", ""
	r.Chat.ContextWindowFallbackTokens = 10
	r.Chat.ContextCompactTriggerPercent = 5
	r.MCP.MaxSelectedToolsPerMessage, r.MCP.ToolTimeoutSeconds = 500, 0
	r.Desktop.DownloadURL = "javascript:x"
	r.File.FullContextLimitEnabled = false
	r.File.FullContextMaxBytes, r.File.FullContextMaxTokens, r.File.FullContextPDFMaxPages = 1, 2, 3
	r.normalize()
	switch {
	case r.Auth.TokenTTLHours != 24 || r.Auth.RefreshTokenTTLHours != 720:
		t.Error("ttl fallback")
	case r.Chat.ModelOptionPolicyMode != "allowlist" || r.Chat.ModelOptionAllowedPaths != defaultAllowedPathsJSON || r.Chat.ModelOptionDeniedPaths != defaultDeniedPathsJSON:
		t.Error("model option fallback")
	case r.Chat.ContextWindowFallbackTokens != 128000 || r.Chat.ContextCompactTriggerPercent != 80:
		t.Error("compaction fallback")
	case r.MCP.MaxSelectedToolsPerMessage != 128 || r.MCP.ToolTimeoutSeconds != 10:
		t.Error("mcp clamps")
	case r.Desktop.DownloadURL != defaultDesktopURL:
		t.Error("desktop url fallback")
	case r.File.FullContextMaxBytes != 0 || r.File.FullContextMaxTokens != 0 || r.File.FullContextPDFMaxPages != 0:
		t.Error("full context limits must be zeroed")
	}
	r.MCP.MaxSelectedToolsPerMessage = -3
	r.normalize()
	if r.MCP.MaxSelectedToolsPerMessage != 32 {
		t.Error("non-positive selection limit becomes 32")
	}
}

func TestApplyValueKeepsPreviousOnParseFailure(t *testing.T) {
	n, b, f, s := 5, true, 0.5, "x"
	applyValue(&n, "abc", false)
	applyValue(&b, "maybe", false)
	applyValue(&f, "zz", false)
	applyValue(&n, "", false)
	if n != 5 || !b || f != 0.5 {
		t.Errorf("parse failures must keep values: %d %v %v", n, b, f)
	}
	applyValue(&n, "", true)
	if n != 0 {
		t.Error("empty optional number means zero")
	}
	applyValue(&n, " 42 ", false)
	applyValue(&b, "0", false)
	applyValue(&f, "0.25", false)
	applyValue(&s, "  padded  ", false)
	var i64 int64
	applyValue(&i64, "9000000000", false)
	if n != 42 || b || f != 0.25 || s != "padded" || i64 != 9000000000 {
		t.Errorf("apply: %d %v %v %q %d", n, b, f, s, i64)
	}
}

func TestSignatures(t *testing.T) {
	a := ModelSignature("m", 3)
	if !strings.HasSuffix(a, "@3") || len(a) != 8+2 {
		t.Errorf("model signature shape: %s", a)
	}
	if ModelSignature("m", 3) != a || ModelSignature("m", 4) == a || ModelSignature("n", 3) == a {
		t.Error("model signature must depend on model and dimensions")
	}
	sp := SpaceSignature("m", 3, "http://h/")
	if !strings.HasSuffix(sp, "@3") || len(sp) != 16+2 {
		t.Errorf("space signature shape: %s", sp)
	}
	if SpaceSignature("m", 3, " http://h ") != sp || SpaceSignature("m", 3, "http://h///") != sp {
		t.Error("endpoint is normalised")
	}
	if SpaceSignature("m", 3, "http://other") == sp {
		t.Error("space signature must depend on the endpoint")
	}
}

func TestCollapseRows(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id uint, ns, key, val string, created, updated time.Time) SystemSetting {
		return SystemSetting{ID: id, Namespace: ns, Key: key, Value: val, CreatedAt: created, UpdatedAt: updated}
	}
	// touched row wins over an untouched default
	rows := []SystemSetting{
		mk(1, "auth", "token_ttl_hours", "24", t0, t0),
		mk(2, "auth", "token_ttl_hours", "24", t0, t0.Add(time.Hour)),
		mk(3, "auth", "smtp_host", "", t0, t0),
	}
	eff, losers := collapseRows(rows)
	if len(eff) != 2 || eff[0].ID != 2 || eff[1].ID != 3 || len(losers) != 1 || losers[0] != 1 {
		t.Fatalf("touched row must win and order must be first appearance: %+v %v", eff, losers)
	}
	// value differing from the default counts as modified even at equal times
	rows = []SystemSetting{
		mk(1, "auth", "token_ttl_hours", "48", t0, t0),
		mk(2, "auth", "token_ttl_hours", "24", t0, t0),
	}
	if eff, _ = collapseRows(rows); eff[0].ID != 1 {
		t.Error("changed value should win over default")
	}
	// both modified: more recent wins
	rows = []SystemSetting{
		mk(1, "auth", "token_ttl_hours", "48", t0, t0.Add(time.Hour)),
		mk(2, "auth", "token_ttl_hours", "72", t0, t0.Add(2*time.Hour)),
	}
	if eff, _ = collapseRows(rows); eff[0].ID != 2 {
		t.Error("most recent update should win")
	}
	// equal everything: lowest id
	rows = []SystemSetting{
		mk(5, "auth", "token_ttl_hours", "24", t0, t0),
		mk(3, "auth", "token_ttl_hours", "24", t0, t0),
		mk(9, "auth", "token_ttl_hours", "24", t0, t0),
	}
	eff, losers = collapseRows(rows)
	if eff[0].ID != 3 || len(losers) != 2 {
		t.Errorf("lowest id should win: %+v %v", eff, losers)
	}
	// unregistered keys: only the timestamp comparison applies
	rows = []SystemSetting{
		mk(1, "zzz", "k", "a", t0, t0),
		mk(2, "zzz", "k", "b", t0, t0),
	}
	if eff, _ = collapseRows(rows); eff[0].ID != 1 {
		t.Error("unregistered equal rows: lowest id")
	}
}

func TestUserSettingValidators(t *testing.T) {
	tooMany := "[" + strings.TrimSuffix(strings.Repeat("1,", 129), ",") + "]"
	ok128 := "[" + strings.TrimSuffix(strings.Repeat("1,", 128), ",") + "]"
	idCases := []struct {
		in string
		ok bool
	}{
		{"[]", true}, {"[1,2]", true}, {" [1, 2] ", true}, {"[1,2,0]", false}, {"[-1]", false},
		{"[1.5]", false}, {`["1"]`, false}, {"{}", false}, {"null", false}, {"", false}, {"[1,", false},
		{"[1] x", false}, {ok128, true}, {tooMany, false}, {"[null]", false}, {"[true]", false}, {"[1e2]", false},
	}
	for _, c := range idCases {
		if got := validToolIDs(c.in); got != c.ok {
			t.Errorf("tool ids %q: %v want %v", c.in, got, c.ok)
		}
	}
	six := `{"a":"toolbar","b":"toolbar","c":"toolbar","d":"toolbar","e":"toolbar","f":"toolbar"}`
	seven := `{"a":"toolbar","b":"toolbar","c":"toolbar","d":"toolbar","e":"toolbar","f":"toolbar","g":"toolbar"}`
	var many strings.Builder
	many.WriteString("{")
	for i := 0; i < 129; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`"k` + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":"menu"`)
	}
	many.WriteString("}")
	plCases := []struct {
		in string
		ok bool
	}{
		{"{}", true}, {`{"web":"toolbar","img":"menu"}`, true}, {six, true}, {seven, false},
		{`{"` + strings.Repeat("x", 96) + `":"menu"}`, true}, {`{"` + strings.Repeat("x", 97) + `":"menu"}`, false},
		{`{" ":"menu"}`, false}, {`{"":"menu"}`, false}, {`{"a":"dock"}`, false}, {`{"a":1}`, false},
		{"[]", false}, {"null", false}, {"", false}, {many.String(), false}, {`{"a":"menu"} x`, false},
	}
	for _, c := range plCases {
		if got := validPlacements(c.in); got != c.ok {
			t.Errorf("placements %.60q: %v want %v", c.in, got, c.ok)
		}
	}
	if len(userKeys) != 23 {
		t.Errorf("user keys: %d", len(userKeys))
	}
}

func TestResolveAgainst(t *testing.T) {
	cases := []struct{ base, in, want string }{
		{"https://app.example.com", "/pwa/icon-192.png", "https://app.example.com/pwa/icon-192.png"},
		{"https://app.example.com", "pwa/a.png", "https://app.example.com/pwa/a.png"},
		{"https://app.example.com", "https://cdn.example.com/a.png", "https://cdn.example.com/a.png"},
		{"https://app.example.com", "//cdn.example.com/a.png", "//cdn.example.com/a.png"},
		{"https://app.example.com", "", ""},
		{"https://app.example.com", "%zz", "%zz"},
		{"", "/pwa/a.png", "/pwa/a.png"},
		{"not a base", "/pwa/a.png", "/pwa/a.png"},
		{"/relative", "/pwa/a.png", "/pwa/a.png"},
		{"https://x.com/app", "/pwa/a.png", "https://x.com/app/pwa/a.png"},
	}
	for _, c := range cases {
		if got := resolveAgainst(c.base, c.in); got != c.want {
			t.Errorf("resolveAgainst(%q,%q)=%q want %q", c.base, c.in, got, c.want)
		}
	}
}

func TestValidDesktopDownloadURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://example.com/dl": true, " http://example.com ": true, "": false, "ftp://x.com": false,
		"https://u:p@x.com/": false, "javascript:alert(1)": false, "https://": false,
		"https://example.com/" + strings.Repeat("a", 200): false,
	} {
		if got := ValidDesktopDownloadURL(in); got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}
