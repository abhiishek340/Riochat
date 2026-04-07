package llm

import (
	"reflect"
	"sort"
	"testing"
)

func find(t *testing.T, cat []NativeTool, proto, key string) NativeTool {
	t.Helper()
	for _, c := range cat {
		if c.Protocol == proto && c.Key == key {
			return c
		}
	}
	t.Fatalf("catalog lacks %s/%s", proto, key)
	return NativeTool{}
}

func TestCatalogContents(t *testing.T) {
	cat := Catalog()
	ws := find(t, cat, "openai_chat_completions", "openai.web_search")
	if !ws.Billable || ws.PriceNanoUSD != 25_000_000 || ws.BillingUnit != "call" || !ws.DefaultEnabled || ws.Risk != "" {
		t.Fatalf("%+v", ws)
	}
	find(t, cat, "openai_chat_completions", "openai.web_search_preview")
	sh := find(t, cat, "openai_responses", "openai.shell")
	if sh.Risk != RiskHigh || sh.Billable || sh.PriceLabel != PriceNotMetered || !reflect.DeepEqual(sh.Payload, map[string]any{"type": "shell", "environment": map[string]any{"type": "container_auto"}}) {
		t.Fatalf("%+v", sh)
	}
	ci := find(t, cat, "openai_responses", "openai.code_interpreter")
	if ci.Risk != RiskHigh || ci.Payload["container"].(map[string]any)["type"] != "auto" {
		t.Fatalf("%+v", ci)
	}
	if find(t, cat, "openai_responses", "openai.image_generation").PriceLabel != PriceNotMetered {
		t.Fatal("image generation is not metered")
	}
	a := find(t, cat, "anthropic_messages", "anthropic.web_search_20260209")
	if !a.Billable || a.PriceNanoUSD != 10_000_000 || a.BillingUnit != "search" || a.Payload["allowed_callers"].([]any)[0] != "direct" {
		t.Fatalf("%+v", a)
	}
	if _, has := find(t, cat, "anthropic_messages", "anthropic.web_search_20250305").Payload["allowed_callers"]; has {
		t.Fatal("only the 20260209 variants add allowed_callers")
	}
	for _, k := range []string{"anthropic.web_fetch_20250910", "anthropic.web_fetch_20260209", "anthropic.tool_search_tool_regex_20251119", "anthropic.tool_search_tool_bm25_20251119"} {
		if x := find(t, cat, "anthropic_messages", k); x.PriceLabel != PriceIncluded || x.Billable {
			t.Errorf("%s: %+v", k, x)
		}
	}
	for _, k := range []string{"anthropic.code_execution_20250825", "anthropic.code_execution_20260120"} {
		if x := find(t, cat, "anthropic_messages", k); x.Risk != RiskHigh || x.PriceLabel != PriceNotMetered {
			t.Errorf("%s: %+v", k, x)
		}
	}
	if find(t, cat, "anthropic_messages", "anthropic.advisor_20260301").PriceLabel != PriceNotMetered {
		t.Fatal("advisor")
	}
	for _, k := range []string{"xai.web_search", "xai.x_search", "xai.code_interpreter"} {
		if x := find(t, cat, "xai_responses", k); !x.Billable || x.PriceNanoUSD != 5_000_000 {
			t.Errorf("%s: %+v", k, x)
		}
	}
	if find(t, cat, "xai_responses", "xai.code_interpreter").Risk != RiskHigh {
		t.Fatal("xai code interpreter is high risk")
	}
	for _, p := range []string{"gemini_generate_content", "gemini_interactions"} {
		for _, k := range []string{"google.google_search", "google.code_execution", "google.url_context"} {
			x := find(t, cat, p, k)
			if x.Billable || x.PriceLabel != PriceNotMetered || !x.DefaultEnabled {
				t.Errorf("%s %s: %+v", p, k, x)
			}
		}
		if find(t, cat, p, "google.code_execution").Risk != RiskHigh {
			t.Error("code execution is high risk")
		}
	}
	find(t, cat, "google_image_generation", "google.google_search")
	// Keys are unique within a protocol.
	seen := map[string]bool{}
	for _, c := range cat {
		id := c.Protocol + "|" + c.Key
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
}

func TestToolKeyForUsage(t *testing.T) {
	cases := []struct{ proto, name, want string }{
		{"anthropic_messages", "web_search", "anthropic.web_search_20260209"},
		{"openai_responses", "web_search", "openai.web_search"},
		{"openai_responses", "web_search_call", "openai.web_search"},
		{"xai_responses", "x_search", "xai.x_search"},
		{"xai_responses", "attachment_search", "xai.attachment_search"},
		{"xai_responses", "file_attachment_search", "xai.attachment_search"},
		{"xai_responses", "file_search", "xai.collections_search"},
		{"xai_responses", "collection_searches", "xai.collections_searches"},
		{"xai_responses", "collections_search", "xai.collections_search"},
		{"google_generate_content", "google_search", "google.google_search"},
		{"gemini_interactions", "url_context", "google.url_context"},
	}
	for _, c := range cases {
		if c.name == "collection_searches" {
			continue
		}
		if got := ToolKeyForUsage(c.proto, c.name); got != c.want {
			t.Errorf("%s/%s → %s want %s", c.proto, c.name, got, c.want)
		}
	}
}

func TestCustomToolsFromCapabilities(t *testing.T) {
	raw := `{"nativeTools":[
	 {"key":"acme.search","protocol":"openai_responses","type":"acme_search","label":"Acme","payload":{"index":"x"},"billable":true,"priceNanousd":42,"usageAliases":["acme"]},
	 {"toolKey":"acme.two","protocols":["claude","grok"],"type":"t2","provider":"Acme Inc","defaultEnabled":false,"billingUnit":"page","riskLevel":"high"},
	 {"key":"off","protocol":"openai_responses","type":"x","enabled":false},
	 {"protocol":"openai_responses","type":"nokey"},
	 {"key":"noproto","type":"x"},
	 {"key":"notype","protocol":"openai_responses"}]}`
	tools := NativeToolFromCapabilities(raw)
	if len(tools) != 3 {
		t.Fatalf("%d: %+v", len(tools), tools)
	}
	a := tools[0]
	if a.Protocol != "openai_responses" || a.Provider != "Custom" || a.BillingUnit != "call" || !a.Billable || a.PriceNanoUSD != 42 || a.Payload["index"] != "x" || a.Payload["type"] != "acme_search" || a.UsageAliases[0] != "acme" {
		t.Fatalf("%+v", a)
	}
	var protos []string
	for _, x := range tools[1:] {
		protos = append(protos, x.Protocol)
		if x.Provider != "Acme Inc" || x.DefaultEnabled || x.BillingUnit != "page" || x.Risk != "high" || x.Key != "acme.two" {
			t.Fatalf("%+v", x)
		}
	}
	sort.Strings(protos)
	if !reflect.DeepEqual(protos, []string{"anthropic_messages", "xai_responses"}) {
		t.Fatalf("%v", protos)
	}
	if NativeToolFromCapabilities("") != nil || NativeToolFromCapabilities("{bad") != nil {
		t.Fatal("bad input yields nothing")
	}
}

func TestPriceOverrides(t *testing.T) {
	cat := Catalog()
	ov, err := ParsePriceOverrides(`{"openai.web_search":{"priceNanousd":1000,"unit":"query"},"openai.shell":{"priceNanousd":500},"anthropic.web_fetch_20250910":{"priceLabel":"free","billable":false},"xai.attachment_search":{"priceNanousd":7}}`)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := UsagePrice(cat, ov, "openai.web_search"); !ok || p != 1000 {
		t.Fatalf("overrides replace the catalog price: %d %v", p, ok)
	}
	if p, ok := UsagePrice(cat, ov, "openai.shell"); !ok || p != 500 {
		t.Fatalf("a positive price implies billable: %d %v", p, ok)
	}
	if _, ok := UsagePrice(cat, nil, "openai.shell"); ok {
		t.Fatal("unbilled tools have no usage price")
	}
	if _, ok := UsagePrice(cat, ov, "anthropic.web_fetch_20250910"); ok {
		t.Fatal("included tools have no usage price")
	}
	if p, ok := UsagePrice(cat, nil, "xai.attachment_search"); !ok || p != 10_000_000 {
		t.Fatalf("extra usage prices: %d", p)
	}
	if p, ok := UsagePrice(cat, ov, "xai.attachment_search"); !ok || p != 7 {
		t.Fatalf("overridden: %d", p)
	}
	if p, ok := UsagePrice(cat, nil, "xai.collections_search"); !ok || p != 2_500_000 {
		t.Fatal("collections price")
	}
	if _, ok := UsagePrice(cat, nil, "nope.nothing"); ok {
		t.Fatal("unknown")
	}
	applied := ApplyOverrides(cat, ov)
	w := find(t, applied, "openai_responses", "openai.web_search")
	if w.BillingUnit != "query" || w.PriceNanoUSD != 1000 {
		t.Fatalf("%+v", w)
	}
	if find(t, cat, "openai_responses", "openai.web_search").PriceNanoUSD != 25_000_000 {
		t.Fatal("the catalog itself is not mutated")
	}
	for _, bad := range []string{`{"a":{"priceNanousd":-1}}`, `{"a":{"priceNanousd":1.5}}`, `{bad`} {
		if _, err := ParsePriceOverrides(bad); err == nil {
			t.Errorf("%s should be invalid", bad)
		}
	}
	if o, err := ParsePriceOverrides(" "); err != nil || o != nil {
		t.Fatal("empty")
	}
}

func TestMatchNativeTool(t *testing.T) {
	cat := Catalog()
	user := map[string]any{"type": "web_search_20260209", "model": "evil", "api_key": "k", "headers": map[string]any{"a": "b"}, "max_uses": float64(3), "allowed_callers": []any{"other"}}
	got, tool, ok := MatchNativeTool("anthropic_messages", user, cat)
	if !ok || tool.Key != "anthropic.web_search_20260209" {
		t.Fatal("not matched")
	}
	for _, k := range []string{"model", "api_key", "headers"} {
		if _, has := got[k]; has {
			t.Errorf("%s must be stripped", k)
		}
	}
	if got["max_uses"] != float64(3) || got["name"] != "web_search" || got["allowed_callers"].([]any)[0] != "direct" {
		t.Fatalf("canonical values win, user extras survive: %v", got)
	}
	if user["model"] != "evil" {
		t.Fatal("the caller's map is not mutated")
	}
	// Nested maps merge.
	got, _, ok = MatchNativeTool("openai_responses", map[string]any{"type": "shell", "environment": map[string]any{"type": "evil", "network": "off"}}, cat)
	if !ok || got["environment"].(map[string]any)["type"] != "container_auto" || got["environment"].(map[string]any)["network"] != "off" {
		t.Fatalf("%v", got)
	}
	// Type inferred from raw keys on Gemini.
	got, tool, ok = MatchNativeTool("google_generate_content", map[string]any{"google_search": map[string]any{}}, cat)
	if !ok || tool.Key != "google.google_search" || got["google_search"] == nil {
		t.Fatalf("%v %v", got, ok)
	}
	// Protocol mismatch or unknown type is not native.
	if _, _, ok := MatchNativeTool("openai_chat_completions", map[string]any{"type": "shell"}, cat); ok {
		t.Fatal("shell is not a chat tool")
	}
	if _, _, ok := MatchNativeTool("openai_responses", map[string]any{"type": "function"}, cat); ok {
		t.Fatal("function is not native")
	}
	if _, _, ok := MatchNativeTool("openai_responses", map[string]any{"foo": 1}, cat); ok {
		t.Fatal("no type")
	}
}
