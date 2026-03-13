package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// responsesAdapter serves openai_responses, openrouter_responses and
// xai_responses.
type responsesAdapter struct{}

var cacheBreakpointTypes = map[string]bool{
	"input_text": true, "input_image": true, "input_file": true, "text": true,
	"image_url": true, "input_audio": true, "file": true, "refusal": true,
}

// explicitCache reports whether explicit prompt caching is requested and
// honored: mode "explicit" and an optional ttl of exactly "30m".
func explicitCache(o options) bool {
	po, ok := o["prompt_cache_options"].(map[string]any)
	if !ok {
		return false
	}
	if m, _ := po["mode"].(string); strings.ToLower(strings.TrimSpace(m)) != "explicit" {
		return false
	}
	if v, ok := po["ttl"]; ok && v != nil {
		if s, _ := v.(string); strings.TrimSpace(s) != "30m" {
			return false
		}
	}
	return true
}

func nativeToolType(t map[string]any) string {
	s, _ := t["type"].(string)
	return s
}

func (responsesAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	req := x.req
	proto := x.proto
	official := proto == OpenAIResponses
	openrouter := proto == OpenRouterResponses
	xai := proto == XAIResponses

	if x.model == "" {
		return nil, invalid(ErrInvalidRequest, "model is required")
	}
	native, err := o.nativeTools()
	if err != nil {
		return nil, err
	}
	so, err := o.requireObject("stream_options")
	if err != nil {
		return nil, err
	}
	reasoningOpt, err := o.requireObject("reasoning")
	if err != nil {
		return nil, err
	}
	textOpt, err := o.requireObject("text")
	if err != nil {
		return nil, err
	}
	rf, err := o.responseFormat()
	if err != nil {
		return nil, err
	}

	explicit := explicitCache(o) && !req.Ephemeral && (official)
	msgs := prepareOpenAIMessages(req.Messages, req.Instructions, !official, explicit, false)
	input := make([]any, 0, len(msgs))
	for i, m := range msgs {
		input = append(input, renderResponsesMessage(m, i, openrouter, explicit)...)
	}
	if openrouter {
		for _, it := range input {
			if item, ok := it.(map[string]any); ok && item["type"] == nil && item["role"] != nil {
				item["type"] = "message"
			}
		}
	}
	if len(input) == 0 {
		input = append(input, map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": ""}}})
	}

	body := map[string]any{"model": x.model, "input": input}
	if x.stream {
		body["stream"] = true
	}
	if official && strings.TrimSpace(req.Instructions) != "" {
		body["instructions"] = req.Instructions
	}
	if n, ok := o.int("max_output_tokens", "max_completion_tokens", "max_tokens"); ok && n > 0 {
		body["max_output_tokens"] = n
	}
	if f, ok := o.float("temperature"); ok {
		body["temperature"] = f
	}
	if f, ok := o.float("top_p"); ok {
		body["top_p"] = f
	}

	reasoning := map[string]any{}
	for k, v := range reasoningOpt {
		reasoning[k] = cloneJSON(v)
	}
	if s, ok := o.str("reasoning_effort"); ok {
		reasoning["effort"] = s
	}
	if s, ok := o.str("reasoning_summary"); ok {
		reasoning["summary"] = s
	}
	if len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}

	text := map[string]any{}
	for k, v := range textOpt {
		text[k] = cloneJSON(v)
	}
	if rf != nil {
		text["format"] = responsesFormat(rf)
	}
	if official {
		if s, ok := o.str("verbosity"); ok {
			text["verbosity"] = s
		}
	}
	if len(text) > 0 {
		body["text"] = text
	}

	switch {
	case req.Ephemeral:
		if !openrouter {
			body["store"] = false
		}
	case official && req.Background:
		body["background"] = true
		body["store"] = true
	}
	if official && !req.Ephemeral && strings.TrimSpace(req.PreviousResponseID) != "" {
		body["previous_response_id"] = strings.TrimSpace(req.PreviousResponseID)
	}
	if official && !req.Ephemeral && strings.TrimSpace(req.PromptCacheKey) != "" {
		body["prompt_cache_key"] = req.PromptCacheKey
	}
	if x.stream && so != nil {
		if v, ok := so["include_obfuscation"]; ok {
			body["stream_options"] = map[string]any{"include_obfuscation": v}
		}
	}

	// Tools: native first, then web search, then functions.
	var tools []any
	for _, t := range native {
		tools = append(tools, cloneJSON(t))
	}
	if ws, _ := o.boolean("web_search"); ws && !hasNativeType(native, "web_search", "web_search_preview") {
		tools = append(tools, map[string]any{"type": "web_search"})
	}
	if !req.DisableTools {
		for _, td := range req.Tools {
			t := map[string]any{"type": "function", "name": td.Name, "parameters": toolSchema(td.Schema)}
			if td.Description != "" {
				t["description"] = td.Description
			}
			tools = append(tools, t)
		}
	} else {
		tools = nil
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	// include
	if official || xai {
		inc := []string{"reasoning.encrypted_content"}
		if toolsHaveWebSearch(tools) {
			inc = append(inc, "web_search_call.action.sources")
		}
		if xai {
			inc = append(inc, "file_search_call.results", "code_interpreter_call.outputs")
		}
		inc = uniqueAppend(inc, o.stringList("include")...)
		body["include"] = inc
	} else if l := o.stringList("include"); len(l) > 0 {
		body["include"] = uniqueAppend(nil, l...)
	}

	consumed := []string{"max_output_tokens", "max_completion_tokens", "max_tokens", "temperature", "top_p",
		"reasoning", "reasoning_effort", "reasoning_summary", "text", "response_format", "verbosity"}
	protected := []string{"background"}
	if xai {
		protected = append(protected, "metadata")
	}
	if !official {
		protected = append(protected, "prompt_cache_key")
	}
	if req.Ephemeral {
		protected = append(protected, "store", "prompt_cache_key")
	}
	copyExtras(body, o, consumed, protected...)

	endpoint, err := x.apiURL("v1", "/responses")
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, endpoint, body)
}

func jsonCall(method, url string, body map[string]any) (*call, error) {
	b, err := marshalBody(body)
	if err != nil {
		return nil, err
	}
	return &call{method: method, url: url, body: b}, nil
}

func marshalBody(body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, invalid(ErrInvalidRequest, "request body is not serializable: %v", err)
	}
	return b, nil
}

func hasNativeType(tools []map[string]any, types ...string) bool {
	for _, t := range tools {
		tt := nativeToolType(t)
		for _, w := range types {
			if tt == w {
				return true
			}
		}
	}
	return false
}

func toolsHaveWebSearch(tools []any) bool {
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok && strings.HasPrefix(nativeToolType(m), "web_search") {
			return true
		}
	}
	return false
}

func responsesFormat(rf *responseFormat) map[string]any {
	switch rf.Type {
	case "json_schema":
		name := rf.Name
		if name == "" {
			name = "response"
		}
		f := map[string]any{"type": "json_schema", "name": name}
		if rf.Schema != nil {
			f["schema"] = cloneJSON(rf.Schema)
		}
		if rf.Strict != nil {
			f["strict"] = *rf.Strict
		}
		return f
	case "json_object", "text":
		return map[string]any{"type": rf.Type}
	}
	f := map[string]any{}
	for k, v := range rf.Raw {
		if k != "json_schema" {
			f[k] = cloneJSON(v)
		}
	}
	return f
}
