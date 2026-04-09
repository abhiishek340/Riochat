package settings

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// modelOptionProtocols are the protocol keys a model-option document may use.
var modelOptionProtocols = []string{
	"default", "openai_chat_completions", "openrouter_chat_completions", "openrouter_responses",
	"openrouter_images", "openai_image_generations", "openai_image_edits", "openai_responses",
	"anthropic_messages", "xai_responses", "xai_image", "xai_image_edits", "xai_video",
	"xai_video_extensions", "gemini_generate_content", "google_image_generation", "gemini_interactions",
}

func isModelOptionProtocol(p string) bool {
	for _, x := range modelOptionProtocols {
		if x == p {
			return true
		}
	}
	return false
}

// optionDoc maps a protocol key to the passthrough parameter paths.
type optionDoc map[string][]string

func prefixed(prefix string, names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// builtinAllowedDoc returns the current default allow document.
func builtinAllowedDoc() optionDoc {
	return optionDoc{
		"default":                     {"temperature", "top_p", "max_tokens", "max_output_tokens", "max_completion_tokens", "stop", "response_format.type"},
		"openai_chat_completions":     {"service_tier", "presence_penalty", "frequency_penalty", "reasoning_effort", "verbosity", "thinking.type", "stream_options.include_usage"},
		"openai_responses":            {"service_tier", "reasoning.effort", "reasoning.summary", "text.verbosity"},
		"openai_image_generations":    {"background", "moderation", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "style", "user"},
		"openai_image_edits":          {"background", "input_fidelity", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "user"},
		"anthropic_messages":          {"speed", "top_k", "thinking.type", "thinking.budget_tokens"},
		"gemini_generate_content":     prefixed("generationConfig.", "temperature", "topP", "maxOutputTokens", "responseMimeType", "thinkingConfig.includeThoughts", "thinkingConfig.thinkingLevel"),
		"google_image_generation":     {"generationConfig.responseModalities", "generationConfig.imageConfig.aspectRatio", "generationConfig.imageConfig.imageSize"},
		"gemini_interactions":         append(prefixed("generation_config.", "temperature", "top_p", "max_output_tokens", "thinking_level", "thinking_summaries"), append(prefixed("response_format.", "type", "aspect_ratio", "image_size", "mime_type", "schema"), "generation_config.video_config.task")...),
		"xai_responses":               {"reasoning.effort", "min_p", "parallel_tool_calls", "store", "top_k"},
		"xai_image":                   {"aspect_ratio", "n", "resolution", "response_format"},
		"xai_image_edits":             {"aspect_ratio", "n", "resolution", "response_format"},
		"xai_video":                   {"aspect_ratio", "duration", "resolution"},
		"xai_video_extensions":        {"duration"},
		"openrouter_chat_completions": {"presence_penalty", "frequency_penalty", "reasoning_effort", "reasoning.effort", "reasoning.summary", "verbosity", "thinking.type", "stream_options.include_usage"},
		"openrouter_responses":        {"reasoning.effort", "reasoning.summary"},
		"openrouter_images":           {"aspect_ratio", "background", "n", "output_compression", "output_format", "provider.allow_fallbacks", "provider.ignore", "provider.only", "provider.order", "provider.sort", "quality", "resolution", "seed", "size", "user"},
	}
}

// builtinDeniedDoc returns the default deny document.
func builtinDeniedDoc() optionDoc {
	return optionDoc{"default": {"model", "messages", "input", "instructions", "prompt", "system", "systemInstruction", "headers", "api_key", "apiKey", "base_url", "baseURL", "stream", "previous_response_id"}}
}

func (d optionDoc) clone() optionDoc {
	out := make(optionDoc, len(d))
	for k, v := range d {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// marshalDoc renders a document as compact JSON with sorted protocol keys.
func marshalDoc(d optionDoc) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string][]string(d))
	return strings.TrimSpace(buf.String())
}

var (
	defaultAllowedPathsJSON = marshalDoc(builtinAllowedDoc())
	defaultDeniedPathsJSON  = marshalDoc(builtinDeniedDoc())
)

// docSignature is an order-insensitive fingerprint of a document: protocol keys
// are trimmed and each path list is treated as a set.
func docSignature(d map[string][]string) string {
	keys := make([]string, 0, len(d))
	norm := map[string][]string{}
	for k, paths := range d {
		k = strings.TrimSpace(k)
		set := map[string]bool{}
		for _, p := range paths {
			set[p] = true
		}
		list := make([]string, 0, len(set))
		for p := range set {
			list = append(list, p)
		}
		sort.Strings(list)
		if _, seen := norm[k]; !seen {
			keys = append(keys, k)
		}
		norm[k] = list
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strings.Join(norm[k], "\x1f"))
		b.WriteByte('\x1e')
	}
	return b.String()
}

// historicalAllowedSignatures fingerprints the earlier default allow documents.
// An earlier default is the current default minus entries that were added
// later: the xAI video protocol keys, the Gemini thinking paths, the Gemini
// interactions thinking summaries, and with the older camelCase naming of the
// interactions response format; independently, some older generations carried
// only reasoning.effort for xai_responses.
func historicalAllowedSignatures() map[string]bool {
	out := map[string]bool{}
	current := docSignature(builtinAllowedDoc())
	for stage := 0; stage <= 3; stage++ {
		for video := 0; video <= 2; video++ {
			for effortOnly := 0; effortOnly <= 1; effortOnly++ {
				d := builtinAllowedDoc()
				if stage >= 1 {
					d["gemini_generate_content"] = without(d["gemini_generate_content"],
						"generationConfig.thinkingConfig.includeThoughts", "generationConfig.thinkingConfig.thinkingLevel")
				}
				if stage >= 2 {
					d["gemini_interactions"] = without(d["gemini_interactions"], "generation_config.thinking_summaries")
				}
				if stage >= 3 {
					paths := d["gemini_interactions"]
					for i, p := range paths {
						if strings.HasPrefix(p, "response_format.") {
							paths[i] = "responseFormat." + strings.TrimPrefix(p, "response_format.")
						}
					}
				}
				if video >= 1 {
					delete(d, "xai_video")
				}
				if video >= 2 {
					delete(d, "xai_video_extensions")
				}
				if effortOnly == 1 {
					d["xai_responses"] = []string{"reasoning.effort"}
				}
				if sig := docSignature(d); sig != current {
					out[sig] = true
				}
			}
		}
	}
	return out
}

func without(list []string, drop ...string) []string {
	var out []string
	for _, p := range list {
		keep := true
		for _, d := range drop {
			if p == d {
				keep = false
			}
		}
		if keep {
			out = append(out, p)
		}
	}
	return out
}

// isHistoricalAllowedDoc reports whether stored is an unmodified earlier
// default allow document. Unparseable or customized documents are not.
func isHistoricalAllowedDoc(stored string) bool {
	var d map[string][]string
	if err := json.Unmarshal([]byte(stored), &d); err != nil || d == nil {
		return false
	}
	return historicalSigs[docSignature(d)]
}

var historicalSigs = historicalAllowedSignatures()

// modelOptionDocCheck validates an allow or deny document.
func modelOptionDocCheck() check {
	return func(v string) *violation {
		if strings.TrimSpace(v) == "" {
			return bad("required", "")
		}
		if utf8.RuneCountInString(v) > 20000 {
			return bad("max_length", "20000")
		}
		var doc map[string][]string
		if err := json.Unmarshal([]byte(v), &doc); err != nil || doc == nil {
			return bad("json_object", "")
		}
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !isModelOptionProtocol(strings.TrimSpace(k)) {
				return bad("model_option_protocol", "")
			}
		}
		for _, k := range keys {
			for _, p := range doc[k] {
				if f := optionPathProblem(p); f != "" {
					return bad("model_option_path", f)
				}
			}
		}
		return nil
	}
}

// optionPathProblem classifies an invalid parameter path; "" means valid.
func optionPathProblem(p string) string {
	if p == "" {
		return "empty"
	}
	if strings.IndexFunc(p, unicode.IsSpace) >= 0 {
		return "whitespace"
	}
	for _, seg := range strings.Split(p, ".") {
		if seg == "" {
			return "segments"
		}
	}
	for _, r := range p {
		switch {
		case r == '.', r == '_', r == '-', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return "characters"
		}
	}
	return ""
}
