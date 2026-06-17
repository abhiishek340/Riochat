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
