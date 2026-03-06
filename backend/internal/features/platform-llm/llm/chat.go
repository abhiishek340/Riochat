package llm

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// chatAdapter serves openai_chat_completions and openrouter_chat_completions.
type chatAdapter struct{}

var chatBreakpointTypes = map[string]bool{"text": true, "image_url": true, "input_audio": true, "file": true}

func audioFormat(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.Index(m, "/"); i >= 0 {
		m = m[i+1:]
	} else {
		return "wav"
	}
	if i := strings.Index(m, ";"); i >= 0 {
		m = m[:i]
	}
	m = strings.TrimPrefix(m, "x-")
	switch m {
	case "mpeg", "mpeg3", "mp3":
		return "mp3"
	case "":
		return "wav"
	}
	return m
}

func (chatAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	req := x.req
	openrouter := x.proto == OpenRouterChat
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
	rf, err := o.responseFormat()
	if err != nil {
		return nil, err
	}
	explicit := explicitCache(o) && !req.Ephemeral && !openrouter

	msgs := prepareOpenAIMessages(req.Messages, req.Instructions, true, explicit, true)
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, renderChatMessage(m, openrouter, explicit)...)
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"role": "user", "content": ""})
	}

	body := map[string]any{"model": x.model, "messages": out}
	if x.stream {
		body["stream"] = true
		opts := map[string]any{"include_usage": x.includeUsage}
		for k, v := range so {
			opts[k] = cloneJSON(v)
		}
		body["stream_options"] = opts
	}
	if n, ok := o.int("max_completion_tokens", "max_output_tokens", "max_tokens"); ok && n > 0 {
		body["max_completion_tokens"] = n
	}
	if s, ok := o.str("reasoning_effort"); ok {
		body["reasoning_effort"] = s
	}
	if v, ok := o["thinking"]; ok {
		switch t := v.(type) {
		case bool:
			typ := "disabled"
			if t {
				typ = "enabled"
			}
			body["thinking"] = map[string]any{"type": typ}
		case string:
			if s := strings.TrimSpace(t); s != "" {
				body["thinking"] = map[string]any{"type": s}
			}
		case map[string]any:
			body["thinking"] = cloneJSON(t)
		}
	}
	if b, ok := o.boolean("enable_thinking"); ok {
		body["enable_thinking"] = b
	}
	if n, ok := o.int("thinking_budget"); ok && n >= 0 {
		body["thinking_budget"] = n
	}
	if s, ok := o.str("verbosity"); ok {
		body["verbosity"] = s
	}
	for _, k := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty"} {
		if f, ok := o.float(k); ok {
			body[k] = f
		}
	}
	if n, ok := o.positiveInt("seed"); ok {
		body["seed"] = n
	}
	if v, ok := o["stop"]; ok {
		switch s := v.(type) {
		case nil:
			body["stop"] = nil
		case string:
			if strings.TrimSpace(s) != "" {
				body["stop"] = s
			}
		default:
			if l := o.stringList("stop"); len(l) > 0 {
				body["stop"] = l
			}
		}
	}
	if rf != nil {
		body["response_format"] = chatFormat(rf)
	}
	if !req.Ephemeral && x.proto == OpenAIChatCompletions && strings.TrimSpace(req.PromptCacheKey) != "" {
		body["prompt_cache_key"] = req.PromptCacheKey
	}

	var tools []any
	for _, t := range native {
		tools = append(tools, cloneJSON(t))
	}
	if !req.DisableTools {
		for _, td := range req.Tools {
			fn := map[string]any{"name": td.Name, "parameters": toolSchema(td.Schema)}
			if td.Description != "" {
				fn["description"] = td.Description
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
	} else {
		tools = nil
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if ws, _ := o.boolean("web_search"); ws && !req.DisableTools {
		if openrouter {
			mergeInto(body, "plugins", []any{map[string]any{"id": "web"}})
		} else {
			mergeInto(body, "web_search_options", map[string]any{})
		}
	}

	consumed := []string{"max_completion_tokens", "max_output_tokens", "max_tokens", "reasoning_effort", "thinking",
		"enable_thinking", "thinking_budget", "verbosity", "temperature", "top_p", "frequency_penalty", "presence_penalty",
		"seed", "stop", "response_format"}
	protected := []string{}
	if req.Ephemeral || x.proto != OpenAIChatCompletions {
		protected = append(protected, "prompt_cache_key")
	}
	copyExtras(body, o, consumed, protected...)

	endpoint, err := x.apiURL("v1", "/chat/completions")
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, endpoint, body)
}

func chatFormat(rf *responseFormat) map[string]any {
	switch rf.Type {
	case "json_schema":
		js := map[string]any{}
		if raw, ok := rf.Raw["json_schema"].(map[string]any); ok {
			for k, v := range raw {
				js[k] = cloneJSON(v)
			}
		} else {
			name := rf.Name
			if name == "" {
				name = "response"
			}
			js["name"] = name
			if rf.Schema != nil {
				js["schema"] = cloneJSON(rf.Schema)
			}
			if rf.Strict != nil {
				js["strict"] = *rf.Strict
			}
		}
		return map[string]any{"type": "json_schema", "json_schema": js}
	case "json_object", "text":
		return map[string]any{"type": rf.Type}
	}
	out := map[string]any{}
	for k, v := range rf.Raw {
		out[k] = cloneJSON(v)
	}
	return out
}
