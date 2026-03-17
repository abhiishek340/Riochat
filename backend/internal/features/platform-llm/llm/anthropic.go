package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type anthropicAdapter struct{}

const (
	anthropicDefaultMaxTokens = 64000
	anthropicMaxBreakpoints   = 4
)

// nativeDefaults maps Anthropic server-tool types to their default names.
var anthropicNativeNames = map[string]string{
	"web_search_20250305":             "web_search",
	"web_search_20260209":             "web_search",
	"web_fetch_20250910":              "web_fetch",
	"web_fetch_20260209":              "web_fetch",
	"code_execution_20250825":         "code_execution",
	"code_execution_20260120":         "code_execution",
	"advisor_20260301":                "advisor",
	"tool_search_tool_regex_20251119": "tool_search_tool_regex",
	"tool_search_tool_bm25_20251119":  "tool_search_tool_bm25",
}

var anthropicServerToolNames = map[string]bool{
	"web_search": true, "web_fetch": true, "code_execution": true, "advisor": true,
	"tool_search_tool_regex": true, "tool_search_tool_bm25": true,
}

type anthropicBuild struct {
	marks   []map[string]any
	cacheOn bool
	cache   map[string]any
}

func (b *anthropicBuild) mark(blk map[string]any) {
	if !b.cacheOn {
		return
	}
	cc := map[string]any{}
	for k, v := range b.cache {
		cc[k] = v
	}
	blk["cache_control"] = cc
	b.marks = append(b.marks, blk)
}

func (anthropicAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	req := x.req
	if x.model == "" {
		return nil, invalid(ErrInvalidRequest, "model is required")
	}
	native, err := o.nativeTools()
	if err != nil {
		return nil, err
	}
	rf, err := o.responseFormat()
	if err != nil {
		return nil, err
	}
	outCfgOpt, err := o.requireObject("output_config")
	if err != nil {
		return nil, err
	}

	b := &anthropicBuild{cache: map[string]any{"type": "ephemeral"}}
	b.cacheOn = !req.Ephemeral
	if v, ok := o.boolean("enable_cache"); ok && !v {
		b.cacheOn = false
	}
	if v, ok := o.boolean("prompt_cache"); ok && !v {
		b.cacheOn = false
	}
	if ttl, ok := o.str("cache_timeout"); ok && (ttl == "5m" || ttl == "1h") {
		b.cache["ttl"] = ttl
	}

	msgs := normalizeMessages(req.Messages)
	var systemBlocks []map[string]any
	if strings.TrimSpace(req.Instructions) != "" {
		systemBlocks = append(systemBlocks, map[string]any{"type": "text", "text": req.Instructions})
	}
	var rendered []any
	for _, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper {
			blks := anthropicBlocks(m, b, false)
			for _, blk := range blks {
				if blk["type"] == "text" {
					systemBlocks = append(systemBlocks, blk)
				}
			}
			continue
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		if isTextOnly(m) && !(b.cacheOn && hasHint(m)) {
			text := plainText(m)
			if strings.TrimSpace(text) == "" {
				continue
			}
			rendered = append(rendered, map[string]any{"role": role, "content": text})
			continue
		}
		blks := anthropicBlocks(m, b, m.Role == RoleAssistant)
		if len(blks) == 0 {
			continue
		}
		arr := make([]any, len(blks))
		for i, blk := range blks {
			arr[i] = blk
		}
		rendered = append(rendered, map[string]any{"role": role, "content": arr})
	}
	if len(rendered) == 0 {
		rendered = append(rendered, map[string]any{"role": "user", "content": " "})
	}

	maxTokens := int64(anthropicDefaultMaxTokens)
	if n, ok := o.int("max_output_tokens"); ok && n > 0 {
		maxTokens = n
	} else if n, ok := o.int("max_tokens"); ok && n > 0 {
		maxTokens = n
	}
	body := map[string]any{"model": x.model, "max_tokens": maxTokens, "messages": rendered}
	if x.stream {
		body["stream"] = true
	}

	if f, ok := o.float("temperature"); ok {
		body["temperature"] = f
	}
	if f, ok := o.float("top_p"); ok {
		body["top_p"] = f
	}
	if n, ok := o.positiveInt("top_k"); ok {
		body["top_k"] = n
	}
	if l := o.stringList("stop", "stop_sequences"); len(l) > 0 {
		body["stop_sequences"] = l
	}
	th, err := anthropicThinking(o, maxTokens)
	if err != nil {
		return nil, err
	}
	if th != nil {
		body["thinking"] = th
	}
	if tc, ok := anthropicToolChoice(o["tool_choice"]); ok {
		body["tool_choice"] = tc
	}
	outCfg := map[string]any{}
	for k, v := range outCfgOpt {
		outCfg[k] = cloneJSON(v)
	}
	if of, ok := o["output_format"].(map[string]any); ok {
		outCfg["format"] = cloneJSON(of)
	} else if rf != nil && rf.Type == "json_schema" && rf.Schema != nil {
		outCfg["format"] = map[string]any{"type": "json_schema", "schema": cloneJSON(rf.Schema)}
	}
	if s, ok := o.str("effort", "reasoning_effort"); ok {
		outCfg["effort"] = s
	}
	if len(outCfg) > 0 {
		body["output_config"] = outCfg
	}
	speed, hasSpeed := o.str("speed")
	if hasSpeed {
		body["speed"] = speed
	}

	// Tools and betas.
	betas := o.stringList("betas", "anthropic_beta", "anthropic-beta")
	var tools []any
	if !req.DisableTools {
		for _, t := range native {
			nt := normalizeAnthropicNative(t)
			tt := nativeToolType(nt)
			switch {
			case strings.HasPrefix(tt, "advisor"):
				betas = uniqueAppend(betas, "advisor-2026-03-01")
			case tt == "mcp_toolset":
				betas = uniqueAppend(betas, "mcp-client-2025-11-20")
			}
			tools = append(tools, nt)
		}
		if ws, _ := o.boolean("web_search"); ws && !nativeHasPrefix(native, "web_search") {
			tools = append(tools, map[string]any{"type": "web_search_20250305", "name": "web_search"})
		}
		for _, td := range req.Tools {
			t := map[string]any{"name": td.Name, "input_schema": toolSchema(td.Schema)}
			if td.Description != "" {
				t["description"] = td.Description
			}
			tools = append(tools, t)
		}
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if hasSpeed && strings.EqualFold(speed, "fast") {
		betas = uniqueAppend(betas, "fast-mode-2026-02-01")
	}

	// System prompt.
	if len(systemBlocks) > 0 {
		marked := false
		for _, blk := range systemBlocks {
			if _, ok := blk["cache_control"]; ok {
				marked = true
			}
		}
		if marked {
			arr := make([]any, len(systemBlocks))
			for i, blk := range systemBlocks {
				arr[i] = blk
			}
			body["system"] = arr
		} else {
			var texts []string
			for _, blk := range systemBlocks {
				if t, _ := blk["text"].(string); strings.TrimSpace(t) != "" {
					texts = append(texts, t)
				}
			}
			if len(texts) > 0 {
				body["system"] = strings.Join(texts, "\n\n")
			}
		}
	}

	// Cache breakpoints: keep only the last four.
	if len(b.marks) > anthropicMaxBreakpoints {
		for _, blk := range b.marks[:len(b.marks)-anthropicMaxBreakpoints] {
			delete(blk, "cache_control")
		}
		b.marks = b.marks[len(b.marks)-anthropicMaxBreakpoints:]
	}
	if len(b.marks) == 0 && b.cacheOn {
		if cc, ok := o.object("cache_control"); ok {
			body["cache_control"] = cloneJSON(cc)
		}
	}

	consumed := []string{"max_output_tokens", "max_tokens", "temperature", "top_p", "top_k", "stop", "stop_sequences",
		"thinking", "enable_thinking", "budget_tokens", "thinking_display", "tool_choice", "output_config",
		"output_format", "effort", "reasoning_effort", "speed", "response_format", "cache_control"}
	copyExtras(body, o, consumed, "max_tokens", "cache_control")

	endpoint, err := x.apiURL("v1", "/messages")
	if err != nil {
		return nil, err
	}
	cl, err := jsonCall(http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	cl.header = http.Header{}
	cl.header.Set("anthropic-version", "2023-06-01")
	if len(betas) > 0 {
		cl.header.Set("anthropic-beta", strings.Join(betas, ","))
	}
	return cl, nil
}
