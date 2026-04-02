package llm

import (
	"encoding/json"
	"sort"
	"strings"
)

// PathSet maps a protocol policy key (or "default") to dotted option paths.
type PathSet map[string][]string

// Policy modes.
const (
	// ModeAllowlist keeps only allowed paths (the default).
	ModeAllowlist = "allowlist"
	// ModeDenylist keeps everything except denied paths.
	ModeDenylist = "denylist"
)

// DefaultAllowedPaths are the built-in per-protocol allowed option paths.
var DefaultAllowedPaths = PathSet{
	"default":                     {"temperature", "top_p", "max_tokens", "max_output_tokens", "max_completion_tokens", "stop", "response_format.type"},
	"openai_chat_completions":     {"service_tier", "presence_penalty", "frequency_penalty", "reasoning_effort", "verbosity", "thinking.type", "stream_options.include_usage"},
	"openai_responses":            {"service_tier", "reasoning.effort", "reasoning.summary", "text.verbosity"},
	"openai_image_generations":    {"background", "moderation", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "style", "user"},
	"openai_image_edits":          {"background", "input_fidelity", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "user"},
	"anthropic_messages":          {"speed", "top_k", "thinking.type", "thinking.budget_tokens"},
	"gemini_generate_content":     {"generationConfig.temperature", "generationConfig.topP", "generationConfig.maxOutputTokens", "generationConfig.responseMimeType", "generationConfig.thinkingConfig.includeThoughts", "generationConfig.thinkingConfig.thinkingLevel"},
	"google_image_generation":     {"generationConfig.responseModalities", "generationConfig.imageConfig.aspectRatio", "generationConfig.imageConfig.imageSize"},
	"gemini_interactions":         {"generation_config.temperature", "generation_config.top_p", "generation_config.max_output_tokens", "generation_config.thinking_level", "generation_config.thinking_summaries", "response_format.type", "response_format.aspect_ratio", "response_format.image_size", "response_format.mime_type", "response_format.schema", "generation_config.video_config.task"},
	"xai_responses":               {"reasoning.effort", "min_p", "parallel_tool_calls", "store", "top_k"},
	"xai_image":                   {"aspect_ratio", "n", "resolution", "response_format"},
	"xai_image_edits":             {"aspect_ratio", "n", "resolution", "response_format"},
	"xai_video":                   {"aspect_ratio", "duration", "resolution"},
	"xai_video_extensions":        {"duration"},
	"openrouter_chat_completions": {"presence_penalty", "frequency_penalty", "reasoning_effort", "reasoning.effort", "reasoning.summary", "verbosity", "thinking.type", "stream_options.include_usage"},
	"openrouter_responses":        {"reasoning.effort", "reasoning.summary"},
	"openrouter_images":           {"aspect_ratio", "background", "n", "output_compression", "output_format", "provider.allow_fallbacks", "provider.ignore", "provider.only", "provider.order", "provider.sort", "quality", "resolution", "seed", "size", "user"},
}

// DefaultDeniedPaths are always layered on top, whatever the mode.
var DefaultDeniedPaths = PathSet{
	"default": {"model", "messages", "input", "instructions", "prompt", "system", "systemInstruction", "headers", "api_key", "apiKey", "base_url", "baseURL", "stream", "previous_response_id"},
}

// ParsePathSet reads a JSON document {"<protocol>": ["a.b", ...]}. Empty text
// is an empty set; invalid JSON is an error.
func ParsePathSet(raw string) (PathSet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PathSet{}, nil
	}
	var m map[string][]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	out := PathSet{}
	for k, v := range m {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out, nil
}

func (s PathSet) paths(policyKey string) []string {
	out := append([]string(nil), s["default"]...)
	return append(out, s[policyKey]...)
}

// FilterOptions applies the allow/deny policy to user-supplied options for a
// protocol (aliases accepted). It returns the permitted options and the
// dropped dotted paths, sorted. Denied paths always win; in allowlist mode a
// leaf is kept only when an allowed path equals it or is one of its ancestors;
// in denylist mode everything else is kept.
func FilterOptions(opts map[string]any, protocol, mode string, allowed, denied PathSet) (map[string]any, []string) {
	key := PolicyKeyFor(protocol)
	deny := denied.paths(key)
	allow := allowed.paths(key)
	allowlist := strings.ToLower(strings.TrimSpace(mode)) != ModeDenylist
	var dropped []string
	out := filterMap(opts, "", allow, deny, allowlist, &dropped)
	sort.Strings(dropped)
	return out, dropped
}

// underAny reports whether path equals or descends from an entry of set.
func underAny(set []string, path string) bool {
	for _, p := range set {
		if path == p || strings.HasPrefix(path, p+".") {
			return true
		}
	}
	return false
}

// couldContainAllowed reports whether some allowed path lives below path.
func couldContainAllowed(set []string, path string) bool {
	for _, p := range set {
		if strings.HasPrefix(p, path+".") {
			return true
		}
	}
	return false
}

func filterMap(in map[string]any, prefix string, allow, deny []string, allowlist bool, dropped *[]string) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if underAny(deny, path) {
			*dropped = append(*dropped, path)
			continue
		}
		if allowlist && underAny(allow, path) {
			// Allowed subtree: still honor denied descendants.
			out[k] = filterDeniedOnly(v, path, deny, dropped)
			continue
		}
		if sub, ok := v.(map[string]any); ok && (!allowlist || couldContainAllowed(allow, path)) {
			f := filterMap(sub, path, allow, deny, allowlist, dropped)
			if len(f) > 0 || len(sub) == 0 && !allowlist {
				out[k] = f
			}
			continue
		}
		if allowlist {
			*dropped = append(*dropped, path)
			continue
		}
		out[k] = cloneJSON(v)
	}
	return out
}

func filterDeniedOnly(v any, path string, deny []string, dropped *[]string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return cloneJSON(v)
	}
	out := map[string]any{}
	for k, e := range m {
		p := path + "." + k
		if underAny(deny, p) {
			*dropped = append(*dropped, p)
			continue
		}
		out[k] = filterDeniedOnly(e, p, deny, dropped)
	}
	return out
}
