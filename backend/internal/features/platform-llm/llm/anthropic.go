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

func nativeHasPrefix(tools []map[string]any, prefix string) bool {
	for _, t := range tools {
		if strings.HasPrefix(nativeToolType(t), prefix) {
			return true
		}
	}
	return false
}

func hasHint(m Message) bool {
	if m.CacheHint {
		return true
	}
	for _, p := range effectiveParts(m) {
		if p.CacheHint {
			return true
		}
	}
	return false
}

// anthropicBlocks renders a message's content blocks, tool results first for
// user turns and tool calls last for assistant turns, applying cache hints.
func anthropicBlocks(m Message, b *anthropicBuild, assistant bool) []map[string]any {
	var blocks []map[string]any
	type slot struct {
		blk     map[string]any
		dynamic bool
	}
	var slots []slot
	add := func(blk map[string]any, dynamic bool) {
		blocks = append(blocks, blk)
		slots = append(slots, slot{blk, dynamic})
	}
	if !assistant {
		for _, tr := range m.ToolResults {
			blk := map[string]any{"type": "tool_result", "tool_use_id": tr.CallID, "content": toolResultOutput(tr)}
			if tr.IsError {
				blk["is_error"] = true
			}
			add(blk, false)
		}
	}
	for _, p := range effectiveParts(m) {
		var blk map[string]any
		if t, ok := partAsText(p); ok {
			blk = map[string]any{"type": "text", "text": t}
		} else if assistant {
			continue
		} else {
			switch p.Kind {
			case PartImage:
				if len(p.Data) == 0 {
					continue
				}
				blk = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": imageMIME(p.MIME), "data": b64(p.Data)}}
			case PartDocument:
				if len(p.Data) == 0 {
					continue
				}
				blk = map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": b64(p.Data)}}
				if strings.TrimSpace(p.FileName) != "" {
					blk["title"] = p.FileName
				}
			default:
				continue
			}
		}
		add(blk, p.Dynamic)
		if p.CacheHint && !p.Dynamic {
			b.mark(blk)
		}
	}
	if assistant {
		for _, tc := range m.ToolCalls {
			add(map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": parseArgsObject(tc.Arguments)}, false)
		}
	}
	if m.CacheHint {
		for i := len(slots) - 1; i >= 0; i-- {
			if !slots[i].dynamic {
				if _, done := slots[i].blk["cache_control"]; !done {
					b.mark(slots[i].blk)
				}
				break
			}
		}
	}
	return blocks
}

func normalizeAnthropicNative(t map[string]any) map[string]any {
	nt := cloneJSON(t).(map[string]any)
	tt := nativeToolType(nt)
	if def, ok := anthropicNativeNames[tt]; ok {
		if n, _ := nt["name"].(string); strings.TrimSpace(n) == "" {
			nt["name"] = def
		}
		if strings.HasSuffix(tt, "_20260209") && (strings.HasPrefix(tt, "web_search") || strings.HasPrefix(tt, "web_fetch")) {
			if _, ok := nt["allowed_callers"]; !ok {
				nt["allowed_callers"] = []any{"direct"}
			}
		}
	}
	return nt
}

func thinkingBudget(budget int64, has bool, maxTokens int64) int64 {
	b := budget
	if !has || b < 1024 {
		b = 1024
		if maxTokens > 2048 {
			b = maxTokens / 4
		}
	}
	if b >= maxTokens && maxTokens > 1 {
		b = maxTokens - 1
	}
	return b
}

func anthropicThinking(o options, maxTokens int64) (map[string]any, error) {
	budget, hasBudget := o.int("budget_tokens")
	var th map[string]any
	if v, ok := o["thinking"]; ok && v != nil {
		switch t := v.(type) {
		case map[string]any:
			th = cloneJSON(t).(map[string]any)
		case string:
			typ := strings.ToLower(strings.TrimSpace(t))
			switch typ {
			case "enabled":
				th = map[string]any{"type": "enabled", "budget_tokens": thinkingBudget(budget, hasBudget, maxTokens)}
			case "":
			default:
				th = map[string]any{"type": typ}
			}
		case bool:
			if t {
				th = map[string]any{"type": "enabled", "budget_tokens": thinkingBudget(budget, hasBudget, maxTokens)}
			} else {
				th = map[string]any{"type": "disabled"}
			}
		default:
			return nil, optionError("thinking", "must be an object, a string or a boolean")
		}
	} else if en, ok := o.boolean("enable_thinking"); ok {
		switch {
		case !en:
			th = map[string]any{"type": "disabled"}
		case hasBudget && budget >= 1024:
			th = map[string]any{"type": "enabled", "budget_tokens": budget}
		default:
			th = map[string]any{"type": "adaptive"}
		}
	} else if hasBudget && budget >= 1024 {
		th = map[string]any{"type": "enabled", "budget_tokens": budget}
	}
	if th != nil {
		if d, ok := o.str("thinking_display"); ok && th["type"] != "disabled" {
			th["display"] = d
		}
	}
	return th, nil
}

func anthropicToolChoice(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		switch strings.ToLower(s) {
		case "":
			return nil, false
		case "auto", "any", "none":
			return map[string]any{"type": strings.ToLower(s)}, true
		case "required":
			return map[string]any{"type": "any"}, true
		}
		return map[string]any{"type": "tool", "name": s}, true
	case map[string]any:
		return cloneJSON(t), true
	}
	return nil, false
}

// ---- parsing --------------------------------------------------------------

func (anthropicAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	if t, _ := m["type"].(string); t == "error" {
		return nil, upstreamFromObject(m, m)
	}
	res := &Result{ID: strOf(m["id"])}
	declared := declaredToolNames(x)
	var text strings.Builder
	var think strings.Builder
	for _, c := range asList(m["content"]) {
		blk, _ := c.(map[string]any)
		if blk == nil {
			continue
		}
		typ := strOf(blk["type"])
		switch {
		case typ == "text":
			text.WriteString(strOf(blk["text"]))
			for _, ct := range asList(blk["citations"]) {
				if cm, ok := ct.(map[string]any); ok {
					res.Citations = addCitation(res.Citations, Citation{URL: strOf(cm["url"]), Title: strOf(cm["title"])})
				}
			}
		case typ == "thinking":
			think.WriteString(strOf(blk["thinking"]))
			if sig := strOf(blk["signature"]); sig != "" {
				res.ReasoningSignature = sig
			}
		case typ == "tool_use":
			name := strOf(blk["name"])
			if anthropicServerToolNames[name] && !declared[name] {
				return nil, undeclaredNative(name)
			}
			args, _ := json.Marshal(blk["input"])
			if blk["input"] == nil {
				args = []byte("{}")
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: strOf(blk["id"]), Name: name, Arguments: string(args)})
		case typ == "server_tool_use":
			sc := ServerToolCall{ID: strOf(blk["id"]), Type: strOf(blk["name"]), Status: "completed", Detail: rawJSON(stripEncrypted(blk))}
			if in, ok := blk["input"].(map[string]any); ok {
				if q := strOf(in["query"]); q != "" {
					sc.Queries = append(sc.Queries, q)
				}
			}
			res.ServerToolCalls = append(res.ServerToolCalls, sc)
		case strings.HasSuffix(typ, "_tool_result"):
			sc := ServerToolCall{ID: strOf(blk["tool_use_id"]), Type: strings.TrimSuffix(typ, "_tool_result"), Status: "completed"}
			if cm, ok := blk["content"].(map[string]any); ok {
				if et := strOf(cm["type"]); strings.HasSuffix(et, "_error") {
					sc.Status = "error"
				}
			}
			for _, r := range asList(blk["content"]) {
				if rm, ok := r.(map[string]any); ok && strOf(rm["url"]) != "" {
					cit := Citation{URL: strOf(rm["url"]), Title: strOf(rm["title"])}
					sc.Sources = addCitation(sc.Sources, cit)
					res.Citations = addCitation(res.Citations, cit)
				}
			}
			sc.Detail = rawJSON(scrubEncrypted(blk))
			res.ServerToolCalls = append(res.ServerToolCalls, sc)
		}
	}
	res.Text = strings.TrimSpace(text.String())
	res.Reasoning = think.String()
	usage, _ := m["usage"].(map[string]any)
	res.Usage = anthropicUsage(usage)
	res.ServerToolUsage = anthropicServerUsage(usage)
	res.FinishReason = anthropicStop(strOf(m["stop_reason"]))
	return res, nil
}

func anthropicStop(s string) string {
	switch s {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	}
	return s
}
