package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// nativeToolEntry is one provider-hosted tool in the pricing catalog.
type nativeToolEntry struct {
	Provider     string
	Key          string
	Label        string
	Description  string
	Type         string
	DefaultPrice int64
	Unit         string
	// Names are the lower-case tool names the upstream reports for this entry.
	Names []string
}

// nativeToolCatalog lists every known provider-hosted tool. Keys have the form
// "<provider lower-case>.<tool type>" (for example "openai.web_search"), the
// same keys the settings registry uses for native_tool_pricing_json, and are
// unique across providers so the override object can be keyed by tool key
// alone.
var nativeToolCatalog = []nativeToolEntry{
	{"OpenAI", "openai.web_search", "Web search", "OpenAI hosted web search.", "web_search", 25_000_000, "call", []string{"web_search", "web_search_call"}},
	{"OpenAI", "openai.web_search_preview", "Web search (preview)", "OpenAI hosted web search, preview version.", "web_search_preview", 25_000_000, "call", []string{"web_search_preview"}},
	{"OpenAI", "openai.shell", "Shell", "Run shell commands in a hosted container.", "shell", 0, "call", []string{"shell"}},
	{"OpenAI", "openai.image_generation", "Image generation", "Generate images inside the conversation.", "image_generation", 0, "call", []string{"image_generation"}},
	{"OpenAI", "openai.code_interpreter", "Code interpreter", "Run code in a hosted sandbox.", "code_interpreter", 0, "call", []string{"code_interpreter"}},

	{"Anthropic", "anthropic.web_search_20250305", "Web search", "Anthropic hosted web search.", "web_search_20250305", 10_000_000, "search", []string{"web_search_20250305"}},
	{"Anthropic", "anthropic.web_search_20260209", "Web search", "Anthropic hosted web search, newer version.", "web_search_20260209", 10_000_000, "search",
		[]string{"web_search", "web_search_20260209", "web_search_requests"}},
	{"Anthropic", "anthropic.web_fetch_20250910", "Web fetch", "Fetch the content of a web page.", "web_fetch_20250910", 0, "call", []string{"web_fetch_20250910"}},
	{"Anthropic", "anthropic.web_fetch_20260209", "Web fetch", "Fetch the content of a web page, newer version.", "web_fetch_20260209", 0, "call", []string{"web_fetch", "web_fetch_20260209", "web_fetch_requests"}},
	{"Anthropic", "anthropic.tool_search_tool_regex_20251119", "Tool search (regex)", "Find tools by regular expression.", "tool_search_tool_regex_20251119", 0, "call", []string{"tool_search", "tool_search_tool_regex"}},
	{"Anthropic", "anthropic.tool_search_tool_bm25_20251119", "Tool search (BM25)", "Find tools by relevance ranking.", "tool_search_tool_bm25_20251119", 0, "call", []string{"tool_search_tool_bm25"}},
	{"Anthropic", "anthropic.code_execution_20250825", "Code execution", "Run code in a hosted sandbox.", "code_execution_20250825", 0, "call", []string{"code_execution_20250522", "code_execution_20250825"}},
	{"Anthropic", "anthropic.code_execution_20260120", "Code execution", "Run code in a hosted sandbox, newer version.", "code_execution_20260120", 0, "call", []string{"code_execution", "code_execution_20260120"}},
	{"Anthropic", "anthropic.advisor_20260301", "Advisor", "Consult a stronger model for guidance.", "advisor_20260301", 0, "call", []string{"advisor"}},

	{"xAI", "xai.web_search", "Web search", "xAI hosted web search.", "web_search", 5_000_000, "call", []string{"web_search"}},
	{"xAI", "xai.x_search", "X search", "Search posts on X.", "x_search", 5_000_000, "call", []string{"x_search"}},
	{"xAI", "xai.code_interpreter", "Code interpreter", "Run code in a hosted sandbox.", "code_interpreter", 5_000_000, "call", []string{"code_interpreter", "code_execution"}},
	{"xAI", "xai.attachment_search", "Attachment search", "xAI hosted attachment search.", "attachment_search", 10_000_000, "call", []string{"attachment_search"}},
	{"xAI", "xai.collections_search", "Collections search", "xAI hosted collections search.", "collections_search", 2_500_000, "call", []string{"collections_search", "file_search"}},

	{"Google", "google.google_search", "Google Search", "Ground answers with Google Search.", "google_search", 0, "call", []string{"google_search", "web_search"}},
	{"Google", "google.code_execution", "Code execution", "Run code in a hosted sandbox.", "code_execution", 0, "call", []string{"code_execution"}},
	{"Google", "google.url_context", "URL context", "Read the content of given URLs.", "url_context", 0, "call", []string{"url_context"}},
}

// nativeUsageOnly are catalog keys that are priced when a provider reports
// their usage but are not part of the listed pricing table.
var nativeUsageOnly = map[string]bool{"xai.attachment_search": true, "xai.collections_search": true}

func nativeEntryByKey(key string) (nativeToolEntry, bool) {
	for _, e := range nativeToolCatalog {
		if e.Key == key {
			return e, true
		}
	}
	return nativeToolEntry{}, false
}

// nativeProviderOf maps an upstream protocol identifier to the catalog
// provider that hosts tools for it ("" when none).
func nativeProviderOf(protocol string) string {
	p := strings.ToLower(strings.TrimSpace(protocol))
	switch {
	case p == "anthropic_messages" || strings.HasPrefix(p, "anthropic"):
		return "Anthropic"
	case strings.HasPrefix(p, "openai"):
		return "OpenAI"
	case strings.HasPrefix(p, "xai") || strings.HasPrefix(p, "grok"):
		return "xAI"
	case strings.HasPrefix(p, "gemini") || strings.HasPrefix(p, "google") || strings.HasPrefix(p, "vertex"):
		return "Google"
	}
	return ""
}

// nativeEntryFor resolves a reported tool name under a protocol.
func nativeEntryFor(protocol, tool string) (nativeToolEntry, bool) {
	prov := nativeProviderOf(protocol)
	if prov == "" {
		return nativeToolEntry{}, false
	}
	name := strings.ToLower(strings.TrimSpace(tool))
	for _, e := range nativeToolCatalog {
		if e.Provider != prov {
			continue
		}
		if strings.ToLower(e.Key) == name || strings.ToLower(e.Type) == name {
			return e, true
		}
		for _, n := range e.Names {
			if n == name {
				return e, true
			}
		}
	}
	return nativeToolEntry{}, false
}

// NativeToolOverride is one override entry as stored in
// native_tool_pricing_json.
type NativeToolOverride struct {
	PriceNanousd int64  `json:"priceNanousd"`
	Unit         string `json:"unit"`
	PriceLabel   string `json:"priceLabel"`
	Billable     bool   `json:"billable"`
}

// NativeToolInput is one entry of the PATCH body.
type NativeToolInput struct {
	ToolKey      string `json:"toolKey" binding:"required,max=128"`
	PriceNanousd int64  `json:"priceNanousd"`
	Unit         string `json:"unit" binding:"max=32"`
	PriceLabel   string `json:"priceLabel" binding:"max=128"`
	Billable     bool   `json:"billable"`
}

// NativeToolPricingItem is the public view of one catalog entry.
type NativeToolPricingItem struct {
	Provider     string `json:"provider"`
	ToolKey      string `json:"toolKey"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	PriceNanousd int64  `json:"priceNanousd"`
	Unit         string `json:"unit"`
	PriceLabel   string `json:"priceLabel"`
	Billable     bool   `json:"billable"`
}

// normalizeNativeOverrides validates an override object and rewrites it to its
// canonical shape: units come from the catalog, labels are cleared and the
// billable flag follows the price.
func normalizeNativeOverrides(in map[string]NativeToolOverride) (map[string]NativeToolOverride, error) {
	out := make(map[string]NativeToolOverride, len(in))
	for key, ov := range in {
		entry, ok := nativeEntryByKey(key)
		if !ok {
			return nil, fmt.Errorf("unknown native tool %q", key)
		}
		if ov.PriceNanousd < 0 {
			return nil, fmt.Errorf("negative price for %q", key)
		}
		unit := entry.Unit
		if unit == "" {
			unit = "call"
		}
		out[key] = NativeToolOverride{PriceNanousd: ov.PriceNanousd, Unit: unit, PriceLabel: "", Billable: ov.PriceNanousd > 0}
	}
	return out, nil
}

// normalizeNativeInputs converts the PATCH body list to a canonical override
// object. A repeated tool key keeps the last entry.
func normalizeNativeInputs(items []NativeToolInput) (map[string]NativeToolOverride, error) {
	raw := make(map[string]NativeToolOverride, len(items))
	for _, it := range items {
		raw[strings.TrimSpace(it.ToolKey)] = NativeToolOverride{PriceNanousd: it.PriceNanousd, Unit: it.Unit, PriceLabel: it.PriceLabel, Billable: it.Billable}
	}
	return normalizeNativeOverrides(raw)
}

// defaultNativeOverrides is the table of catalog defaults in override shape.
func defaultNativeOverrides() map[string]NativeToolOverride {
	out := make(map[string]NativeToolOverride, len(nativeToolCatalog))
	for _, e := range nativeToolCatalog {
		if nativeUsageOnly[e.Key] {
			continue
		}
		unit := e.Unit
		if unit == "" {
			unit = "call"
		}
		out[e.Key] = NativeToolOverride{PriceNanousd: e.DefaultPrice, Unit: unit, Billable: e.DefaultPrice > 0}
	}
	return out
}

// marshalNativeOverrides renders the pretty-printed setting value.
func marshalNativeOverrides(m map[string]NativeToolOverride) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return "{}"
	}
	return strings.TrimSpace(buf.String())
}

// defaultNativePricingJSON is the default value of native_tool_pricing_json.
func defaultNativePricingJSON() string { return marshalNativeOverrides(defaultNativeOverrides()) }

// parseNativeOverrides reads a stored override object. Empty text yields no
// overrides; unknown keys or negative prices are errors.
func parseNativeOverrides(text string) (map[string]NativeToolOverride, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "null" {
		return map[string]NativeToolOverride{}, nil
	}
	var raw map[string]NativeToolOverride
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	return normalizeNativeOverrides(raw)
}

// effectiveNativePrice is the per-call price in force for an entry: the
// override when present (zero disables charging), otherwise the default.
func effectiveNativePrice(e nativeToolEntry, overrides map[string]NativeToolOverride) int64 {
	if ov, ok := overrides[e.Key]; ok {
		if ov.PriceNanousd <= 0 {
			return 0
		}
		return ov.PriceNanousd
	}
	return e.DefaultPrice
}

// nativePricingSource names where the effective table comes from.
func nativePricingSource(overrides map[string]NativeToolOverride) string {
	for _, e := range nativeToolCatalog {
		if effectiveNativePrice(e, overrides) != e.DefaultPrice {
			return "admin_configured"
		}
	}
	return "provider_official_defaults"
}

func priceLabelFor(price int64, unit string) string {
	if price <= 0 {
		return "not billed"
	}
	if unit == "" {
		unit = "call"
	}
	return fmt.Sprintf("$%s / %s", trimFloat(NanoToUSD(price)), unit)
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.9f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// nativePricingView builds the public table (catalog plus overrides).
func nativePricingView(overrides map[string]NativeToolOverride) []NativeToolPricingItem {
	out := make([]NativeToolPricingItem, 0, len(nativeToolCatalog))
	for _, e := range nativeToolCatalog {
		if nativeUsageOnly[e.Key] {
			continue
		}
		price := effectiveNativePrice(e, overrides)
		unit := e.Unit
		if unit == "" {
			unit = "call"
		}
		out = append(out, NativeToolPricingItem{
			Provider: e.Provider, ToolKey: e.Key, Label: strings.TrimSpace(e.Label), Description: strings.TrimSpace(e.Description),
			Type: e.Type, PriceNanousd: price, Unit: unit, PriceLabel: priceLabelFor(price, unit), Billable: price > 0,
		})
	}
	return out
}
