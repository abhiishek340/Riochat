package channel

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Reasoning formats.
const (
	fmtOpenAI          = "openai"
	fmtAnthropicEffort = "anthropic_effort"
	fmtAnthropicBudget = "anthropic_budget"
	fmtGeminiLevel     = "gemini_level"
	fmtGeminiBudget    = "gemini_budget"
	fmtQwen            = "qwen"
	fmtToggle          = "toggle"
)

var reasoningFormats = []string{fmtOpenAI, fmtAnthropicEffort, fmtAnthropicBudget, fmtGeminiLevel, fmtGeminiBudget, fmtQwen, fmtToggle}

// Reasoning capability sources.
const (
	srcExplicit = "explicit"
	srcInferred = "inferred"
	srcCatalog  = "catalog"
)

// canonicalLevels run from lowest to highest.
var canonicalLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

func levelRank(l string) int {
	for i, v := range canonicalLevels {
		if v == l {
			return i
		}
	}
	return -1
}

func isCanonicalLevel(l string) bool { return levelRank(l) >= 0 }

func sortLevels(levels []string) {
	sort.SliceStable(levels, func(i, j int) bool { return levelRank(levels[i]) < levelRank(levels[j]) })
}

// ReasoningCapability describes the reasoning-effort capability of a model on
// a protocol.
type ReasoningCapability struct {
	Format      string         `json:"format"`
	Levels      []string       `json:"levels"`
	Default     string         `json:"default"`
	Budgets     map[string]int `json:"budgets,omitempty"`
	Source      string         `json:"source"`
	ControlPath string         `json:"controlPath,omitempty"`
	Locked      bool           `json:"locked"`
}

func (c *ReasoningCapability) clone() *ReasoningCapability {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Levels = append([]string(nil), c.Levels...)
	if c.Budgets != nil {
		cp.Budgets = map[string]int{}
		for k, v := range c.Budgets {
			cp.Budgets[k] = v
		}
	}
	return &cp
}

// protocolKey normalizes a route protocol (legacy aliases included) into the
// key used by the reasoning rules.
func protocolKey(p string) string {
	switch lower(p) {
	case "openai":
		return ProtoOpenAIResponses
	case "openrouter":
		return ProtoOpenRouterResponses
	case "anthropic", "claude":
		return ProtoAnthropicMessages
	case "xai", "grok":
		return ProtoXAIResponses
	case "google", "gemini", ProtoGoogleGenerate:
		return "gemini_generate_content"
	}
	return lower(p)
}

func protocolKeys(protocols []string) []string {
	var out []string
	for _, p := range protocols {
		k := protocolKey(p)
		if k != "" && !containsString(out, k) {
			out = append(out, k)
		}
	}
	return out
}

var formatProtocols = map[string][]string{
	fmtOpenAI:          {ProtoOpenAIChat, ProtoOpenRouterChat, ProtoOpenAIResponses, ProtoXAIResponses, ProtoOpenRouterResponses},
	fmtAnthropicEffort: {ProtoAnthropicMessages}, fmtAnthropicBudget: {ProtoAnthropicMessages},
	fmtGeminiLevel:  {"gemini_generate_content", ProtoGeminiInteractions},
	fmtGeminiBudget: {"gemini_generate_content"},
	fmtQwen:         {ProtoOpenAIChat, ProtoOpenRouterChat},
	fmtToggle:       {ProtoOpenAIChat, ProtoOpenRouterChat},
}

func formatSupportsProtocol(format, pkey string) bool {
	return containsString(formatProtocols[format], pkey)
}

func isChatCompletionsKey(k string) bool { return k == ProtoOpenAIChat || k == ProtoOpenRouterChat }

func isBudgetFormat(f string) bool { return f == fmtAnthropicBudget || f == fmtGeminiBudget }

// allowedLevels lists the levels a format may declare.
func allowedLevels(format string) []string {
	var out []string
	for _, l := range canonicalLevels {
		switch format {
		case fmtOpenAI:
		case fmtAnthropicEffort:
			if l == "minimal" || l == "ultra" {
				continue
			}
		case fmtGeminiLevel:
			if l != "minimal" && l != "low" && l != "medium" && l != "high" {
				continue
			}
		default:
			if l == "ultra" {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

func validReasoningFormat(f string) bool { return containsString(reasoningFormats, f) }

// validate checks a capability the way an explicit declaration is checked.
func (c *ReasoningCapability) validate() error {
	if c == nil || !validReasoningFormat(c.Format) {
		return ErrReasoningFormat
	}
	if len(c.Levels) == 0 {
		return ErrReasoningLevels
	}
	allowed := allowedLevels(c.Format)
	seen := map[string]bool{}
	for _, l := range c.Levels {
		if !containsString(allowed, l) || seen[l] {
			return ErrReasoningLevels
		}
		seen[l] = true
	}
	if c.Format == fmtToggle && (len(c.Levels) != 2 || levelRank(c.Levels[0]) > levelRank(c.Levels[1]) || !seen["none"] || c.Levels[0] != "none") {
		return ErrReasoningLevels
	}
	if !seen[c.Default] {
		return ErrReasoningDefault
	}
	switch c.Format {
	case fmtAnthropicBudget, fmtGeminiBudget:
		for _, l := range c.Levels {
			if l == "none" {
				continue
			}
			if _, ok := c.Budgets[l]; !ok {
				return ErrReasoningBudgets
			}
		}
	}
	if c.Format == fmtAnthropicBudget || c.Format == fmtGeminiBudget || c.Format == fmtQwen {
		for l, b := range c.Budgets {
			if !isCanonicalLevel(l) || b < 0 || b > math.MaxInt32 {
				return ErrReasoningBudgets
			}
			if c.Format == fmtAnthropicBudget && l != "none" && b < 1024 {
				return ErrReasoningBudgets
			}
		}
	}
	return nil
}

// parseExplicitReasoning decodes and validates the `reasoning` declaration of a
// capabilities document.
func parseExplicitReasoning(v any) (*ReasoningCapability, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, ErrReasoningFormat
	}
	format, _ := obj["format"].(string)
	format = lower(format)
	if !validReasoningFormat(format) {
		return nil, ErrReasoningFormat
	}
	c := &ReasoningCapability{Format: format, Source: srcExplicit}
	arr, ok := obj["levels"].([]any)
	if !ok || len(arr) == 0 {
		return nil, ErrReasoningLevels
	}
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, ErrReasoningLevels
		}
		c.Levels = append(c.Levels, lower(s))
	}
	sortLevels(c.Levels)
	def, _ := obj["default"].(string)
	c.Default = lower(def)
	if rawB, has := obj["budgets"]; has && rawB != nil {
		bm, ok := rawB.(map[string]any)
		if !ok {
			if c.Format == fmtAnthropicBudget || c.Format == fmtGeminiBudget || c.Format == fmtQwen {
				return nil, ErrReasoningBudgets
			}
		} else if c.Format == fmtAnthropicBudget || c.Format == fmtGeminiBudget || c.Format == fmtQwen {
			c.Budgets = map[string]int{}
			for k, val := range bm {
				n, okNum := budgetInt(val)
				if !okNum {
					return nil, ErrReasoningBudgets
				}
				c.Budgets[lower(k)] = n
			}
		}
	}
	// Validation order: levels, default, budgets.
	probe := c.clone()
	if err := probe.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func budgetInt(v any) (int, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		if err != nil || n < 0 || n > math.MaxInt32 {
			return 0, false
		}
		return int(n), true
	case float64:
		if t != math.Trunc(t) || t < 0 || t > math.MaxInt32 {
			return 0, false
		}
		return int(t), true
	case int:
		return t, t >= 0
	}
	return 0, false
}

// validateReasoningDeclaration is the save-time check of the `reasoning` key.
func validateReasoningDeclaration(caps capsMap) error {
	v, ok := caps["reasoning"]
	if !ok {
		return nil
	}
	_, err := parseExplicitReasoning(v)
	return err
}

// levelPath is the request path that carries the effort level on a protocol.
func levelPath(pkey string) string {
	switch pkey {
	case ProtoOpenAIChat, ProtoOpenRouterChat:
		return "reasoning_effort"
	case ProtoOpenAIResponses, ProtoOpenRouterResponses, ProtoXAIResponses:
		return "reasoning.effort"
	case ProtoAnthropicMessages:
		return "output_config.effort"
	case "gemini_generate_content":
		return "generationConfig.thinkingConfig.thinkingLevel"
	case ProtoGeminiInteractions:
		return "generation_config.thinking_level"
	}
	return ""
}

func levelFormatForProtocol(pkey string) string {
	switch pkey {
	case ProtoOpenAIChat, ProtoOpenRouterChat, ProtoOpenAIResponses, ProtoOpenRouterResponses, ProtoXAIResponses:
		return fmtOpenAI
	case ProtoAnthropicMessages:
		return fmtAnthropicEffort
	case "gemini_generate_content", ProtoGeminiInteractions:
		return fmtGeminiLevel
	}
	return ""
}

// nativePaths lists the request paths a format's translation owns.
func nativePaths(format string) []string {
	switch format {
	case fmtOpenAI:
		return []string{"reasoning_effort", "reasoning.effort"}
	case fmtAnthropicEffort:
		return []string{"output_config.effort"}
	case fmtAnthropicBudget:
		return []string{"thinking.type", "thinking.budget_tokens"}
	case fmtGeminiLevel:
		return []string{"generationConfig.thinkingConfig.thinkingLevel", "generation_config.thinking_config.thinking_level", "generation_config.thinking_level"}
	case fmtGeminiBudget:
		return []string{"generationConfig.thinkingConfig.thinkingBudget", "generation_config.thinking_config.thinking_budget"}
	case fmtQwen:
		return []string{"enable_thinking", "thinking_budget", "budget_tokens"}
	case fmtToggle:
		return []string{"thinking.type", "enable_thinking"}
	}
	return nil
}

// ownedReasoningPaths are all request paths owned by the reasoning translation.
func ownedReasoningPaths() map[string]bool {
	out := map[string]bool{}
	for _, f := range reasoningFormats {
		for _, p := range nativePaths(f) {
			out[p] = true
		}
	}
	for _, k := range []string{ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropicMessages, "gemini_generate_content", ProtoGeminiInteractions} {
		out[levelPath(k)] = true
	}
	return out
}

// getPath reads a dotted path from a decoded JSON object.
func getPath(obj map[string]any, path string) (any, bool) {
	var cur any = obj
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
