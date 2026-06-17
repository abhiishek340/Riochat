package channel

import (
	"regexp"
	"strings"
)

// vendorAliases maps raw values (vendor keys submitted by an administrator,
// remote owner names) to canonical vendor keys.
var vendorAliases = map[string]string{
	"openai": "openai", "anthropic": "anthropic", "claude": "anthropic",
	"google": "google", "gemini": "google", "gemma": "google",
	"meta": "meta", "llama": "meta", "microsoft": "microsoft", "phi": "microsoft",
	"amazon": "amazon", "aws": "amazon", "bedrock": "amazon", "nova": "amazon", "titan": "amazon",
	"nvidia": "nvidia", "nemotron": "nvidia", "xai": "xai", "grok": "xai", "deepseek": "deepseek",
	"moonshot": "moonshot", "kimi": "moonshot",
	"alibaba": "alibaba", "qwen": "alibaba", "qwq": "alibaba", "qvq": "alibaba", "tongyi": "alibaba", "wanx": "alibaba",
	"mistral": "mistral", "mixtral": "mistral", "codestral": "mistral", "pixtral": "mistral", "ministral": "mistral", "devstral": "mistral",
	"zhipu": "zhipu", "glm": "zhipu", "chatglm": "zhipu", "bigmodel": "zhipu",
	"minimax": "minimax", "abab": "minimax", "hailuo": "minimax",
	"bytedance": "bytedance", "byte": "bytedance", "volcengine": "bytedance", "doubao": "bytedance", "seed": "bytedance",
	"tencent": "tencent", "hunyuan": "tencent", "longcat": "longcat",
	"xiaomi": "xiaomi", "mimo": "xiaomi", "xiaomimimo": "xiaomi", "xiaomi-mi-mo": "xiaomi",
	"iflytek": "iflytek", "iflytekcloud": "iflytek", "spark": "iflytek",
	"stepfun": "stepfun", "step": "stepfun", "baichuan": "baichuan",
	"baidu": "baidu", "ernie": "baidu", "wenxin": "baidu",
	"openrouter": "openrouter", "copilot": "copilot", "github": "copilot", "unknown": "unknown",
}

// canonicalVendor maps a raw value to a canonical vendor key ("" when it has none).
func canonicalVendor(raw string) string { return vendorAliases[lower(raw)] }

// vendorRule is one detection rule: a vendor and the matcher over an
// examined form of a candidate string.
type vendorRule struct {
	vendor string
	match  func(f string) bool
}

func prefixRule(vendor string, prefixes ...string) vendorRule {
	return vendorRule{vendor, func(f string) bool { return hasAnyPrefix(f, prefixes...) }}
}

// vendorRules is ordered; the first matching rule decides.
var vendorRules = []vendorRule{
	{"anthropic", func(f string) bool { return strings.HasPrefix(f, "claude") || strings.Contains(f, "anthropic/") }},
	{"google", func(f string) bool {
		return hasAnyPrefix(f, "nano-banana", "gemini", "gemma", "imagen", "veo") || strings.Contains(f, "google/")
	}},
	{"meta", func(f string) bool { return strings.HasPrefix(f, "llama") || strings.Contains(f, "meta/") }},
	{"microsoft", func(f string) bool { return strings.HasPrefix(f, "phi") || strings.Contains(f, "microsoft/") }},
	{"amazon", func(f string) bool {
		return hasAnyPrefix(f, "nova", "titan") || containsAny(f, "bedrock", "amazon/", "amazon.", "aws/", "aws.")
	}},
	{"nvidia", func(f string) bool { return strings.HasPrefix(f, "nemotron") || strings.Contains(f, "nvidia/") }},
	{"xai", func(f string) bool { return strings.HasPrefix(f, "grok") || strings.Contains(f, "xai/") }},
	prefixRule("deepseek", "deepseek"),
	{"moonshot", func(f string) bool { return strings.HasPrefix(f, "kimi") || strings.Contains(f, "moonshot") }},
	{"alibaba", func(f string) bool {
		return hasAnyPrefix(f, "qwen", "qwq", "qvq", "wanx") || containsAny(f, "tongyi", "alibaba/")
	}},
	{"mistral", func(f string) bool {
		return hasAnyPrefix(f, "mistral", "mixtral", "ministral", "codestral", "pixtral", "devstral") || strings.Contains(f, "mistral")
	}},
	{"zhipu", func(f string) bool {
		return hasAnyPrefix(f, "glm", "chatglm", "cogview", "cogvideo") || containsAny(f, "bigmodel", "zhipu")
	}},
	prefixRule("minimax", "minimax", "abab", "hailuo"),
	{"bytedance", func(f string) bool {
		return hasAnyPrefix(f, "doubao", "seed") || containsAny(f, "bytedance/", "volcengine/")
	}},
	{"tencent", func(f string) bool { return strings.HasPrefix(f, "hunyuan") || strings.Contains(f, "tencent/") }},
	prefixRule("longcat", "longcat"),
	{"xiaomi", func(f string) bool { return strings.HasPrefix(f, "mimo") || strings.Contains(f, "xiaomi/") }},
	{"iflytek", func(f string) bool { return strings.HasPrefix(f, "spark") || strings.Contains(f, "iflytek") }},
	{"stepfun", func(f string) bool { return strings.HasPrefix(f, "step") || strings.Contains(f, "stepfun") }},
	prefixRule("baichuan", "baichuan"),
	{"baidu", func(f string) bool { return hasAnyPrefix(f, "ernie", "wenxin") || strings.Contains(f, "baidu/") }},
	{"copilot", func(f string) bool { return strings.Contains(f, "copilot") || strings.Contains(f, "github/") }},
	{"openai", func(f string) bool {
		return hasAnyPrefix(f, "gpt-", "chatgpt", "o1", "o3", "o4", "codex", "dall-e", "sora") || strings.Contains(f, "openai/")
	}},
}

// examinedForms returns the forms of a candidate that detection looks at: the
// whole lower-cased text, the text after the first "/" and the text after the
// last "/".
func examinedForms(candidate string) []string {
	c := lower(candidate)
	if c == "" {
		return nil
	}
	forms := []string{c}
	if i := strings.Index(c, "/"); i >= 0 {
		forms = append(forms, c[i+1:])
	}
	if i := strings.LastIndex(c, "/"); i >= 0 {
		forms = append(forms, c[i+1:])
	}
	return forms
}

// detectVendor finds the vendor of the first candidate that matches a rule. It
// returns "" when nothing matches.
func detectVendor(candidates ...string) string {
	for _, cand := range candidates {
		forms := examinedForms(cand)
		for _, rule := range vendorRules {
			for _, f := range forms {
				if rule.match(f) {
					return rule.vendor
				}
			}
		}
	}
	for _, cand := range candidates {
		c := lower(cand)
		if strings.HasPrefix(c, "openrouter/") || strings.HasPrefix(c, "openrouter-") {
			return "openrouter"
		}
	}
	return ""
}

// deriveVendor returns the canonical key of raw when it has one, else the
// vendor detected from the names, else "unknown".
func deriveVendor(raw string, names ...string) string {
	if v := canonicalVendor(raw); v != "" {
		return v
	}
	if v := detectVendor(names...); v != "" {
		return v
	}
	return "unknown"
}

var vendorKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// normalizeVendorKey lower-cases and trims a submitted vendor key, maps known
// aliases and otherwise requires the key syntax.
func normalizeVendorKey(raw string) (string, error) {
	k := lower(raw)
	if v := vendorAliases[k]; v != "" {
		return v, nil
	}
	if !vendorKeyRe.MatchString(k) {
		return "", ErrInvalidVendor
	}
	return k, nil
}

// vendorIconByKey is the icon used for a vendor when no name-prefix icon applies.
func vendorIconByKey(vendor string) string {
	switch vendor {
	case "anthropic":
		return "claude"
	case "google":
		return "gemini"
	case "amazon":
		return "aws"
	case "xai":
		return "grok"
	case "xiaomi":
		return "xiaomimimo"
	case "iflytek":
		return "iflytekcloud"
	case "meta", "microsoft", "nvidia", "deepseek", "moonshot", "alibaba", "mistral", "zhipu", "minimax", "bytedance",
		"tencent", "longcat", "stepfun", "baichuan", "baidu", "openrouter", "copilot", "openai":
		return vendor
	}
	return ""
}

type iconRule struct {
	icon  string
	match func(f string) bool
}

var iconRules = []iconRule{
	{"claude", func(f string) bool { return strings.HasPrefix(f, "claude") }},
	{"nanobanana", func(f string) bool { return strings.HasPrefix(f, "nano-banana") || isGeminiImageName(f) }},
	{"gemma", func(f string) bool { return strings.HasPrefix(f, "gemma") }},
	{"gemini", func(f string) bool { return hasAnyPrefix(f, "gemini", "imagen", "veo") }},
	{"meta", func(f string) bool { return strings.HasPrefix(f, "llama") }},
	{"microsoft", func(f string) bool { return strings.HasPrefix(f, "phi") }},
	{"nova", func(f string) bool { return strings.HasPrefix(f, "nova") }},
	{"bedrock", func(f string) bool { return strings.HasPrefix(f, "titan") || strings.Contains(f, "bedrock") }},
	{"nvidia", func(f string) bool { return strings.HasPrefix(f, "nemotron") }},
	{"grok", func(f string) bool { return strings.HasPrefix(f, "grok") }},
	{"deepseek", func(f string) bool { return strings.HasPrefix(f, "deepseek") }},
	{"kimi", func(f string) bool { return hasAnyPrefix(f, "kimi", "moonshot") }},
	{"qwen", func(f string) bool { return hasAnyPrefix(f, "qwen", "qwq", "qvq", "wanx") }},
	{"mistral", func(f string) bool {
		return hasAnyPrefix(f, "mistral", "mixtral", "ministral", "codestral", "pixtral", "devstral")
	}},
	{"chatglm", func(f string) bool { return hasAnyPrefix(f, "glm", "chatglm") }},
	{"cogview", func(f string) bool { return strings.HasPrefix(f, "cogview") }},
	{"minimax", func(f string) bool { return hasAnyPrefix(f, "minimax", "abab") }},
	{"hailuo", func(f string) bool { return strings.HasPrefix(f, "hailuo") }},
	{"doubao", func(f string) bool { return hasAnyPrefix(f, "doubao", "seed") }},
	{"hunyuan", func(f string) bool { return strings.HasPrefix(f, "hunyuan") }},
	{"longcat", func(f string) bool { return strings.HasPrefix(f, "longcat") }},
	{"xiaomimimo", func(f string) bool { return strings.HasPrefix(f, "mimo") }},
	{"spark", func(f string) bool { return strings.HasPrefix(f, "spark") }},
	{"stepfun", func(f string) bool { return strings.HasPrefix(f, "step") }},
	{"baichuan", func(f string) bool { return strings.HasPrefix(f, "baichuan") }},
	{"wenxin", func(f string) bool { return hasAnyPrefix(f, "ernie", "wenxin") }},
	{"dalle", func(f string) bool { return strings.HasPrefix(f, "dall-e") }},
	{"sora", func(f string) bool { return strings.HasPrefix(f, "sora") }},
	{"openai", func(f string) bool { return hasAnyPrefix(f, "gpt-", "chatgpt", "o1", "o3", "o4", "codex") }},
}

// autoIcon derives the icon of a model: an explicit icon wins, then a name
// prefix, then the icon of the vendor.
func autoIcon(explicit, name, vendor string) string {
	if e := strings.TrimSpace(explicit); e != "" {
		return e
	}
	for _, f := range examinedForms(name) {
		for _, r := range iconRules {
			if r.match(f) {
				return r.icon
			}
		}
	}
	return vendorIconByKey(lower(vendor))
}
