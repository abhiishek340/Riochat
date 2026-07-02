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

// defaultLevelOf picks the default of a level list: medium when present, else
// the highest level not above medium, else the first.
func defaultLevelOf(levels []string) string {
	if containsString(levels, "medium") {
		return "medium"
	}
	best := ""
	for _, l := range levels {
		if levelRank(l) <= levelRank("medium") && levelRank(l) >= levelRank(best) {
			best = l
		}
	}
	if best != "" {
		return best
	}
	if len(levels) > 0 {
		return levels[0]
	}
	return ""
}

// catalogDefaultLevel is the default of a catalog capability: medium, else the
// highest non-none level not above medium, else the lowest non-none, else first.
func catalogDefaultLevel(levels []string) string {
	if containsString(levels, "medium") {
		return "medium"
	}
	best := ""
	for _, l := range levels {
		if l != "none" && levelRank(l) <= levelRank("medium") && levelRank(l) > levelRank(best) {
			best = l
		}
	}
	if best != "" {
		return best
	}
	for _, l := range levels {
		if l != "none" {
			return l
		}
	}
	if len(levels) > 0 {
		return levels[0]
	}
	return ""
}

func isQwenEntry(e *catalogEntry, vendor string) bool {
	v := lower(vendor)
	if v == "alibaba" || v == "qwen" {
		return true
	}
	if v != "" && v != "unknown" {
		return false
	}
	if e == nil {
		return false
	}
	if e.CanonicalID != "" {
		c := strings.ToLower(e.CanonicalID)
		return strings.HasPrefix(c, "alibaba/") || strings.HasPrefix(c, "qwen/") || strings.Contains(c, "/qwen")
	}
	return strings.HasPrefix(strings.ToLower(e.Provider), "alibaba") || strings.Contains(strings.ToLower(e.ID), "qwen")
}

type catalogReason string

const (
	reasonOK               catalogReason = ""
	reasonNotReasoning     catalogReason = "not_reasoning"
	reasonUnsupportedProto catalogReason = "unsupported_protocol"
	reasonInvalid          catalogReason = "invalid"
)

var budgetTargets = map[string]int{"none": 0, "minimal": 1024, "low": 2048, "medium": 8192, "high": 24576, "xhigh": 32768, "max": 65536}

func clampBudget(target int, min, max *int, anthropic bool) int {
	if anthropic && target < 1024 {
		target = 1024
	}
	if min != nil && target < *min {
		target = *min
	}
	if max != nil && target > *max {
		target = *max
	}
	if anthropic && target < 1024 {
		target = 1024
	}
	return target
}

// catalogCapability converts a catalog entry into the capability it grants on
// a protocol key.
func catalogCapability(e *catalogEntry, pkey, vendor string) (*ReasoningCapability, catalogReason) {
	if e == nil || !e.hasReasoning() {
		return nil, reasonNotReasoning
	}
	applied := false
	// 1. effort option.
	if eff := e.option(optEffort); eff != nil {
		if format := levelFormatForProtocol(pkey); format != "" {
			var levels []string
			allowed := allowedLevels(format)
			for _, v := range eff.Values {
				v = lower(v)
				if containsString(allowed, v) && !containsString(levels, v) {
					levels = append(levels, v)
				}
			}
			if len(levels) > 0 {
				sortLevels(levels)
				c := &ReasoningCapability{Format: format, Levels: levels, Default: catalogDefaultLevel(levels), Source: srcCatalog}
				if c.validate() != nil {
					return nil, reasonInvalid
				}
				return c, reasonOK
			}
		}
		applied = true
	}
	// 2. budget option.
	if b := e.option(optBudget); b != nil {
		format := ""
		switch {
		case pkey == ProtoAnthropicMessages:
			format = fmtAnthropicBudget
		case pkey == "gemini_generate_content":
			format = fmtGeminiBudget
		case isChatCompletionsKey(pkey) && isQwenEntry(e, vendor):
			format = fmtQwen
		}
		if format != "" {
			anthropic := format == fmtAnthropicBudget
			type lb struct {
				level  string
				budget int
			}
			var list []lb
			for _, l := range []string{"low", "medium", "high"} {
				list = append(list, lb{l, clampBudget(budgetTargets[l], b.Min, b.Max, anthropic)})
			}
			// When clamping makes adjacent levels equal, the lower one is dropped.
			var kept []lb
			for i, x := range list {
				if i+1 < len(list) && list[i+1].budget == x.budget {
					continue
				}
				kept = append(kept, x)
			}
			c := &ReasoningCapability{Format: format, Source: srcCatalog, Budgets: map[string]int{}}
			offerNone := format == fmtQwen || (format == fmtGeminiBudget && (b.Min == nil || *b.Min == 0))
			if offerNone {
				c.Levels = append(c.Levels, "none")
				c.Budgets["none"] = 0
			}
			for _, x := range kept {
				c.Levels = append(c.Levels, x.level)
				c.Budgets[x.level] = x.budget
			}
			c.Default = catalogDefaultLevel(c.Levels)
			if c.validate() != nil {
				return nil, reasonInvalid
			}
			return c, reasonOK
		}
		applied = true
	}
	// 3. toggle option.
	if e.option(optToggle) != nil && isChatCompletionsKey(pkey) {
		format := fmtToggle
		if isQwenEntry(e, vendor) {
			format = fmtQwen
		}
		c := &ReasoningCapability{Format: format, Levels: []string{"none", "high"}, Default: "high", Source: srcCatalog}
		if c.validate() != nil {
			return nil, reasonInvalid
		}
		return c, reasonOK
	}
	_ = applied
	return nil, reasonUnsupportedProto
}

// catalogCapabilityAcross returns the catalog capability that is valid on the
// most protocol keys (ties to the earliest).
func catalogCapabilityAcross(e *catalogEntry, pkeys []string, vendor string) *ReasoningCapability {
	var best *ReasoningCapability
	bestCount := 0
	for i, pk := range pkeys {
		c, _ := catalogCapability(e, pk, vendor)
		if c == nil {
			continue
		}
		count := 0
		for _, other := range pkeys[i:] {
			if formatSupportsProtocol(c.Format, other) {
				count++
			}
		}
		if count > bestCount {
			best, bestCount = c, count
		}
	}
	return best
}

// ---- native parameter inference ------------------------------------------

// nativeThinking summarizes the thinking-related native parameters found in the
// administrator's default options for one format.
type nativeThinking struct {
	level       string
	hasLevel    bool
	budget      int
	hasBudget   bool
	disabled    bool
	hasDisabled bool
}

func readNative(defaults map[string]any, format string) (nativeThinking, bool) {
	var n nativeThinking
	found := false
	str := func(path string) (string, bool) {
		v, ok := getPath(defaults, path)
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		return lower(s), ok
	}
	num := func(path string) (int, bool) {
		v, ok := getPath(defaults, path)
		if !ok {
			return 0, false
		}
		return budgetInt(v)
	}
	boolean := func(path string) (bool, bool) {
		v, ok := getPath(defaults, path)
		if !ok {
			return false, false
		}
		b, ok := v.(bool)
		return b, ok
	}
	switch format {
	case fmtOpenAI, fmtAnthropicEffort, fmtGeminiLevel:
		for _, p := range nativePaths(format) {
			if s, ok := str(p); ok && isCanonicalLevel(s) {
				n.level, n.hasLevel, found = s, true, true
				break
			}
		}
	case fmtAnthropicBudget:
		if b, ok := num("thinking.budget_tokens"); ok {
			n.budget, n.hasBudget, found = b, true, true
		}
		if s, ok := str("thinking.type"); ok {
			n.disabled, n.hasDisabled, found = s == "disabled", true, true
		}
	case fmtGeminiBudget:
		for _, p := range nativePaths(format) {
			if b, ok := num(p); ok {
				n.budget, n.hasBudget, found = b, true, true
				break
			}
		}
	case fmtQwen:
		if b, ok := boolean("enable_thinking"); ok {
			n.disabled, n.hasDisabled, found = !b, true, true
		}
		for _, p := range []string{"thinking_budget", "budget_tokens"} {
			if b, ok := num(p); ok {
				n.budget, n.hasBudget, found = b, true, true
				break
			}
		}
	case fmtToggle:
		if s, ok := str("thinking.type"); ok {
			n.disabled, n.hasDisabled, found = s == "disabled", true, true
		} else if b, ok := boolean("enable_thinking"); ok {
			n.disabled, n.hasDisabled, found = !b, true, true
		}
	}
	return n, found
}

// nearestLevelByBudget picks the level whose budget is closest to b.
func nearestLevelByBudget(budgets map[string]int, levels []string, b int) string {
	best, bestDiff := "", math.MaxInt
	for _, l := range levels {
		if l == "none" {
			continue
		}
		bud, ok := budgets[l]
		if !ok {
			continue
		}
		d := bud - b
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = l, d
		}
	}
	return best
}

var conservativeLevels = map[string][]string{
	fmtGeminiLevel: {"low", "high"},
	fmtQwen:        {"none", "low", "medium", "high"},
	fmtToggle:      {"none", "high"},
}

// inferFromNative builds the capability implied by native request parameters
// the administrator configured. entry is the catalog entry for the model (may
// be nil) and supplies levels/budgets of the same format.
func inferFromNative(caps capsMap, pkey, vendor string, entry *catalogEntry) *ReasoningCapability {
	defaults := caps.object("defaultOptions")
	controls, _ := caps["optionControls"].([]any)

	// (a) a select control on the protocol's level path.
	if lp := levelPath(pkey); lp != "" {
		format := levelFormatForProtocol(pkey)
		for _, ce := range controls {
			cm, ok := ce.(map[string]any)
			if !ok || lower(strOf(cm["type"])) != "select" || strOf(cm["path"]) != lp {
				continue
			}
			allowed := allowedLevels(format)
			var levels []string
			for _, v := range optionValues(cm["options"]) {
				v = lower(v)
				if containsString(allowed, v) && !containsString(levels, v) {
					levels = append(levels, v)
				}
			}
			if len(levels) == 0 {
				continue
			}
			sortLevels(levels)
			def := ""
			if v, ok := getPath(defaults, lp); ok {
				if s, ok := v.(string); ok && containsString(levels, lower(s)) {
					def = lower(s)
				}
			}
			if def == "" {
				def = defaultLevelOf(levels)
			}
			c := &ReasoningCapability{Format: format, Levels: levels, Default: def, Source: srcInferred, ControlPath: lp}
			if c.validate() == nil {
				return c
			}
		}
	}

	// (b) native thinking parameters in the default options, (c) native
	// thinking controls declared without defaults.
	declaredPaths := map[string]bool{}
	for _, ce := range controls {
		if cm, ok := ce.(map[string]any); ok {
			declaredPaths[strOf(cm["path"])] = true
		}
	}
	for _, format := range reasoningFormats {
		if !formatSupportsProtocol(format, pkey) {
			continue
		}
		native, found := readNative(defaults, format)
		viaControl := false
		if !found {
			for _, p := range nativePaths(format) {
				if declaredPaths[p] {
					viaControl = true
				}
			}
			if !viaControl {
				continue
			}
		}
		c := buildNativeCapability(format, pkey, vendor, entry, native, found)
		if c != nil && c.validate() == nil {
			return c
		}
	}
	return nil
}

func buildNativeCapability(format, pkey, vendor string, entry *catalogEntry, n nativeThinking, found bool) *ReasoningCapability {
	c := &ReasoningCapability{Format: format, Source: srcInferred}
	// Levels from the catalog when it grants the same format, else a
	// conservative set.
	if cc, _ := catalogCapability(entry, pkey, vendor); cc != nil && cc.Format == format {
		c.Levels = append([]string(nil), cc.Levels...)
		if cc.Budgets != nil {
			c.Budgets = map[string]int{}
			for k, v := range cc.Budgets {
				c.Budgets[k] = v
			}
		}
	} else if cons, ok := conservativeLevels[format]; ok {
		c.Levels = append([]string(nil), cons...)
	} else {
		c.Levels = []string{"low", "medium", "high"}
	}
	if isBudgetFormat(format) || format == fmtQwen {
		if c.Budgets == nil {
			c.Budgets = map[string]int{}
		}
		for _, l := range []string{"low", "medium", "high"} {
			if _, ok := c.Budgets[l]; !ok && (isBudgetFormat(format) || containsString(c.Levels, l)) {
				c.Budgets[l] = budgetTargets[l]
			}
		}
	}
	// The administrator's own level is added when missing.
	level := ""
	if found {
		switch {
		case n.hasLevel:
			level = n.level
		case n.hasDisabled && n.disabled:
			level = "none"
		case n.hasBudget:
			if n.budget == 0 {
				level = "none"
			} else {
				for _, l := range []string{"low", "medium", "high"} {
					if _, ok := c.Budgets[l]; !ok {
						c.Budgets[l] = budgetTargets[l]
					}
				}
				level = nearestLevelByBudget(c.Budgets, c.Levels, n.budget)
				if level == "" {
					level = "medium"
				}
			}
		case n.hasDisabled:
			level = "high"
		}
	}
	allowed := allowedLevels(format)
	if level != "" && containsString(allowed, level) && !containsString(c.Levels, level) {
		c.Levels = append(c.Levels, level)
	}
	if format == fmtToggle && !containsString(c.Levels, "high") {
		c.Levels = []string{"none", "high"}
	}
	if isBudgetFormat(format) {
		// Budget formats cannot offer "none" with a budget requirement.
		if n.hasBudget && level != "" && level != "none" {
			if _, ok := c.Budgets[level]; !ok {
				c.Budgets[level] = n.budget
			}
		}
	}
	if level == "" || !containsString(c.Levels, level) {
		level = defaultLevelOf(c.Levels)
	}
	sortLevels(c.Levels)
	c.Default = level
	return c
}

func strOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// optionValues extracts option values from an options array of strings or
// {value: ...} objects.
func optionValues(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, e := range arr {
		switch t := e.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			if s, ok := t["value"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// ---- resolution -----------------------------------------------------------

// reasoningResolution is the result of resolving the capability of a model.
type reasoningResolution struct {
	Cap *ReasoningCapability
}

// lockedPaths reads lockedOptionPaths.
func lockedPaths(caps capsMap) map[string]bool {
	out := map[string]bool{}
	for _, p := range caps.stringList("lockedOptionPaths") {
		out[p] = true
	}
	return out
}

func applyLock(c *ReasoningCapability, caps capsMap, pkeys []string) {
	if c == nil {
		return
	}
	locked := lockedPaths(caps)
	if locked["reasoning_effort"] {
		c.Locked = true
	}
	for _, pk := range pkeys {
		if lp := levelPath(pk); lp != "" && locked[lp] && formatSupportsProtocol(c.Format, pk) {
			c.Locked = true
		}
		if lp := levelPath(pk); lp != "" && locked[lp] && levelFormatForProtocol(pk) == c.Format {
			c.Locked = true
		}
	}
	if c.ControlPath != "" && locked[c.ControlPath] {
		c.Locked = true
	}
}

// nativeDefaultOverride lets defaultOptions native values override the default
// level of an explicit declaration.
func nativeDefaultOverride(c *ReasoningCapability, caps capsMap) {
	defaults := caps.object("defaultOptions")
	if len(defaults) == 0 {
		return
	}
	n, found := readNative(defaults, c.Format)
	if !found {
		return
	}
	level := ""
	switch {
	case n.hasLevel:
		level = n.level
	case n.hasDisabled && n.disabled:
		level = "none"
	case n.hasBudget && len(c.Budgets) > 0:
		level = nearestLevelByBudget(c.Budgets, c.Levels, n.budget)
	}
	if level != "" && containsString(c.Levels, level) {
		c.Default = level
	}
}

// resolveReasoning resolves the capability of a model across its protocols.
// protocols are the route protocols (empty when the model has no routes).
func resolveReasoning(caps capsMap, protocols []string, vendor string, entry *catalogEntry) *ReasoningCapability {
	pkeys := protocolKeys(protocols)
	if raw, declared := caps["reasoning"]; declared {
		c, err := parseExplicitReasoning(raw)
		if err != nil {
			return nil
		}
		if len(pkeys) > 0 {
			ok := false
			for _, pk := range pkeys {
				if formatSupportsProtocol(c.Format, pk) {
					ok = true
				}
			}
			if !ok {
				return nil
			}
		}
		nativeDefaultOverride(c, caps)
		applyLock(c, caps, pkeys)
		return c
	}
	for _, pk := range pkeys {
		if c := inferFromNative(caps, pk, vendor, entry); c != nil {
			applyLock(c, caps, pkeys)
			return c
		}
	}
	if len(pkeys) == 0 {
		// A model without routes: infer on the major text protocols.
		for _, pk := range []string{ProtoOpenAIResponses, ProtoAnthropicMessages, "gemini_generate_content", ProtoOpenAIChat} {
			if c := inferFromNative(caps, pk, vendor, entry); c != nil {
				applyLock(c, caps, nil)
				return c
			}
		}
		for _, pk := range []string{ProtoOpenAIResponses, ProtoAnthropicMessages, "gemini_generate_content", ProtoOpenAIChat} {
			if c, _ := catalogCapability(entry, pk, vendor); c != nil {
				applyLock(c, caps, nil)
				return c
			}
		}
		return nil
	}
	if c := catalogCapabilityAcross(entry, pkeys, vendor); c != nil {
		applyLock(c, caps, pkeys)
		return c
	}
	return nil
}

// ResolveLevel maps a requested level onto the capability: a declared level is
// used as is, a non-canonical request falls to the default and an undeclared
// canonical level maps to the nearest declared one (ties to the lower).
func ResolveLevel(c *ReasoningCapability, requested string) string {
	if c == nil {
		return ""
	}
	req := lower(requested)
	if !isCanonicalLevel(req) {
		return c.Default
	}
	if containsString(c.Levels, req) {
		return req
	}
	best, bestDiff := "", math.MaxInt
	rr := levelRank(req)
	for _, l := range c.Levels {
		d := levelRank(l) - rr
		if d < 0 {
			d = -d
		}
		if d < bestDiff || (d == bestDiff && levelRank(l) < levelRank(best)) {
			best, bestDiff = l, d
		}
	}
	return best
}

// ReasoningTemplate is the admin-form template for a reasoning declaration.
type ReasoningTemplate struct {
	Format  string         `json:"format"`
	Levels  []string       `json:"levels"`
	Budgets map[string]int `json:"budgets"`
}

// buildReasoningTemplate computes the template of the resolve endpoint. cap is
// the effective capability (may be nil), entry the reasoning catalog match and
// protocols the model's protocols.
func buildReasoningTemplate(cap *ReasoningCapability, entry *catalogEntry, protocols []string, vendor string) *ReasoningTemplate {
	pkeys := protocolKeys(protocols)
	format := ""
	if cap != nil {
		format = cap.Format
	}
	var catCap *ReasoningCapability
	if format == "" && entry != nil {
		catCap = catalogCapabilityAcross(entry, pkeys, vendor)
		if catCap == nil && len(pkeys) == 0 {
			for _, pk := range []string{ProtoOpenAIResponses, ProtoAnthropicMessages, "gemini_generate_content", ProtoOpenAIChat} {
				if c, _ := catalogCapability(entry, pk, vendor); c != nil {
					catCap = c
					break
				}
			}
		}
		if catCap != nil {
			format = catCap.Format
		}
	}
	if format == "" && len(pkeys) > 0 {
		format = levelFormatForProtocol(pkeys[0])
	}
	if format == "" {
		return nil
	}
	t := &ReasoningTemplate{Format: format}
	levels := allowedLevels(format)
	if format == fmtToggle {
		levels = []string{"none", "high"}
	}
	var catBudget *catalogOption
	if entry != nil {
		catBudget = entry.option(optBudget)
	}
	for _, l := range levels {
		if format == fmtGeminiBudget && l == "none" && catBudget != nil && catBudget.Min != nil && *catBudget.Min > 0 {
			continue
		}
		t.Levels = append(t.Levels, l)
	}
	if isBudgetFormat(format) {
		t.Budgets = map[string]int{}
		anthropic := format == fmtAnthropicBudget
		for _, l := range t.Levels {
			b := budgetTargets[l]
			if l != "none" {
				var mn, mx *int
				if catBudget != nil {
					mn, mx = catBudget.Min, catBudget.Max
				}
				b = clampBudget(b, mn, mx, anthropic)
			}
			t.Budgets[l] = b
		}
		for _, src := range []*ReasoningCapability{catCap, cap} {
			if src != nil && src.Format == format {
				for l, b := range src.Budgets {
					t.Budgets[l] = b
				}
			}
		}
	}
	return t
}
