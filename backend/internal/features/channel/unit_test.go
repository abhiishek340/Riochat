package channel

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPlatformNameNormalization(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	good := map[string]string{" gpt-4o ": "gpt-4o", "GPT 4o": "GPT 4o", "a/b:c": "a/b:c", "\u4e2d\u6587\u6a21\u578b": "\u4e2d\u6587\u6a21\u578b"}
	for in, want := range good {
		if got, err := normalizePlatformName(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "   ", "tab\tin", "nl\nin", "cr\rin", "nul\x00in", "personal:x", "Personal:x/y", " personal:z", "nbsp in", "ls in"} {
		if _, err := normalizePlatformName(in); !errors.Is(err, ErrInvalidPlatformName) {
			t.Errorf("%q must be refused: %v", in, err)
		}
	}
}

func TestKindsNormalization(t *testing.T) {
	good := map[string]string{
		`["chat"]`:                        `["chat"]`,
		` [" Chat ","IMAGE_GEN","chat"] `: `["chat","image_gen"]`,
		`image_edit`:                      `["image_edit"]`,
		`"video_gen"`:                     `["video_gen"]`,
	}
	for in, want := range good {
		got, err := normalizeKinds(in)
		if err != nil || kindsJSON(got) != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{``, `[]`, `["nope"]`, `["chat",""]`, `{"a":1}`, `[1]`, `null`, `["chat","telepathy"]`} {
		if _, err := normalizeKinds(in); !errors.Is(err, ErrInvalidKinds) {
			t.Errorf("%q must be refused: %v", in, err)
		}
	}
	if got := parseStoredKinds(`["chat","audio"]`); strings.Join(got, ",") != "chat,audio" {
		t.Errorf("stored: %v", got)
	}
	if got := parseStoredKinds(`garbage`); len(got) != 0 && strings.Join(got, ",") != "chat" {
		t.Errorf("garbage stored kinds: %v", got)
	}
}

func TestKindInference(t *testing.T) {
	cases := map[string]string{
		"gemini-omni-flash-x":        `["chat","image_gen","image_edit","video_gen"]`,
		"Gemini-Omni-Flash":          `["chat","image_gen","image_edit","video_gen"]`,
		"gpt-image-1":                `["image_gen","image_edit"]`,
		"gpt-image-1-mini":           `["image_gen","image_edit"]`,
		"chatgpt-image-latest":       `["image_gen","image_edit"]`,
		"dall-e-2":                   `["image_gen","image_edit"]`,
		"nano-banana":                `["image_gen","image_edit"]`,
		"nano-banana-2":              `["image_gen","image_edit"]`,
		"nano-banana-pro":            `["image_gen","image_edit"]`,
		"gemini-2.5-flash-image":     `["image_gen","image_edit"]`,
		"gemini-3.1-flash-image":     `["image_gen","image_edit"]`,
		"gemini-3-pro-image":         `["image_gen","image_edit"]`,
		"gemini-3-pro-image-preview": `["image_gen","image_edit"]`,
		"grok-imagine-image-beta":    `["image_gen","image_edit"]`,
		"dall-e-3":                   `["image_gen"]`,
		"imagen-4.0":                 `["image_gen"]`,
		"grok-imagine-video-1":       `["video_gen","video_extension"]`,
		"sora":                       `["video_gen"]`,
		"veo-2":                      `["video_gen"]`,
		"veo-3.1":                    `["video_gen"]`,
		"kling-v2":                   `["video_gen"]`,
		"gpt-4o-audio-preview":       `["audio"]`,
		"gpt-4o":                     `["chat"]`,
		"gemini-2.5-pro":             `["chat"]`,
		"sora-2":                     `["chat"]`,
		"dall-e":                     `["chat"]`,
		"":                           `["chat"]`,
	}
	for name, want := range cases {
		if got := kindsJSON(inferKinds(name)); got != want {
			t.Errorf("%q: %s want %s", name, got, want)
		}
	}
}

func TestVendorCanonicalAndDetection(t *testing.T) {
	canon := map[string]string{
		"openai": "openai", "Claude": "anthropic", "anthropic": "anthropic", "gemini": "google", "gemma": "google", "llama": "meta",
		"phi": "microsoft", "bedrock": "amazon", "nova": "amazon", "titan": "amazon", "aws": "amazon", "nemotron": "nvidia", "grok": "xai",
		"kimi": "moonshot", "qwen": "alibaba", "qwq": "alibaba", "qvq": "alibaba", "tongyi": "alibaba", "wanx": "alibaba",
		"codestral": "mistral", "devstral": "mistral", "glm": "zhipu", "chatglm": "zhipu", "bigmodel": "zhipu", "abab": "minimax", "hailuo": "minimax",
		"doubao": "bytedance", "volcengine": "bytedance", "seed": "bytedance", "byte": "bytedance", "hunyuan": "tencent", "longcat": "longcat",
		"mimo": "xiaomi", "xiaomimimo": "xiaomi", "xiaomi-mi-mo": "xiaomi", "spark": "iflytek", "iflytekcloud": "iflytek", "step": "stepfun",
		"baichuan": "baichuan", "ernie": "baidu", "wenxin": "baidu", "openrouter": "openrouter", "github": "copilot", "copilot": "copilot", "unknown": "unknown",
		"deepseek": "deepseek", "meta": "meta", "mixtral": "mistral", "ministral": "mistral", "pixtral": "mistral",
		"": "", "mystery": "",
	}
	for in, want := range canon {
		if got := canonicalVendor(in); got != want {
			t.Errorf("canonical(%q) = %q want %q", in, got, want)
		}
	}
	detect := map[string]string{
		"claude-3-opus": "anthropic", "anthropic/claude": "anthropic", "gemini-2.5-pro": "google", "gemma-3": "google", "imagen-4": "google", "veo-3": "google",
		"nano-banana": "google", "models/gemini-pro": "google", "llama-3.1-70b": "meta", "meta/llama": "meta", "phi-4": "microsoft", "nova-pro": "amazon",
		"titan-embed": "amazon", "us.amazon.nova-lite": "amazon", "aws/titan": "amazon", "bedrock-claude": "amazon", "nemotron-4": "nvidia", "nvidia/x": "nvidia",
		"grok-4": "xai", "xai/grok": "xai", "deepseek-r1": "deepseek", "kimi-k2": "moonshot", "moonshot-v1": "moonshot", "qwen3-max": "alibaba", "qwq-32b": "alibaba",
		"wanx2.1": "alibaba", "tongyi-x": "alibaba", "mistral-large": "mistral", "mixtral-8x7b": "mistral", "codestral": "mistral", "acme-mistral-ft": "mistral",
		"glm-4.5": "zhipu", "chatglm3": "zhipu", "cogview-4": "zhipu", "cogvideox": "zhipu", "bigmodel-x": "zhipu", "minimax-m1": "minimax", "abab6.5": "minimax",
		"hailuo-02": "minimax", "doubao-pro": "bytedance", "seed-oss": "bytedance", "bytedance/x": "bytedance", "volcengine/x": "bytedance", "hunyuan-large": "tencent",
		"tencent/hy": "tencent", "longcat-flash": "longcat", "mimo-7b": "xiaomi", "xiaomi/x": "xiaomi", "spark-max": "iflytek", "iflytek-x": "iflytek",
		"step-1v": "stepfun", "stepfun/x": "stepfun", "baichuan4": "baichuan", "ernie-4.5": "baidu", "wenxin-x": "baidu", "baidu/x": "baidu",
		"copilot-x": "copilot", "github/gpt": "copilot", "gpt-4o": "openai", "chatgpt-4o-latest": "openai", "o1-mini": "openai", "o3": "openai", "o4-mini": "openai",
		"codex-mini": "openai", "dall-e-3": "openai", "sora": "openai", "openai/gpt-4": "openai", "openrouter/auto": "openrouter", "openrouter-x": "openrouter",
		"unknown-thing": "", "": "",
	}
	for in, want := range detect {
		if got := detectVendor(in); got != want {
			t.Errorf("detect(%q) = %q want %q", in, got, want)
		}
	}
	// Names are examined whole, after the first slash and after the last slash.
	if got := detectVendor("someone/claude-3"); got != "anthropic" {
		t.Errorf("after slash: %q", got)
	}
	if got := detectVendor("a/b/gemini-x"); got != "google" {
		t.Errorf("after last slash: %q", got)
	}
	// openrouter only wins when nothing else matched.
	if got := detectVendor("openrouter/claude-3"); got != "anthropic" {
		t.Errorf("openrouter fallback order: %q", got)
	}
	// deriveVendor: canonical raw wins, then detection over names, then unknown.
	if got := deriveVendor("Claude", "gpt-4o"); got != "anthropic" {
		t.Errorf("raw wins: %q", got)
	}
	if got := deriveVendor("custom-owner", "gpt-4o"); got != "openai" {
		t.Errorf("names: %q", got)
	}
	if got := deriveVendor("", "zzz", "my upstream", "https://api.deepseek.com"); got != "unknown" && got != "deepseek" {
		t.Errorf("base url candidate: %q", got)
	}
	if got := deriveVendor("", "zzz"); got != "unknown" {
		t.Errorf("fallback: %q", got)
	}
}

func TestVendorKeyNormalization(t *testing.T) {
	good := map[string]string{"Claude": "anthropic", " OpenAI ": "openai", "my_vendor-1": "my_vendor-1", "x": "x", "0abc": "0abc"}
	for in, want := range good {
		if got, err := normalizeVendorKey(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-x", "_x", "has space", "UPPER!", "a.b", strings.Repeat("a", 65)} {
		if _, err := normalizeVendorKey(in); !errors.Is(err, ErrInvalidVendor) {
			t.Errorf("%q must be refused: %v", in, err)
		}
	}
}

func TestAutoIcon(t *testing.T) {
	cases := []struct{ explicit, name, vendor, want string }{
		{"Custom", "gpt-4o", "openai", "Custom"},
		{"", "claude-3", "anthropic", "claude"},
		{"", "nano-banana-pro", "google", "nanobanana"},
		{"", "gemini-3-pro-image", "google", "nanobanana"},
		{"", "gemma-3", "google", "gemma"},
		{"", "gemini-2.5", "google", "gemini"},
		{"", "imagen-4", "google", "gemini"},
		{"", "veo-3", "google", "gemini"},
		{"", "llama-3", "meta", "meta"},
		{"", "phi-4", "microsoft", "microsoft"},
		{"", "nova-pro", "amazon", "nova"},
		{"", "titan-x", "amazon", "bedrock"},
		{"", "my-bedrock-model", "amazon", "bedrock"},
		{"", "nemotron-4", "nvidia", "nvidia"},
		{"", "grok-4", "xai", "grok"},
		{"", "deepseek-r1", "deepseek", "deepseek"},
		{"", "kimi-k2", "moonshot", "kimi"},
		{"", "qwen3", "alibaba", "qwen"},
		{"", "mixtral-8x7b", "mistral", "mistral"},
		{"", "glm-4.5", "zhipu", "chatglm"},
		{"", "cogview-4", "zhipu", "cogview"},
		{"", "minimax-m1", "minimax", "minimax"},
		{"", "hailuo-02", "minimax", "hailuo"},
		{"", "doubao-pro", "bytedance", "doubao"},
		{"", "hunyuan-x", "tencent", "hunyuan"},
		{"", "longcat-x", "longcat", "longcat"},
		{"", "mimo-7b", "xiaomi", "xiaomimimo"},
		{"", "spark-max", "iflytek", "spark"},
		{"", "step-1", "stepfun", "stepfun"},
		{"", "baichuan4", "baichuan", "baichuan"},
		{"", "ernie-4", "baidu", "wenxin"},
		{"", "dall-e-3", "openai", "dalle"},
		{"", "sora", "openai", "sora"},
		{"", "gpt-4o", "openai", "openai"},
		{"", "o3-mini", "openai", "openai"},
		{"", "mystery", "anthropic", "claude"},
		{"", "mystery", "google", "gemini"},
		{"", "mystery", "amazon", "aws"},
		{"", "mystery", "xai", "grok"},
		{"", "mystery", "xiaomi", "xiaomimimo"},
		{"", "mystery", "iflytek", "iflytekcloud"},
		{"", "mystery", "deepseek", "deepseek"},
		{"", "mystery", "openrouter", "openrouter"},
		{"", "mystery", "copilot", "copilot"},
		{"", "mystery", "unknown", ""},
		{"", "mystery", "", ""},
	}
	for _, c := range cases {
		if got := autoIcon(c.explicit, c.name, c.vendor); got != c.want {
			t.Errorf("autoIcon(%q,%q,%q) = %q want %q", c.explicit, c.name, c.vendor, got, c.want)
		}
	}
}

func TestBaseURLValidation(t *testing.T) {
	good := []string{"https://api.openai.com/v1", " http://10.0.0.5:8080 ", "http://192.168.1.2", "https://example.com:8443/path", "http://[2001:db8::1]:80/v1",
		"http://127.0.0.1:11434", "http://internal.corp:3000"}
	for _, u := range good {
		if _, err := validateUpstreamBaseURL(u); err != nil {
			t.Errorf("%q must be allowed: %v", u, err)
		}
	}
	bad := []string{"", "not a url", "ftp://example.com", "file:///etc/passwd", "javascript:alert(1)", "//example.com", "https://", "https://u:p@example.com",
		"http://169.254.169.254", "http://169.254.169.254/latest/meta-data", "http://[fe80::1%25eth0]/", "http://metadata.google.internal", "http://[fe80::1]/",
		"https://exa mple.com", "https://-bad-.example.com", strings.Repeat("a", 3)}
	for _, u := range bad {
		if _, err := validateUpstreamBaseURL(u); !errors.Is(err, ErrInvalidBaseURL) {
			t.Errorf("%q must be refused: %v", u, err)
		}
	}
}

func TestHeaderRedactionAndValidation(t *testing.T) {
	in := `{"Authorization":"x","X_API_KEY":"y","my-token":"z","Secret-Thing":"s","Cookie":"c","Set-Cookie":"d","Accept":"json","Api-Key":"k","apikey":"k2","X-Trace":"t"}`
	var out map[string]string
	if err := json.Unmarshal([]byte(redactHeadersJSON(in)), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Authorization", "X_API_KEY", "my-token", "Secret-Thing", "Cookie", "Set-Cookie", "Api-Key", "apikey"} {
		if out[k] != "********" {
			t.Errorf("%s not redacted: %q", k, out[k])
		}
	}
	if out["Accept"] != "json" || out["X-Trace"] != "t" {
		t.Errorf("benign headers changed: %v", out)
	}
	if got := redactHeadersJSON(`{`); got != `{}` {
		t.Errorf("invalid json -> %q", got)
	}
	if got := redactHeadersJSON(``); got != `` {
		t.Errorf("empty stays empty: %q", got)
	}
	if got := redactHeadersJSON(`[1]`); got != `{}` && got != `[1]` {
		t.Errorf("non-object: %q", got)
	}
	if err := validateHeadersJSON(`[1,2]`); err != nil {
		t.Errorf("any valid JSON is accepted: %v", err)
	}
	if err := validateHeadersJSON(`{`); !errors.Is(err, ErrInvalidHeaders) {
		t.Errorf("invalid: %v", err)
	}
}

func TestFieldNormalizers(t *testing.T) {
	if normalizeStatus("  ") != "active" || normalizeStatus(" inactive ") != "inactive" || normalizeStatus("weird") != "weird" {
		t.Error("status")
	}
	if atLeastOne(0) != 1 || atLeastOne(-4) != 1 || atLeastOne(9) != 9 || nonNegative(-1) != 0 || nonNegative(3) != 3 {
		t.Error("clamps")
	}
	if timeoutOrDefault(0, 5) != 5 || timeoutOrDefault(-1, 5) != 5 || timeoutOrDefault(7, 5) != 7 {
		t.Error("timeouts")
	}
	if normalizeThresholdLogic("and") != "and" || normalizeThresholdLogic("AND") != "or" || normalizeThresholdLogic("") != "or" || normalizeThresholdLogic("xor") != "or" {
		t.Error("logic")
	}
	if normalizeCBPolicyMode("Enforced") != "enforced" || normalizeCBPolicyMode("") != "default" || normalizeCBPolicyMode("other") != "default" {
		t.Error("policy mode")
	}
	for in, want := range map[string]string{"": "public", " Public ": "public", "INTERNAL": "internal"} {
		if got, err := normalizeAccessScope(in); err != nil || got != want {
			t.Errorf("scope %q: %q %v", in, got, err)
		}
	}
	if _, err := normalizeAccessScope("private"); !errors.Is(err, ErrInvalidAccessScope) {
		t.Error("scope private")
	}
	if p, err := validateSystemPrompt("  hi  "); err != nil || p != "hi" {
		t.Errorf("prompt: %q %v", p, err)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if _, err := validateSystemPrompt(strings.Repeat("\u5b57", 20001)); !errors.Is(err, ErrSystemPromptLong) {
		t.Error("prompt length counts code points")
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if _, err := validateSystemPrompt(strings.Repeat("\u5b57", 20000)); err != nil {
		t.Error("prompt at the limit")
	}
	if n, ok := normalizeDisplayName("  Name  "); !ok || n != "Name" {
		t.Error("display name")
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if _, ok := normalizeDisplayName(strings.Repeat("\u5b57", 65)); ok {
		t.Error("display name length")
	}
	if got := uniqueUints([]uint{3, 1, 3, 2, 1}); len(got) != 3 || got[0] != 3 || got[2] != 2 {
		t.Errorf("uniqueUints: %v", got)
	}
}

func TestProtocolRules(t *testing.T) {
	allowed := map[string][]string{
		KindChat:           {"openai_responses", "openrouter_chat_completions", "openrouter_responses", "openai_chat_completions", "anthropic_messages", "google_generate_content", "gemini_interactions", "xai_responses"},
		KindAudio:          {"openai_responses", "openrouter_chat_completions", "openrouter_responses", "openai_chat_completions", "anthropic_messages", "google_generate_content", "xai_responses"},
		KindImageGen:       {"openai_image_generations", "openrouter_images", "google_image_generation", "gemini_interactions", "xai_image"},
		KindImageEdit:      {"openai_image_edits", "openrouter_images", "google_image_generation", "gemini_interactions", "xai_image_edits"},
		KindVideoGen:       {"openai_video_generations", "gemini_interactions", "xai_video"},
		KindVideoExtension: {"xai_video_extensions"},
	}
	for kind, list := range allowed {
		for p := range knownProtocols {
			want := containsString(list, p)
			if got := protocolAllowedForKind(p, kind); got != want {
				t.Errorf("%s allowed for %s: %v want %v", p, kind, got, want)
			}
		}
	}
	if len(knownProtocols) != 17 {
		t.Errorf("known protocols: %d", len(knownProtocols))
	}
	if isImplementedProtocol(ProtoOpenAIVideoGen) || !isImplementedProtocol(ProtoXAIVideo) || isImplementedProtocol("") || isImplementedProtocol("nope") {
		t.Error("implemented protocols")
	}
	// Supported combinations.
	for _, c := range []struct {
		set  []string
		want bool
	}{
		{nil, true}, {[]string{"openai_responses"}, true}, {[]string{"openai_responses", "openai_responses"}, true},
		{[]string{"openai_image_generations", "openai_image_edits"}, true}, {[]string{"openai_image_edits", "openai_image_generations"}, true},
		{[]string{"xai_image", "xai_image_edits"}, true}, {[]string{"xai_video", "xai_video_extensions"}, true},
		{[]string{"xai_video", "xai_image"}, false}, {[]string{"openai_chat_completions", "openai_responses"}, false},
		{[]string{"openai_image_generations", "openai_image_edits", "xai_image"}, false},
	} {
		if got := supportedCombination(c.set); got != c.want {
			t.Errorf("%v: %v want %v", c.set, got, c.want)
		}
	}
}

func TestProtocolResolutionRules(t *testing.T) {
	none := protocolDefaults{}
	// Explicit protocols.
	if p, err := resolveProtocol("openai", none, []string{"chat"}, " OpenAI_Responses "); err != nil || p != ProtoOpenAIResponses {
		t.Errorf("explicit: %q %v", p, err)
	}
	if _, err := resolveProtocol("openai", none, []string{"chat"}, "bogus"); !errors.Is(err, ErrInvalidAdapter) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := resolveProtocol("openai", none, []string{"chat"}, ProtoXAIVideo); !errors.Is(err, ErrInvalidAdapter) {
		t.Errorf("not allowed for kinds: %v", err)
	}
	if p, err := resolveProtocol("openai", none, nil, ProtoXAIVideo); err != nil || p != ProtoXAIVideo {
		t.Errorf("empty kinds accept any known protocol: %q %v", p, err)
	}
	// Inferred protocols.
	if _, err := resolveProtocol("openai", none, nil, ""); !errors.Is(err, ErrProtocolRequired) {
		t.Errorf("no kinds: %v", err)
	}
	table := []struct {
		style string
		kinds []string
		want  string
	}{
		{StyleOpenAI, []string{"chat"}, ProtoOpenAIChat},
		{StyleOpenAI, []string{"audio"}, ProtoOpenAIChat},
		{StyleOpenAI, []string{"image_gen"}, ProtoOpenAIImageGen},
		{StyleOpenAI, []string{"image_edit"}, ProtoOpenAIImageEdit},
		{StyleOpenAI, []string{"video_gen"}, ProtoOpenAIVideoGen},
		{StyleAnthropic, []string{"chat"}, ProtoAnthropicMessages},
		{StyleGoogle, []string{"chat"}, ProtoGoogleGenerate},
		{StyleGoogle, []string{"image_gen"}, ProtoGoogleImage},
		{StyleGoogle, []string{"image_edit"}, ProtoGoogleImage},
		{StyleGoogle, []string{"video_gen"}, ProtoGeminiInteractions},
		{StyleXAI, []string{"chat"}, ProtoXAIResponses},
		{StyleXAI, []string{"image_gen"}, ProtoXAIImage},
		{StyleXAI, []string{"image_edit"}, ProtoXAIImageEdit},
		{StyleXAI, []string{"video_gen"}, ProtoXAIVideo},
		{StyleXAI, []string{"video_extension"}, ProtoXAIVideoExtension},
		{StyleOpenRouter, []string{"chat"}, ProtoOpenRouterResponses},
		{StyleOpenRouter, []string{"image_gen"}, ProtoOpenRouterImages},
		{StyleOpenRouter, []string{"image_edit"}, ProtoOpenRouterImages},
		{StyleOpenRouter, []string{"video_gen"}, ProtoOpenAIVideoGen},
		{StyleCustom, []string{"chat"}, ProtoOpenAIChat},
		{StyleCustom, []string{"image_gen"}, ProtoOpenAIImageGen},
		{StyleCustom, []string{"image_edit"}, ProtoOpenAIImageEdit},
		// The primary kind (first in canonical order) decides, not the order given.
		{StyleOpenAI, []string{"image_gen", "chat"}, ProtoOpenAIChat},
		// A video default that serves every kind is used for a universal model.
		{StyleGoogle, []string{"chat", "image_gen", "image_edit", "video_gen"}, ProtoGeminiInteractions},
	}
	for _, c := range table {
		if p, err := resolveProtocol(c.style, none, c.kinds, ""); err != nil || p != c.want {
			t.Errorf("%s %v: %q %v want %q", c.style, c.kinds, p, err, c.want)
		}
	}
	for _, c := range []struct {
		style string
		kinds []string
	}{{StyleAnthropic, []string{"image_gen"}}, {StyleAnthropic, []string{"video_gen"}}, {StyleCustom, []string{"video_gen"}}, {"mystery", []string{"chat"}}, {"", []string{"chat"}}, {StyleCustom, []string{"video_extension"}}} {
		if p, err := resolveProtocol(c.style, none, c.kinds, ""); !errors.Is(err, ErrProtocolRequired) {
			t.Errorf("%s %v must need a protocol, got %q %v", c.style, c.kinds, p, err)
		}
	}
	// The upstream's own defaults beat the fallback table.
	defs := protocolDefaults{"chat": ProtoOpenAIResponses}
	if p, _ := resolveProtocol(StyleOpenAI, defs, []string{"chat"}, ""); p != ProtoOpenAIResponses {
		t.Errorf("custom default: %q", p)
	}
	// Protocol sets.
	sets := []struct {
		style string
		kinds []string
		in    []string
		want  string
	}{
		{StyleOpenAI, []string{"image_gen", "image_edit"}, nil, "openai_image_generations,openai_image_edits"},
		{StyleGoogle, []string{"image_gen", "image_edit"}, nil, "google_image_generation"},
		{StyleXAI, []string{"image_gen", "image_edit"}, nil, "xai_image,xai_image_edits"},
		{StyleXAI, []string{"video_gen", "video_extension"}, nil, "xai_video,xai_video_extensions"},
		{StyleOpenAI, []string{"video_gen", "video_extension"}, nil, "openai_video_generations"},
		{StyleOpenRouter, []string{"image_gen", "image_edit"}, nil, "openrouter_images"},
		{StyleOpenAI, []string{"chat"}, []string{" ", ""}, "openai_chat_completions"},
		{StyleOpenAI, []string{"image_gen", "image_edit"}, []string{"openai_image_edits", "openai_image_generations", "openai_image_edits"}, "openai_image_edits,openai_image_generations"},
	}
	for _, c := range sets {
		got, err := resolveProtocolSet(c.style, none, c.kinds, c.in)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%s %v %v: %v %v want %s", c.style, c.kinds, c.in, got, err, c.want)
		}
	}
	if _, err := resolveProtocolSet(StyleAnthropic, none, []string{"image_gen", "image_edit"}, nil); !errors.Is(err, ErrProtocolRequired) {
		t.Errorf("anthropic image set: %v", err)
	}
	if _, err := resolveProtocolSet(StyleOpenAI, none, []string{"chat"}, []string{"openai_chat_completions", "openai_responses"}); !errors.Is(err, ErrInvalidCombination) {
		t.Errorf("two chat protocols: %v", err)
	}
	// Suggested protocols never fail with "protocol required".
	if got, err := suggestedProtocols(StyleAnthropic, none, []string{"image_gen"}); err != nil || len(got) != 0 {
		t.Errorf("suggested: %v %v", got, err)
	}
	if got, err := suggestedProtocols(StyleOpenAI, none, []string{"chat"}); err != nil || len(got) != 1 {
		t.Errorf("suggested chat: %v %v", got, err)
	}
	if got := fallbackDefaultsJSON("anthropic"); got != `{"audio":"anthropic_messages","chat":"anthropic_messages"}` {
		t.Errorf("fallback json: %s", got)
	}
	if got := fallbackDefaultsJSON("mystery"); got != "{}" {
		t.Errorf("unknown style: %s", got)
	}
}

func TestProtocolEndpointKinds(t *testing.T) {
	for p, want := range map[string]string{
		ProtoOpenAIChat: "chat_completions", ProtoOpenRouterChat: "chat_completions", ProtoOpenAIResponses: "responses", ProtoXAIResponses: "responses",
		ProtoOpenRouterResponses: "responses", ProtoOpenAIImageGen: "image_generations", ProtoXAIImage: "image_generations", ProtoGoogleImage: "image_generations",
		ProtoOpenAIImageEdit: "image_edits", ProtoXAIImageEdit: "image_edits", ProtoOpenRouterImages: "images", ProtoOpenAIVideoGen: "video_generations",
		ProtoXAIVideo: "video_generations", ProtoXAIVideoExtension: "video_extensions", ProtoGeminiInteractions: "interactions",
	} {
		if got := protocolEndpoint(p); got != want {
			t.Errorf("%s: %q want %q", p, got, want)
		}
	}
}

func TestKeyDocument(t *testing.T) {
	doc, err := parseAndValidateKeys(`{"strategy":"round_robin","keys":[{"key":"a-key-12345","note":"n"},{"key":"b","status":"off"}],"custom":{"x":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	if doc.normalizedStrategy() != "round_robin" || len(doc.activeKeys()) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	out, _ := doc.marshal()
	if !strings.Contains(out, `"custom":{"x":1}`) {
		t.Errorf("unrelated fields must be preserved: %s", out)
	}
	for _, bad := range []string{`{`, `[]`, `{"keys":[]}`, `{"keys":[{"key":" "}]}`, `{"keys":[{"key":"k","status":"disabled"}]}`, `{"keys":"x"}`, `{"strategy":5,"keys":[{"key":"k"}]}`} {
		if _, err := parseAndValidateKeys(bad); !errors.Is(err, ErrInvalidAPIKeys) {
			t.Errorf("%s must be refused: %v", bad, err)
		}
	}
	for in, want := range map[string]string{"failover": "failover", "round_robin": "round_robin", "": "random", "whatever": "random", " failover ": "failover"} {
		if got := (&keyDoc{Strategy: in}).normalizedStrategy(); got != want {
			t.Errorf("strategy %q: %s", in, got)
		}
	}
	for in, want := range map[string]string{"": "****", "short": "****", "12345678": "****", "123456789": "1234****6789", " sk-abcdefghijk ": "sk-a****hijk"} {
		if got := maskKey(in); got != want {
			t.Errorf("mask %q: %q want %q", in, got, want)
		}
	}
	// Selection helpers.
	d := &keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "off", Status: "disabled"}, {Key: "one"}, {Key: "two"}}}
	if k, ok := d.pickKey(func() int64 { return 5 }); !ok || k != "one" {
		t.Errorf("failover pick: %q", k)
	}
	d.Strategy = "round_robin"
	if k, _ := d.pickKey(func() int64 { return 5 }); k != "two" {
		t.Errorf("round robin pick: %q", k)
	}
	if k, _ := d.pickKey(func() int64 { return 6 }); k != "one" {
		t.Errorf("round robin wraps: %q", k)
	}
	if _, ok := (&keyDoc{Keys: []keyEntry{{Key: "x", Status: "disabled"}}}).pickKey(func() int64 { return 0 }); ok {
		t.Error("no active key")
	}
}

func TestWebPHeaderParsing(t *testing.T) {
	// A real 1x1 lossless image, then an extended container around it.
	vp8l, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	info, err := inspectIcon(vp8l)
	if err != nil || info.contentType != "image/webp" || info.width != 1 || info.height != 1 || info.ext != ".webp" {
		t.Fatalf("plain webp: %+v %v", info, err)
	}
	chunk := func(tag string, payload []byte) []byte {
		out := append([]byte(tag), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(out[4:], uint32(len(payload)))
		out = append(out, payload...)
		if len(payload)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	riff := func(parts ...[]byte) []byte {
		body := []byte("WEBP")
		for _, p := range parts {
			body = append(body, p...)
		}
		out := append([]byte("RIFF"), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
		return append(out, body...)
	}
	vp8x := func(flags byte, w, h int) []byte {
		p := make([]byte, 10)
		p[0] = flags
		wm, hm := w-1, h-1
		p[4], p[5], p[6] = byte(wm), byte(wm>>8), byte(wm>>16)
		p[7], p[8], p[9] = byte(hm), byte(hm>>8), byte(hm>>16)
		return chunk("VP8X", p)
	}
	img := vp8l[20:33] // the 13-byte VP8L payload
	ext := riff(vp8x(0, 1, 1), chunk("VP8L", img))
	if info, err := inspectIcon(ext); err != nil || info.width != 1 || info.height != 1 {
		t.Fatalf("extended webp: %+v %v", info, err)
	}
	// A canvas that disagrees with the still image, or a header with no image
	// data, does not decode.
	if _, err := inspectIcon(riff(vp8x(0, 300, 200), chunk("VP8L", img))); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("canvas mismatch: %v", err)
	}
	if _, err := inspectIcon(riff(vp8x(0, 100, 50), chunk("EXIF", make([]byte, 8)))); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("header only: %v", err)
	}
	// Dimension limits apply to the canvas.
	if _, err := inspectIcon(riff(vp8x(0x02, 3000, 10), chunk("ANMF", make([]byte, 16)))); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("webp dimensions: %v", err)
	}
	if info, err := inspectIcon(riff(vp8x(0x02, 2048, 10), chunk("ANMF", make([]byte, 16)))); err != nil || info.width != 2048 {
		t.Errorf("animated webp canvas: %+v %v", info, err)
	}
	// Truncated, oversized RIFF length and trailing garbage inside the length.
	trunc := append([]byte(nil), ext...)
	binary.LittleEndian.PutUint32(trunc[4:], uint32(len(ext)+100))
	if _, err := inspectIcon(trunc); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("truncated webp: %v", err)
	}
	if _, err := inspectIcon(ext[:len(ext)-4]); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("cut webp: %v", err)
	}
	two := riff(chunk("VP8L", img), chunk("VP8L", img))
	if _, err := inspectIcon(two); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("two bitstreams: %v", err)
	}
	if _, err := inspectIcon(nil); !errors.Is(err, ErrIconFileInvalid) {
		t.Errorf("empty: %v", err)
	}
	if _, err := inspectIcon(make([]byte, maxIconBytes+1)); !errors.Is(err, ErrIconFileTooLarge) {
		t.Errorf("big: %v", err)
	}
}
