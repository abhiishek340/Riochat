package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NativeTool describes a vendor-hosted tool administrators can enable per
// model.
type NativeTool struct {
	// Protocol is the policy key of the protocol the tool belongs to.
	Protocol string
	Provider string
	Type     string
	// Key is "<provider>.<type>" and unique within a protocol.
	Key         string
	Label       string
	Description string
	// Payload is the canonical tool object sent upstream.
	Payload        map[string]any
	DefaultEnabled bool
	Billable       bool
	BillingUnit    string
	PriceNanoUSD   int64
	// PriceLabel is shown when the tool is not billed: notMetered or included.
	PriceLabel   string
	Risk         string // "high" marks code and shell tools
	UsageAliases []string
}

// Price labels for tools that are not billed.
const (
	PriceNotMetered = "notMetered"
	PriceIncluded   = "included"
	RiskHigh        = "high"
)

func nativeEntry(protocol, provider, typ, label, price string, nano int64, unit string, risk bool, payload map[string]any, aliases ...string) NativeTool {
	if payload == nil {
		payload = map[string]any{"type": typ}
	}
	t := NativeTool{
		Protocol: protocol, Provider: label, Type: typ, Key: provider + "." + typ, Label: typ,
		Payload: payload, DefaultEnabled: true, UsageAliases: aliases,
	}
	t.Provider = label
	if nano > 0 {
		t.Billable, t.PriceNanoUSD, t.BillingUnit = true, nano, unit
	} else {
		t.PriceLabel = price
	}
	if risk {
		t.Risk = RiskHigh
	}
	return t
}

// Catalog returns the built-in native tools.
func Catalog() []NativeTool {
	const (
		openAICall = 25_000_000
		anthSearch = 10_000_000
		xaiCall    = 5_000_000
	)
	var out []NativeTool
	add := func(t NativeTool) { out = append(out, t) }
	for _, p := range []string{"openai_chat_completions", "openai_responses"} {
		add(nativeEntry(p, "openai", "web_search", "OpenAI", "", openAICall, "call", false, nil, "web_search_call"))
		add(nativeEntry(p, "openai", "web_search_preview", "OpenAI", "", openAICall, "call", false, nil, "web_search_preview_call"))
	}
	add(nativeEntry("openai_responses", "openai", "shell", "OpenAI", PriceNotMetered, 0, "", true,
		map[string]any{"type": "shell", "environment": map[string]any{"type": "container_auto"}}, "shell_call"))
	add(nativeEntry("openai_responses", "openai", "image_generation", "OpenAI", PriceNotMetered, 0, "", false, nil, "image_generation_call"))
	add(nativeEntry("openai_responses", "openai", "code_interpreter", "OpenAI", PriceNotMetered, 0, "", true,
		map[string]any{"type": "code_interpreter", "container": map[string]any{"type": "auto"}}, "code_interpreter_call"))

	for _, v := range []string{"web_search_20250305", "web_search_20260209"} {
		payload := map[string]any{"type": v, "name": "web_search"}
		if strings.HasSuffix(v, "_20260209") {
			payload["allowed_callers"] = []any{"direct"}
		}
		add(nativeEntry("anthropic_messages", "anthropic", v, "Anthropic", "", anthSearch, "search", false, payload, "web_search"))
	}
	for _, v := range []string{"web_fetch_20250910", "web_fetch_20260209", "tool_search_tool_regex_20251119", "tool_search_tool_bm25_20251119"} {
		payload := map[string]any{"type": v, "name": anthropicNativeNames[v]}
		if strings.HasSuffix(v, "_20260209") {
			payload["allowed_callers"] = []any{"direct"}
		}
		add(nativeEntry("anthropic_messages", "anthropic", v, "Anthropic", PriceIncluded, 0, "", false, payload))
	}
	for _, v := range []string{"code_execution_20250825", "code_execution_20260120"} {
		add(nativeEntry("anthropic_messages", "anthropic", v, "Anthropic", PriceNotMetered, 0, "", true,
			map[string]any{"type": v, "name": "code_execution"}, "code_execution"))
	}
	add(nativeEntry("anthropic_messages", "anthropic", "advisor_20260301", "Anthropic", PriceNotMetered, 0, "", false,
		map[string]any{"type": "advisor_20260301", "name": "advisor"}, "advisor"))

	add(nativeEntry("xai_responses", "xai", "web_search", "xAI", "", xaiCall, "call", false, nil, "web_search_call"))
	add(nativeEntry("xai_responses", "xai", "x_search", "xAI", "", xaiCall, "call", false, nil, "x_search_call"))
	add(nativeEntry("xai_responses", "xai", "code_interpreter", "xAI", "", xaiCall, "call", true, nil, "code_interpreter_call"))

	for _, p := range []string{"gemini_generate_content", "gemini_interactions"} {
		add(nativeEntry(p, "google", "google_search", "Google", PriceNotMetered, 0, "", false, nil))
		add(nativeEntry(p, "google", "code_execution", "Google", PriceNotMetered, 0, "", true, nil))
		add(nativeEntry(p, "google", "url_context", "Google", PriceNotMetered, 0, "", false, nil))
	}
	add(nativeEntry("google_image_generation", "google", "google_search", "Google", PriceNotMetered, 0, "", false, nil))
	return out
}

// ExtraUsagePrices are per-call prices for usage keys providers report without
// a declarable tool.
var ExtraUsagePrices = map[string]int64{
	"xai.attachment_search":  10_000_000,
	"xai.collections_search": 2_500_000,
}

// ToolKeyForUsage maps a provider-reported tool name to the catalog key.
func ToolKeyForUsage(protocol, reported string) string {
	proto, _ := NormalizeProtocol(protocol)
	pk := proto.PolicyKey()
	name := normalizeServerToolName(strings.ToLower(strings.TrimSpace(reported)))
	switch pk {
	case "anthropic_messages":
		if name == "web_search" {
			return "anthropic.web_search_20260209"
		}
		return "anthropic." + name
	case "xai_responses":
		switch name {
		case "attachment_search", "file_attachment_search":
			return "xai.attachment_search"
		case "file_search", "collection_search", "collections_search":
			return "xai.collections_search"
		}
		return "xai." + name
	case "gemini_generate_content", "google_image_generation", "gemini_interactions":
		return "google." + name
	}
	return "openai." + name
}

// NativeToolFromCapabilities parses the administrator-declared nativeTools[]
// of a model's capabilities JSON. Entries with enabled=false, no key, no
// protocol or no type are ignored; provider defaults to "Custom".
func NativeToolFromCapabilities(raw string) []NativeTool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var doc struct {
		NativeTools []map[string]any `json:"nativeTools"`
	}
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return nil
	}
	var out []NativeTool
	for _, e := range doc.NativeTools {
		if en, ok := e["enabled"].(bool); ok && !en {
			continue
		}
		key := firstNonEmpty(strOf(e["key"]), strOf(e["toolKey"]))
		typ := strOf(e["type"])
		var protocols []string
		if p := strOf(e["protocol"]); p != "" {
			protocols = append(protocols, p)
		}
		for _, p := range asList(e["protocols"]) {
			if s := strOf(p); s != "" {
				protocols = append(protocols, s)
			}
		}
		if key == "" || typ == "" || len(protocols) == 0 {
			continue
		}
		t := NativeTool{
			Key: key, Type: typ, Provider: firstNonEmpty(strOf(e["provider"]), "Custom"),
			Label: firstNonEmpty(strOf(e["label"]), key), Description: strOf(e["description"]),
			DefaultEnabled: true, BillingUnit: "call", PriceLabel: strOf(e["priceLabel"]), Risk: strOf(e["riskLevel"]),
		}
		if b, ok := e["defaultEnabled"].(bool); ok {
			t.DefaultEnabled = b
		}
		if b, ok := e["billable"].(bool); ok {
			t.Billable = b
		}
		if u := strOf(e["billingUnit"]); u != "" {
			t.BillingUnit = u
		}
		if n, ok := toInt(e["priceNanousd"]); ok {
			t.PriceNanoUSD = n
		}
		for _, a := range asList(e["usageAliases"]) {
			if s := strOf(a); s != "" {
				t.UsageAliases = append(t.UsageAliases, s)
			}
		}
		t.Payload = map[string]any{"type": typ}
		if p, ok := e["payload"].(map[string]any); ok {
			for k, v := range p {
				t.Payload[k] = cloneJSON(v)
			}
			if _, ok := t.Payload["type"]; !ok {
				t.Payload["type"] = typ
			}
		}
		for _, p := range protocols {
			c := t
			c.Protocol = PolicyKeyFor(p)
			out = append(out, c)
		}
	}
	return out
}
