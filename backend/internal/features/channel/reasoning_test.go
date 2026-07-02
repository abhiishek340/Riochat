package channel

import (
	"errors"
	"strings"
	"testing"
)

func caps(t *testing.T, raw string) capsMap {
	t.Helper()
	m, err := parseCaps(raw)
	if err != nil {
		t.Fatalf("caps %s: %v", raw, err)
	}
	return m
}

func validateDecl(raw string) error { return validateCapabilitiesText(raw) }

func TestReasoningDeclarationRules(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"openai ok", `{"reasoning":{"format":"openai","levels":["high","low","medium"],"default":"medium"}}`, nil},
		{"openai all levels incl none minimal xhigh max ultra", `{"reasoning":{"format":"openai","levels":["none","minimal","low","medium","high","xhigh","max","ultra"],"default":"none"}}`, nil},
		{"ultra elsewhere", `{"reasoning":{"format":"qwen","levels":["low","ultra"],"default":"low"}}`, ErrReasoningLevels},
		{"anthropic effort ok", `{"reasoning":{"format":"anthropic_effort","levels":["low","medium","high","xhigh","max"],"default":"high"}}`, nil},
		{"anthropic effort none", `{"reasoning":{"format":"anthropic_effort","levels":["none","low"],"default":"low"}}`, nil},
		{"gemini level ok", `{"reasoning":{"format":"gemini_level","levels":["minimal","low","medium","high"],"default":"low"}}`, nil},
		{"gemini level none", `{"reasoning":{"format":"gemini_level","levels":["none","low"],"default":"low"}}`, ErrReasoningLevels},
		{"gemini level xhigh", `{"reasoning":{"format":"gemini_level","levels":["low","xhigh"],"default":"low"}}`, ErrReasoningLevels},
		{"toggle three levels", `{"reasoning":{"format":"toggle","levels":["none","low","high"],"default":"low"}}`, ErrReasoningLevels},
		{"toggle lower not none", `{"reasoning":{"format":"toggle","levels":["low","high"],"default":"low"}}`, ErrReasoningLevels},
		{"toggle ok", `{"reasoning":{"format":"toggle","levels":["none","high"],"default":"none"}}`, nil},
		{"non canonical level", `{"reasoning":{"format":"openai","levels":["extreme"],"default":"low"}}`, ErrReasoningLevels},
		{"levels not strings", `{"reasoning":{"format":"openai","levels":[1],"default":"low"}}`, ErrReasoningLevels},
		{"levels missing", `{"reasoning":{"format":"openai","default":"low"}}`, ErrReasoningLevels},
		{"default missing", `{"reasoning":{"format":"openai","levels":["low"]}}`, ErrReasoningDefault},
		{"default non canonical", `{"reasoning":{"format":"openai","levels":["low"],"default":"LOW!"}}`, ErrReasoningDefault},
		{"format missing", `{"reasoning":{"levels":["low"],"default":"low"}}`, ErrReasoningFormat},
		{"reasoning array", `{"reasoning":[]}`, ErrReasoningFormat},
		{"anthropic budget requires all non-none", `{"reasoning":{"format":"anthropic_budget","levels":["none","low","high"],"default":"low","budgets":{"low":2000}}}`, ErrReasoningBudgets},
		{"anthropic budget ok", `{"reasoning":{"format":"anthropic_budget","levels":["none","low","high"],"default":"low","budgets":{"low":2000,"high":20000}}}`, nil},
		{"anthropic budget 1024", `{"reasoning":{"format":"anthropic_budget","levels":["low"],"default":"low","budgets":{"low":1024}}}`, nil},
		{"anthropic budget 1023", `{"reasoning":{"format":"anthropic_budget","levels":["low"],"default":"low","budgets":{"low":1023}}}`, ErrReasoningBudgets},
		{"budget negative", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low","budgets":{"low":-1}}}`, ErrReasoningBudgets},
		{"budget fraction", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low","budgets":{"low":1.5}}}`, ErrReasoningBudgets},
		{"budget too large", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low","budgets":{"low":2147483648}}}`, ErrReasoningBudgets},
		{"budget max int32", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low","budgets":{"low":2147483647}}}`, nil},
		{"gemini budget zero", `{"reasoning":{"format":"gemini_budget","levels":["none","low"],"default":"none","budgets":{"low":0}}}`, nil},
		{"gemini budget missing", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low"}}`, ErrReasoningBudgets},
		{"budgets not object", `{"reasoning":{"format":"gemini_budget","levels":["low"],"default":"low","budgets":[1]}}`, ErrReasoningBudgets},
		{"qwen budgets optional", `{"reasoning":{"format":"qwen","levels":["none","low"],"default":"low"}}`, nil},
		{"qwen budgets bad when given", `{"reasoning":{"format":"qwen","levels":["low"],"default":"low","budgets":{"low":-3}}}`, ErrReasoningBudgets},
		{"budgets ignored for openai", `{"reasoning":{"format":"openai","levels":["low"],"default":"low","budgets":"junk"}}`, nil},
	}
	for _, c := range cases {
		err := validateDecl(c.raw)
		if c.want == nil && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// Levels are sorted into canonical order.
	c, err := parseExplicitReasoning(caps(t, `{"reasoning":{"format":"openai","levels":["high","none","medium","low"],"default":"low"}}`)["reasoning"])
	if err != nil || strings.Join(c.Levels, ",") != "none,low,medium,high" {
		t.Fatalf("sorted: %+v %v", c, err)
	}
}

func TestReasoningProtocolKeys(t *testing.T) {
	for in, want := range map[string]string{
		"openai": "openai_responses", "OpenRouter": "openrouter_responses", "anthropic": "anthropic_messages", "claude": "anthropic_messages",
		"xai": "xai_responses", "grok": "xai_responses", "google": "gemini_generate_content", "gemini": "gemini_generate_content",
		"google_generate_content": "gemini_generate_content", "openai_chat_completions": "openai_chat_completions", "xai_image": "xai_image",
	} {
		if got := protocolKey(in); got != want {
			t.Errorf("protocolKey(%q) = %q want %q", in, got, want)
		}
	}
	got := protocolKeys([]string{"openai", "openai_responses", "google_generate_content"})
	if strings.Join(got, ",") != "openai_responses,gemini_generate_content" {
		t.Errorf("protocolKeys dedupes: %v", got)
	}
	// Which format can be translated for which protocol.
	table := map[string][]string{
		"openai":           {"openai_chat_completions", "openrouter_chat_completions", "openai_responses", "xai_responses", "openrouter_responses"},
		"anthropic_effort": {"anthropic_messages"}, "anthropic_budget": {"anthropic_messages"},
		"gemini_level": {"gemini_generate_content", "gemini_interactions"}, "gemini_budget": {"gemini_generate_content"},
		"qwen": {"openai_chat_completions", "openrouter_chat_completions"}, "toggle": {"openai_chat_completions", "openrouter_chat_completions"},
	}
	all := []string{"openai_chat_completions", "openrouter_chat_completions", "openai_responses", "xai_responses", "openrouter_responses",
		"anthropic_messages", "gemini_generate_content", "gemini_interactions", "openai_image_generations", "xai_image"}
	for format, list := range table {
		for _, pk := range all {
			if got := formatSupportsProtocol(format, pk); got != containsString(list, pk) {
				t.Errorf("format %s on %s: %v", format, pk, got)
			}
		}
	}
}

func TestReasoningExplicitResolution(t *testing.T) {
	decl := `{"reasoning":{"format":"openai","levels":["low","medium","high"],"default":"low"}`
	c := resolveReasoning(caps(t, decl+`}`), []string{ProtoOpenAIChat}, "", nil)
	if c == nil || c.Source != "explicit" || c.Default != "low" || c.Locked {
		t.Fatalf("explicit: %+v", c)
	}
	// Not translatable on the protocol: no capability, and the catalog is not consulted.
	entry := &catalogEntry{Provider: "anthropic", ID: "x", Options: []catalogOption{{Type: "effort", Values: []string{"low", "high"}}}}
	if c = resolveReasoning(caps(t, decl+`}`), []string{ProtoAnthropicMessages}, "anthropic", entry); c != nil {
		t.Fatalf("unsupported explicit: %+v", c)
	}
	// Any compatible protocol is enough in a multi-protocol view; no routes means "applies".
	if c = resolveReasoning(caps(t, decl+`}`), []string{ProtoAnthropicMessages, ProtoOpenAIResponses}, "", nil); c == nil {
		t.Fatal("compatible with any protocol")
	}
	if c = resolveReasoning(caps(t, decl+`}`), nil, "", nil); c == nil {
		t.Fatal("no routes yet")
	}
	// Invalid declaration -> none (never falls back).
	if c = resolveReasoning(caps(t, `{"reasoning":{"format":"nope"}}`), []string{ProtoOpenAIChat}, "", entry); c != nil {
		t.Fatalf("invalid: %+v", c)
	}
	// defaultOptions native values override the declared default.
	c = resolveReasoning(caps(t, decl+`,"defaultOptions":{"reasoning_effort":"high"}}`), []string{ProtoOpenAIChat}, "", nil)
	if c == nil || c.Default != "high" {
		t.Fatalf("native default: %+v", c)
	}
	// ...but only when that level is declared.
	c = resolveReasoning(caps(t, decl+`,"defaultOptions":{"reasoning_effort":"max"}}`), []string{ProtoOpenAIChat}, "", nil)
	if c == nil || c.Default != "low" {
		t.Fatalf("undeclared native default: %+v", c)
	}
	// Locked via the format's native level path on any protocol, or the generic key.
	for _, locked := range []string{`["reasoning.effort"]`, `["reasoning_effort"]`} {
		c = resolveReasoning(caps(t, decl+`,"lockedOptionPaths":`+locked+`}`), []string{ProtoOpenAIResponses}, "", nil)
		if c == nil || !c.Locked {
			t.Fatalf("locked by %s: %+v", locked, c)
		}
	}
	c = resolveReasoning(caps(t, decl+`,"lockedOptionPaths":["something_else"]}`), []string{ProtoOpenAIResponses}, "", nil)
	if c == nil || c.Locked {
		t.Fatalf("unrelated lock: %+v", c)
	}
}

func TestReasoningInferenceFromNativeParameters(t *testing.T) {
	infer := func(raw, protocol string) *ReasoningCapability {
		t.Helper()
		return resolveReasoning(caps(t, raw), []string{protocol}, "", nil)
	}
	// (a) a select control on the level path.
	c := infer(`{"optionControls":[{"path":"reasoning_effort","type":"select","options":["low","medium","bogus","ultra","low"]}],"defaultOptions":{"reasoning_effort":"medium"}}`, ProtoOpenAIChat)
	if c == nil || c.Source != "inferred" || c.ControlPath != "reasoning_effort" || strings.Join(c.Levels, ",") != "low,medium,ultra" || c.Default != "medium" || c.Format != "openai" {
		t.Fatalf("select control: %+v", c)
	}
	// Default falls to the highest level not above medium when the configured value is invalid.
	c = infer(`{"optionControls":[{"path":"reasoning.effort","type":"select","options":[{"value":"high"},{"value":"low"},{"value":"medium"}]}],"defaultOptions":{"reasoning":{"effort":"max"}}}`, ProtoOpenAIResponses)
	if c == nil || c.Default != "medium" || c.ControlPath != "reasoning.effort" {
		t.Fatalf("default fallback: %+v", c)
	}
	c = infer(`{"optionControls":[{"path":"reasoning.effort","type":"select","options":["high","max"]}]}`, ProtoOpenAIResponses)
	if c == nil || c.Default != "high" {
		t.Fatalf("first level when none is at or below medium: %+v", c)
	}
	// Each protocol has its own level path and format.
	c = infer(`{"optionControls":[{"path":"output_config.effort","type":"select","options":["minimal","low","high"]}]}`, ProtoAnthropicMessages)
	if c == nil || c.Format != "anthropic_effort" || strings.Join(c.Levels, ",") != "low,high" {
		t.Fatalf("anthropic excludes minimal: %+v", c)
	}
	c = infer(`{"optionControls":[{"path":"generationConfig.thinkingConfig.thinkingLevel","type":"select","options":["low","high","max"]}]}`, "gemini_generate_content")
	if c == nil || c.Format != "gemini_level" || strings.Join(c.Levels, ",") != "low,high" {
		t.Fatalf("gemini: %+v", c)
	}
	c = infer(`{"optionControls":[{"path":"generation_config.thinking_level","type":"select","options":["low","high"]}]}`, ProtoGeminiInteractions)
	if c == nil || c.Format != "gemini_level" || c.ControlPath != "generation_config.thinking_level" {
		t.Fatalf("interactions: %+v", c)
	}
	// A control on another protocol's path or of a free-text type does not infer.
	// A control on another protocol's path is not the protocol's level control; it still counts as a declared native control.
	c = infer(`{"optionControls":[{"path":"reasoning.effort","type":"select","options":["low"]}]}`, ProtoOpenAIChat)
	if c == nil || c.ControlPath != "" || c.Source != "inferred" {
		t.Fatalf("path of another protocol: %+v", c)
	}
	// A free-text control on the level path is no select (a), but counts as a declared native control (c).
	c = infer(`{"optionControls":[{"path":"reasoning_effort","type":"text"}]}`, ProtoOpenAIChat)
	if c == nil || c.ControlPath != "" || c.Source != "inferred" {
		t.Fatalf("free text: %+v", c)
	}
	// (b) native parameters in the default options.
	c = infer(`{"defaultOptions":{"reasoning_effort":"high"}}`, ProtoOpenAIChat)
	if c == nil || c.Format != "openai" || c.Default != "high" || strings.Join(c.Levels, ",") != "low,medium,high" || c.ControlPath != "" {
		t.Fatalf("effort default: %+v", c)
	}
	c = infer(`{"defaultOptions":{"reasoning_effort":"xhigh"}}`, ProtoOpenAIChat)
	if c == nil || c.Default != "xhigh" || !containsString(c.Levels, "xhigh") {
		t.Fatalf("the administrator's level is added: %+v", c)
	}
	c = infer(`{"defaultOptions":{"thinking":{"type":"enabled","budget_tokens":8000}}}`, ProtoAnthropicMessages)
	if c == nil || c.Format != "anthropic_budget" || c.Default != "medium" || c.Budgets["low"] != 2048 || c.Budgets["medium"] != 8192 || c.Budgets["high"] != 24576 {
		t.Fatalf("anthropic budget: %+v", c)
	}
	c = infer(`{"defaultOptions":{"thinking":{"type":"enabled","budget_tokens":30000}}}`, ProtoAnthropicMessages)
	if c == nil || c.Default != "high" {
		t.Fatalf("nearest budget: %+v", c)
	}
	c = infer(`{"defaultOptions":{"thinking":{"type":"disabled"}}}`, ProtoAnthropicMessages)
	if c == nil || c.Format != "anthropic_budget" || c.Default != "none" || !containsString(c.Levels, "none") {
		t.Fatalf("disabled: %+v", c)
	}
	c = infer(`{"defaultOptions":{"enable_thinking":false}}`, ProtoOpenAIChat)
	if c == nil || c.Format != "qwen" || c.Default != "none" || strings.Join(c.Levels, ",") != "none,low,medium,high" {
		t.Fatalf("qwen disabled: %+v", c)
	}
	c = infer(`{"defaultOptions":{"enable_thinking":true,"thinking_budget":8192}}`, ProtoOpenAIChat)
	if c == nil || c.Format != "qwen" || c.Default != "medium" || c.Budgets["medium"] != 8192 {
		t.Fatalf("qwen budget: %+v", c)
	}
	c = infer(`{"defaultOptions":{"thinking":{"type":"disabled"}}}`, ProtoOpenAIChat)
	if c == nil || c.Format != "toggle" || strings.Join(c.Levels, ",") != "none,high" || c.Default != "none" {
		t.Fatalf("toggle: %+v", c)
	}
	c = infer(`{"defaultOptions":{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}}`, "gemini_generate_content")
	if c == nil || c.Format != "gemini_level" || c.Default != "high" || strings.Join(c.Levels, ",") != "low,high" {
		t.Fatalf("gemini level default: %+v", c)
	}
	c = infer(`{"defaultOptions":{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}}`, "gemini_generate_content")
	if c == nil || c.Format != "gemini_budget" || c.Default != "none" {
		t.Fatalf("gemini budget zero: %+v", c)
	}
	// (c) native thinking controls declared without defaults.
	c = infer(`{"optionControls":[{"path":"enable_thinking","type":"boolean"}]}`, ProtoOpenAIChat)
	if c == nil || c.Format != "qwen" || c.Source != "inferred" {
		t.Fatalf("declared control: %+v", c)
	}
	if c = infer(`{}`, ProtoOpenAIChat); c != nil {
		t.Fatalf("nothing declared: %+v", c)
	}
	// The first protocol (in route order) that infers one wins in a multi-protocol view.
	multi := resolveReasoning(caps(t, `{"defaultOptions":{"reasoning_effort":"low"}}`), []string{ProtoAnthropicMessages, ProtoOpenAIChat}, "", nil)
	if multi == nil || multi.Format != "openai" {
		t.Fatalf("multi: %+v", multi)
	}
}

func TestCatalogCapabilityDetails(t *testing.T) {
	effort := &catalogEntry{Provider: "openai", ID: "a", Options: []catalogOption{{Type: "effort", Values: []string{"none", "minimal", "low", "medium", "high", "xhigh", "weird", "ultra"}}}}
	c, why := catalogCapability(effort, ProtoOpenAIResponses, "openai")
	if c == nil || strings.Join(c.Levels, ",") != "none,minimal,low,medium,high,xhigh,ultra" || c.Default != "medium" || why != "" {
		t.Fatalf("openai effort: %+v %v", c, why)
	}
	c, _ = catalogCapability(effort, ProtoAnthropicMessages, "anthropic")
	if c == nil || c.Format != "anthropic_effort" || containsString(c.Levels, "minimal") || containsString(c.Levels, "ultra") {
		t.Fatalf("anthropic effort drops minimal and ultra: %+v", c)
	}
	c, _ = catalogCapability(effort, ProtoGeminiInteractions, "google")
	if c == nil || c.Format != "gemini_level" || strings.Join(c.Levels, ",") != "minimal,low,medium,high" {
		t.Fatalf("gemini effort: %+v", c)
	}
	if c, why = catalogCapability(effort, ProtoOpenAIImageGen, "openai"); c != nil || why == "" {
		t.Fatalf("image protocol: %+v %v", c, why)
	}
	if c, why = catalogCapability(&catalogEntry{Provider: "p", ID: "q"}, ProtoOpenAIChat, ""); c != nil || why == "" {
		t.Fatalf("not reasoning: %+v %v", c, why)
	}
	// Default when medium is not offered: the highest non-none level not above medium, else the lowest non-none.
	low := &catalogEntry{Options: []catalogOption{{Type: "effort", Values: []string{"low", "high"}}}}
	if c, _ = catalogCapability(low, ProtoOpenAIResponses, "openai"); c == nil || c.Default != "low" {
		t.Fatalf("default without medium: %+v", c)
	}
	hi := &catalogEntry{Options: []catalogOption{{Type: "effort", Values: []string{"none", "high", "max"}}}}
	if c, _ = catalogCapability(hi, ProtoOpenAIResponses, "openai"); c == nil || c.Default != "high" {
		t.Fatalf("default skipping none: %+v", c)
	}
	// Budgets: clamped to the declared range, adjacent equal levels drop the lower one.
	one, ten := 1024, 3000
	budget := &catalogEntry{Options: []catalogOption{{Type: "budget_tokens", Min: &one, Max: &ten}}}
	c, _ = catalogCapability(budget, ProtoAnthropicMessages, "anthropic")
	if c == nil || c.Format != "anthropic_budget" || strings.Join(c.Levels, ",") != "low,high" || c.Budgets["low"] != 2048 || c.Budgets["high"] != 3000 {
		t.Fatalf("clamped: %+v", c)
	}
	zero := 0
	gb := &catalogEntry{Options: []catalogOption{{Type: "budget_tokens", Min: &zero}}}
	c, _ = catalogCapability(gb, "gemini_generate_content", "google")
	if c == nil || c.Format != "gemini_budget" || !containsString(c.Levels, "none") || c.Budgets["none"] != 0 {
		t.Fatalf("gemini none with zero minimum: %+v", c)
	}
	c, _ = catalogCapability(&catalogEntry{Options: []catalogOption{{Type: "budget_tokens", Min: &one}}}, "gemini_generate_content", "google")
	if c == nil || containsString(c.Levels, "none") {
		t.Fatalf("gemini without none when min > 0: %+v", c)
	}
	// Qwen detection.
	qb := &catalogEntry{Provider: "alibaba", ID: "qwen3-max", CanonicalID: "alibaba/qwen3-max", Options: []catalogOption{{Type: "toggle"}, {Type: "budget_tokens"}}}
	for _, v := range []string{"alibaba", "qwen", ""} {
		if c, _ = catalogCapability(qb, ProtoOpenAIChat, v); c == nil || c.Format != "qwen" {
			t.Errorf("qwen with vendor %q: %+v", v, c)
		}
	}
	if c, _ = catalogCapability(qb, ProtoOpenAIChat, "moonshot"); c == nil || c.Format != "toggle" {
		t.Errorf("other known vendor: %+v", c)
	}
	if c, _ = catalogCapability(qb, ProtoOpenAIResponses, "alibaba"); c != nil {
		t.Errorf("qwen only on chat completions: %+v", c)
	}
	// Over several protocols the capability valid on the most protocols wins.
	across := catalogCapabilityAcross(effort, []string{ProtoAnthropicMessages, ProtoOpenAIChat, ProtoOpenAIResponses}, "")
	if across == nil || across.Format != "openai" {
		t.Fatalf("across: %+v", across)
	}
	tie := catalogCapabilityAcross(effort, []string{ProtoAnthropicMessages, ProtoOpenAIChat}, "")
	if tie == nil || tie.Format != "anthropic_effort" {
		t.Fatalf("ties go to the earliest protocol: %+v", tie)
	}
}

func TestLegacyControlsCompile(t *testing.T) {
	raw := `{
	  "defaultOptions": {"top_p": 0.9, "style": "vivid", "safe": true, "n": 3},
	  "optionControls": [
	    {"path":"top_p","type":"number","label":"Top P","min":0,"max":1,"step":0.05},
	    {"path":"style","type":"select","options":["natural","vivid"]},
	    {"path":"n","type":"select","options":["1","3"]},
	    {"path":"safe","type":"boolean"},
	    {"path":"free_text","type":"text"},
	    {"path":"json_blob","type":"json"},
	    {"path":"model","type":"select","options":["x"]},
	    {"path":"stream_options.include_usage","type":"boolean"},
	    {"path":"reasoning_effort","type":"select","options":["low"]},
	    {"path":"enable_thinking","type":"boolean"},
	    {"path":"-bad","type":"boolean"},
	    {"path":"covered","type":"boolean"},
	    {"path":"locked_one","type":"boolean"},
	    {"path":"empty_select","type":"select","options":[]}
	  ],
	  "lockedOptionPaths": ["locked_one"],
	  "controls": [{"id":"explicit","type":"toggle","on":{"patch":{"covered":true}},"off":{"patch":{"covered":false}}}]
	}`
	list := effectiveControls(caps(t, raw), nil, []string{ProtoOpenAIChat}, false)
	byID := map[string]*Control{}
	var order []string
	for _, c := range list {
		byID[c.ID] = c
		order = append(order, c.ID)
	}
	// Explicit controls precede legacy ones.
	if len(order) < 2 || order[0] != "explicit" {
		t.Fatalf("order: %v", order)
	}
	for _, skipped := range []string{"free_text", "json_blob", "model", "stream_options.include_usage", "reasoning_effort", "enable_thinking", "-bad", "covered", "empty_select"} {
		if byID[skipped] != nil {
			t.Errorf("%s must not be exposed", skipped)
		}
	}
	tp := byID["top_p"]
	if tp == nil || tp.Type != "number" || tp.Placement != "menu" || tp.Label != "Top P" || tp.Min == nil || *tp.Max != 1 || *tp.Step != 0.05 || tp.Default == nil || *tp.Default != "0.9" {
		t.Fatalf("top_p: %+v", tp)
	}
	st := byID["style"]
	if st == nil || st.Type != "select" || st.Default == nil || *st.Default != "vivid" || len(st.Options) != 2 {
		t.Fatalf("style: %+v", st)
	}
	if p := st.Patch("natural", ProtoOpenAIChat); p["style"] != "natural" {
		t.Errorf("select patch writes the value: %v", p)
	}
	n := byID["n"]
	if n == nil {
		t.Fatal("n missing")
	}
	if p := n.Patch("3", ProtoOpenAIChat); p["n"] != float64(3) {
		t.Errorf("numeric defaults make numeric values: %#v", p)
	}
	sf := byID["safe"]
	if sf == nil || sf.Type != "toggle" || *sf.Default != "on" {
		t.Fatalf("safe: %+v", sf)
	}
	if p := sf.Patch("off", ProtoOpenAIChat); p["safe"] != false {
		t.Errorf("toggle patch: %v", p)
	}
	if lk := byID["locked_one"]; lk == nil || !lk.Locked {
		t.Fatalf("locked path locks the control: %+v", lk)
	}
}

func TestExplicitReasoningControlOverride(t *testing.T) {
	cp := caps(t, `{"reasoning":{"format":"openai","levels":["low","medium","high"],"default":"medium"},
	  "controls":[{"id":"x","kind":"reasoning","label":"Think","description":"how hard","icon":"zap","placement":"menu","options":[{"value":"high","label":"Max effort"},{"value":"low"}]},
	              {"id":"other","type":"toggle"}]}`)
	rc := resolveReasoning(cp, []string{ProtoOpenAIChat}, "", nil)
	list := effectiveControls(cp, rc, []string{ProtoOpenAIChat}, false)
	if len(list) != 2 || list[0].ID != "reasoning" || list[1].ID != "other" {
		t.Fatalf("controls: %+v", list)
	}
	r := list[0]
	if r.Kind != "reasoning" || r.Label != "Think" || r.Description != "how hard" || r.Icon != "zap" || r.Placement != "menu" {
		t.Fatalf("override presentation: %+v", r)
	}
	if len(r.Options) != 2 || r.Options[0].Value != "low" || r.Options[1].Label != "Max effort" {
		t.Fatalf("option subset: %+v", r.Options)
	}
	if r.Default == nil || *r.Default != "low" {
		t.Fatalf("a default outside the subset moves to its first option: %v", r.Default)
	}
	// A hidden override hides the generated control but not from includeHidden callers.
	hid := caps(t, `{"reasoning":{"format":"openai","levels":["low","high"],"default":"low"},"controls":[{"id":"reasoning","kind":"reasoning","hidden":true}]}`)
	rc = resolveReasoning(hid, []string{ProtoOpenAIChat}, "", nil)
	if got := effectiveControls(hid, rc, []string{ProtoOpenAIChat}, false); len(got) != 0 {
		t.Fatalf("hidden: %+v", got)
	}
	if got := effectiveControls(hid, rc, []string{ProtoOpenAIChat}, true); len(got) != 1 || got[0].Default == nil || *got[0].Default != "low" {
		t.Fatalf("hidden defaults still apply: %+v", got)
	}
	// Invalid stored controls never break the catalog.
	if got := effectiveControls(caps(t, `{"controls":"junk"}`), nil, nil, false); len(got) != 0 {
		t.Fatalf("junk: %+v", got)
	}
}

func TestCapabilityLimitsAndModalities(t *testing.T) {
	c := caps(t, `{"context_window":"32000","max_output_tokens":2000}`)
	if c.contextWindowOverride() != 32000 || c.maxOutputOverride() != 2000 {
		t.Fatalf("aliases: %d %d", c.contextWindowOverride(), c.maxOutputOverride())
	}
	// Priority order of the window keys.
	c = caps(t, `{"contextWindowTokens":10000,"contextWindow":20000}`)
	if c.contextWindowOverride() != 20000 {
		t.Fatalf("priority: %d", c.contextWindowOverride())
	}
	c = caps(t, `{"contextWindow":"abc","context_window":8192}`)
	if c.contextWindowOverride() != 8192 && c.contextWindowOverride() != 0 {
		t.Fatalf("junk window: %d", c.contextWindowOverride())
	}
	if effectiveMaxOutput("gpt-4.1", capsMap{}) != 32768 || effectiveMaxOutput("mystery", capsMap{}) != 8192 || effectiveMaxOutput("gpt-4.1", caps(t, `{"maxOutputTokens":1000}`)) != 1000 {
		t.Error("effective max output")
	}
	if w := displayContextWindow(capsMap{}, 3000); w != nil {
		t.Error("catalog windows below 4096 are ignored")
	}
	if w := displayContextWindow(capsMap{}, 20_000_000); w != nil {
		t.Error("catalog windows above 16M are ignored")
	}
	if w := displayContextWindow(capsMap{}, 8192); w == nil || *w != 8192 {
		t.Error("catalog window")
	}
	// Family table: separators, whole-number digit groups and word boundaries.
	cases := map[string]int{
		"gpt-4.1": 1047576, "GPT_4_1": 1047576, "gpt-4.10": 0, "gpt-4.1-mini": 1047576, "azure/gpt-4o-2024": 128000, "claude-sonnet-3.7": 200000,
		"claude-3.7-sonnet": 200000, "claude-3-5-haiku": 200000, "claude-haiku-3.5": 200000, "o1": 200000, "o3-mini": 200000, "o4": 200000,
		"gemini-1.5-pro": 1000000, "gemini-2.0-flash": 1000000, "grok-3": 131072, "gpt-3.5-turbo": 16385, "gpt-4.5-preview": 128000,
		"proto1": 0, "model:o3": 200000, "foo/o4-mini": 200000,
	}
	for name, want := range cases {
		w, _, ok := familyLimits(name)
		if (want == 0) == ok || (ok && w != want) {
			t.Errorf("familyLimits(%q) = %d,%v want %d", name, w, ok, want)
		}
	}
	// Modalities.
	if got := normalizeModalities([]string{"video", "IMAGE", "image", "smell"}, true); strings.Join(got, ",") != "text,image,video" {
		t.Errorf("input: %v", got)
	}
	if got := normalizeModalities([]string{"audio"}, false); strings.Join(got, ",") != "audio" {
		t.Errorf("output keeps text optional: %v", got)
	}
	if got := normalizeModalities(nil, true); len(got) != 0 {
		t.Errorf("empty stays empty: %v", got)
	}
	mods, declared, valid := caps(t, `{"inputModalities":["pdf","image"]}`).explicitInputModalities()
	if !declared || !valid || strings.Join(mods, ",") != "text,image,pdf" {
		t.Errorf("explicit: %v %v %v", mods, declared, valid)
	}
	if _, declared, valid = caps(t, `{"inputModalities":["image","smell"]}`).explicitInputModalities(); !declared || valid {
		t.Error("an unknown modality makes the declaration invalid")
	}
	if _, declared, _ = (capsMap{}).explicitInputModalities(); declared {
		t.Error("undeclared")
	}
}
