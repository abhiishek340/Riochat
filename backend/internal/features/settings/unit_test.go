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
