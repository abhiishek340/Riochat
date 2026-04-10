package settings

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// NativeTool describes a vendor-hosted tool that administrators may enable for
// a model.
type NativeTool struct {
	Protocol       string         `json:"protocol"`
	Provider       string         `json:"provider"`
	Type           string         `json:"type"`
	ToolKey        string         `json:"toolKey"`
	Label          string         `json:"label"`
	Description    string         `json:"description"`
	Payload        map[string]any `json:"payload"`
	DefaultEnabled bool           `json:"defaultEnabled"`
	Billable       bool           `json:"billable"`
	BillingUnit    string         `json:"billingUnit"`
	PriceNanoUSD   int64          `json:"priceNanousd"`
	PriceLabel     string         `json:"priceLabel"`
	RiskLevel      string         `json:"riskLevel"`
	UsageAliases   []string       `json:"usageAliases"`
}

const (
	priceNotMetered = "notMetered"
	priceIncluded   = "included"
)

type toolSpec struct {
	protocol, provider, typ, label, desc string
	payload                              map[string]any
	price                                int64
	unit                                 string
	priceLabel                           string
	high                                 bool
	aliases                              []string
}

func typed(t string) map[string]any { return map[string]any{"type": t} }

// builtinNativeTools builds the static catalog.
func builtinNativeTools() []NativeTool {
	const (
		search25  = 25_000_000
		search10  = 10_000_000
		search5   = 5_000_000
		perCall   = "call"
		perSearch = "search"
	)
	specs := []toolSpec{
		{"openai_chat_completions", "OpenAI", "web_search", "Web search", "OpenAI hosted web search.", typed("web_search"), search25, perCall, "", false, []string{"web_search", "web_search_call"}},
		{"openai_chat_completions", "OpenAI", "web_search_preview", "Web search (preview)", "OpenAI hosted web search, preview version.", typed("web_search_preview"), search25, perCall, "", false, []string{"web_search_preview"}},
		{"openai_responses", "OpenAI", "web_search", "Web search", "OpenAI hosted web search.", typed("web_search"), search25, perCall, "", false, []string{"web_search", "web_search_call"}},
		{"openai_responses", "OpenAI", "web_search_preview", "Web search (preview)", "OpenAI hosted web search, preview version.", typed("web_search_preview"), search25, perCall, "", false, []string{"web_search_preview"}},
		{"openai_responses", "OpenAI", "shell", "Shell", "Run shell commands in a hosted container.", map[string]any{"type": "shell", "environment": typed("container_auto")}, 0, perCall, priceNotMetered, true, nil},
		{"openai_responses", "OpenAI", "image_generation", "Image generation", "Generate images inside the conversation.", typed("image_generation"), 0, perCall, priceNotMetered, false, nil},
		{"openai_responses", "OpenAI", "code_interpreter", "Code interpreter", "Run code in a hosted sandbox.", map[string]any{"type": "code_interpreter", "container": typed("auto")}, 0, perCall, priceNotMetered, true, nil},
		{"anthropic_messages", "Anthropic", "web_search_20250305", "Web search", "Anthropic hosted web search.", typed("web_search_20250305"), search10, perSearch, "", false, []string{"web_search"}},
		{"anthropic_messages", "Anthropic", "web_search_20260209", "Web search", "Anthropic hosted web search, newer version.", map[string]any{"type": "web_search_20260209", "allowed_callers": []any{"direct"}}, search10, perSearch, "", false, []string{"web_search"}},
		{"anthropic_messages", "Anthropic", "web_fetch_20250910", "Web fetch", "Fetch the content of a web page.", typed("web_fetch_20250910"), 0, perCall, priceIncluded, false, []string{"web_fetch"}},
		{"anthropic_messages", "Anthropic", "web_fetch_20260209", "Web fetch", "Fetch the content of a web page, newer version.", typed("web_fetch_20260209"), 0, perCall, priceIncluded, false, []string{"web_fetch"}},
		{"anthropic_messages", "Anthropic", "tool_search_tool_regex_20251119", "Tool search (regex)", "Find tools by regular expression.", typed("tool_search_tool_regex_20251119"), 0, perCall, priceIncluded, false, nil},
		{"anthropic_messages", "Anthropic", "tool_search_tool_bm25_20251119", "Tool search (BM25)", "Find tools by relevance ranking.", typed("tool_search_tool_bm25_20251119"), 0, perCall, priceIncluded, false, nil},
		{"anthropic_messages", "Anthropic", "code_execution_20250825", "Code execution", "Run code in a hosted sandbox.", typed("code_execution_20250825"), 0, perCall, priceNotMetered, true, []string{"code_execution"}},
		{"anthropic_messages", "Anthropic", "code_execution_20260120", "Code execution", "Run code in a hosted sandbox, newer version.", typed("code_execution_20260120"), 0, perCall, priceNotMetered, true, []string{"code_execution"}},
		{"anthropic_messages", "Anthropic", "advisor_20260301", "Advisor", "Consult a stronger model for guidance.", typed("advisor_20260301"), 0, perCall, priceNotMetered, false, nil},
		{"xai_responses", "xAI", "web_search", "Web search", "xAI hosted web search.", typed("web_search"), search5, perCall, "", false, []string{"web_search"}},
		{"xai_responses", "xAI", "x_search", "X search", "Search posts on X.", typed("x_search"), search5, perCall, "", false, []string{"x_search"}},
		{"xai_responses", "xAI", "code_interpreter", "Code interpreter", "Run code in a hosted sandbox.", typed("code_interpreter"), search5, perCall, "", true, []string{"code_interpreter", "code_execution"}},
		{"gemini_generate_content", "Google", "google_search", "Google Search", "Ground answers with Google Search.", map[string]any{"google_search": map[string]any{}}, 0, perCall, priceNotMetered, false, []string{"google_search"}},
		{"gemini_generate_content", "Google", "code_execution", "Code execution", "Run code in a hosted sandbox.", map[string]any{"code_execution": map[string]any{}}, 0, perCall, priceNotMetered, true, []string{"code_execution"}},
		{"gemini_generate_content", "Google", "url_context", "URL context", "Read the content of given URLs.", map[string]any{"url_context": map[string]any{}}, 0, perCall, priceNotMetered, false, []string{"url_context"}},
		{"google_image_generation", "Google", "google_search", "Google Search", "Ground image generation with Google Search.", map[string]any{"google_search": map[string]any{}}, 0, perCall, priceNotMetered, false, []string{"google_search"}},
		{"gemini_interactions", "Google", "google_search", "Google Search", "Ground answers with Google Search.", typed("google_search"), 0, perCall, priceNotMetered, false, []string{"google_search"}},
		{"gemini_interactions", "Google", "code_execution", "Code execution", "Run code in a hosted sandbox.", typed("code_execution"), 0, perCall, priceNotMetered, true, []string{"code_execution"}},
		{"gemini_interactions", "Google", "url_context", "URL context", "Read the content of given URLs.", typed("url_context"), 0, perCall, priceNotMetered, false, []string{"url_context"}},
	}
	out := make([]NativeTool, 0, len(specs))
	for _, s := range specs {
		risk := "low"
		if s.high {
			risk = "high"
		}
		out = append(out, NativeTool{
			Protocol: s.protocol, Provider: s.provider, Type: s.typ,
			ToolKey: strings.ToLower(s.provider) + "." + s.typ,
			Label:   s.label, Description: s.desc, Payload: s.payload,
			DefaultEnabled: true, Billable: s.price > 0, BillingUnit: s.unit,
			PriceNanoUSD: s.price, PriceLabel: s.priceLabel, RiskLevel: risk,
			UsageAliases: append([]string{}, s.aliases...),
		})
	}
	return out
}

// pricingEntry is one tool in a native-tool pricing document.
type pricingEntry struct {
	PriceNanoUSD int64  `json:"priceNanousd"`
	Unit         string `json:"unit"`
	PriceLabel   string `json:"priceLabel"`
	Billable     bool   `json:"billable"`
}

// usageOnlyPrices are catalog keys that exist only as usage prices reported by
// providers.
var usageOnlyPrices = map[string]int64{
	"xai.attachment_search":  10_000_000,
	"xai.collections_search": 2_500_000,
}

// pricingDefaults returns the default price of every catalog key.
func pricingDefaults() map[string]pricingEntry {
	out := map[string]pricingEntry{}
	for _, t := range builtinNativeTools() {
		if _, dup := out[t.ToolKey]; dup {
			continue
		}
		out[t.ToolKey] = pricingEntry{PriceNanoUSD: t.PriceNanoUSD, Unit: t.BillingUnit, Billable: t.Billable}
	}
	return out
}

// acceptedPricingKeys is every key a pricing document may name: the catalog
// plus the usage-only keys, which are priced but never listed in the default
// document.
func acceptedPricingKeys() map[string]pricingEntry {
	out := pricingDefaults()
	for k, p := range usageOnlyPrices {
		out[k] = pricingEntry{PriceNanoUSD: p, Unit: "call", Billable: true}
	}
	return out
}

func prettyJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimSpace(buf.String())
}

var defaultNativeToolPricingJSON = prettyJSON(pricingDefaults())

// nativeToolPricingCheck validates a pricing override document: an object
// keyed by catalog tool key, each value an object with a non-negative price.
func nativeToolPricingCheck() check {
	known := acceptedPricingKeys()
	return func(v string) *violation {
		if v == "" {
			return bad("required", "")
		}
		if len([]rune(v)) > 20000 {
			return bad("max_length", "20000")
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(v), &doc); err != nil || doc == nil {
			return bad("native_tool_pricing", "")
		}
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := known[k]; !ok {
				return bad("native_tool_pricing", "")
			}
			var e struct {
				PriceNanoUSD *int64  `json:"priceNanousd"`
				Unit         *string `json:"unit"`
				PriceLabel   *string `json:"priceLabel"`
				Billable     *bool   `json:"billable"`
			}
			if err := json.Unmarshal(doc[k], &e); err != nil {
				return bad("native_tool_pricing", "")
			}
			if e.PriceNanoUSD != nil && *e.PriceNanoUSD < 0 {
				return bad("native_tool_pricing", "")
			}
		}
		return nil
	}
}
