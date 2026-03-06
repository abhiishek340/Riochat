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

func renderChatMessage(m Message, openrouter, explicit bool) []any {
	role := string(m.Role)
	switch m.Role {
	case RoleDeveloper:
		role = "system"
	case RoleTool, RoleFunction:
		role = "user"
	}
	var out []any
	assistant := m.Role == RoleAssistant

	parts := effectiveParts(m)
	anyHint := m.CacheHint
	allText := true
	for _, p := range parts {
		if _, ok := partAsText(p); !ok {
			allText = false
		}
		if p.CacheHint {
			anyHint = true
		}
	}
	var content any
	hasContent := false
	if allText && !(explicit && anyHint) {
		var sb []string
		for _, p := range parts {
			t, _ := partAsText(p)
			sb = append(sb, t)
		}
		if len(sb) > 0 {
			content, hasContent = strings.Join(sb, "\n\n"), true
		}
	} else {
		var blocks []any
		lastSupported := -1
		for _, p := range parts {
			var blk map[string]any
			if t, ok := partAsText(p); ok {
				blk = map[string]any{"type": "text", "text": t}
			} else {
				if assistant {
					continue
				}
				switch p.Kind {
				case PartImage:
					if len(p.Data) == 0 {
						continue
					}
					blk = map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(imageMIME(p.MIME), p.Data)}}
				case PartDocument:
					if len(p.Data) == 0 {
						continue
					}
					name := p.FileName
					if strings.TrimSpace(name) == "" {
						name = "document.pdf"
					}
					blk = map[string]any{"type": "file", "file": map[string]any{"filename": name, "file_data": dataURL("application/pdf", p.Data)}}
				case PartAudio:
					if len(p.Data) == 0 {
						continue
					}
					blk = map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": base64.StdEncoding.EncodeToString(p.Data), "format": audioFormat(p.MIME)}}
				case PartVideo:
					if len(p.Data) == 0 {
						continue
					}
					blk = map[string]any{"type": "video_url", "video_url": map[string]any{"url": dataURL(p.MIME, p.Data)}}
				default:
					continue
				}
			}
			blocks = append(blocks, blk)
			if explicit && chatBreakpointTypes[blk["type"].(string)] {
				lastSupported = len(blocks) - 1
				if p.CacheHint && !p.Dynamic {
					blk["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
				}
			}
		}
		if explicit && m.CacheHint && lastSupported >= 0 {
			blocks[lastSupported].(map[string]any)["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
		}
		if len(blocks) > 0 {
			content, hasContent = blocks, true
		}
	}

	if hasContent || len(m.ToolCalls) > 0 || (assistant && m.Reasoning != "") {
		msg := map[string]any{"role": role}
		if hasContent {
			msg["content"] = content
		} else {
			msg["content"] = ""
		}
		if assistant {
			if m.Reasoning != "" {
				if openrouter {
					msg["reasoning"] = m.Reasoning
				} else {
					msg["reasoning_content"] = m.Reasoning
				}
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					calls = append(calls, map[string]any{"id": tc.ID, "type": "function",
						"function": map[string]any{"name": tc.Name, "arguments": argsOrEmpty(tc.Arguments)}})
				}
				msg["tool_calls"] = calls
			}
		}
		out = append(out, msg)
	}
	for _, tr := range m.ToolResults {
		out = append(out, map[string]any{"role": "tool", "tool_call_id": tr.CallID, "content": toolResultOutput(tr)})
	}
	return out
}

// ---- parsing --------------------------------------------------------------

func (chatAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	if e, ok := m["error"].(map[string]any); ok && e != nil {
		return nil, upstreamFromObject(m, m)
	}
	res := &Result{ID: strOf(m["id"]), ServiceTier: strOf(m["service_tier"])}
	choices := asList(m["choices"])
	if len(choices) > 0 {
		ch, _ := choices[0].(map[string]any)
		msg, _ := ch["message"].(map[string]any)
		res.FinishReason = strOf(ch["finish_reason"])
		var text, think strings.Builder
		switch c := msg["content"].(type) {
		case string:
			text.WriteString(c)
		case []any:
			for _, p := range c {
				pm, _ := p.(map[string]any)
				switch pt := strOf(pm["type"]); {
				case pt == "text" || pt == "output_text":
					text.WriteString(strOf(pm["text"]))
				case strings.Contains(pt, "reason") || strings.Contains(pt, "think"):
					think.WriteString(firstNonEmpty(strOf(pm["text"]), strOf(pm["reasoning"]), strOf(pm["thinking"])))
				}
			}
		}
		if r := firstNonEmpty(strOf(msg["reasoning_content"]), strOf(msg["reasoning"])); r != "" {
			if think.Len() > 0 {
				think.WriteString("\n\n")
			}
			think.WriteString(r)
		}
		if think.Len() == 0 {
			var parts []string
			for _, d := range asList(msg["reasoning_details"]) {
				if dm, ok := d.(map[string]any); ok {
					if t := firstNonEmpty(strOf(dm["text"]), strOf(dm["summary"])); t != "" {
						parts = append(parts, t)
					}
				}
			}
			think.WriteString(strings.Join(parts, "\n\n"))
		}
		res.Text, res.Reasoning = text.String(), think.String()
		if r := strOf(msg["refusal"]); r != "" && res.Text == "" {
			res.Text = r
		}
		for _, t := range asList(msg["tool_calls"]) {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			tc := ToolCall{ID: strOf(tm["id"])}
			if fn, ok := tm["function"].(map[string]any); ok {
				tc.Name, tc.Arguments = strOf(fn["name"]), argsOrEmpty(strOf(fn["arguments"]))
			} else if cu, ok := tm["custom"].(map[string]any); ok {
				b, _ := json.Marshal(map[string]any{"input": strOf(cu["input"])})
				tc.Name, tc.Arguments = strOf(cu["name"]), string(b)
			}
			res.ToolCalls = append(res.ToolCalls, tc)
		}
		for _, a := range asList(msg["annotations"]) {
			am, _ := a.(map[string]any)
			uc, _ := am["url_citation"].(map[string]any)
			if uc == nil {
				uc = am
			}
			res.Citations = addCitation(res.Citations, Citation{URL: strOf(uc["url"]), Title: strOf(uc["title"])})
		}
	}
	for _, c := range asList(m["citations"]) {
		switch cv := c.(type) {
		case string:
			res.Citations = addCitation(res.Citations, Citation{URL: cv})
		case map[string]any:
			res.Citations = addCitation(res.Citations, Citation{URL: strOf(cv["url"]), Title: strOf(cv["title"])})
		}
	}
	usage, _ := m["usage"].(map[string]any)
	res.Usage = openAIUsage(usage, false)
	if res.Usage.ServiceTier == "" {
		res.Usage.ServiceTier = res.ServiceTier
	}
	if isDeepSeek(x) && res.Text != "" {
		visible, calls, err := extractDSML(res.Text)
		if err != nil {
			return nil, err
		}
		res.Text = visible
		res.ToolCalls = append(res.ToolCalls, calls...)
	}
	if len(res.ToolCalls) > 0 && (res.FinishReason == "" || res.FinishReason == "stop") {
		res.FinishReason = "tool_calls"
	}
	return res, nil
}

// ---- streaming ------------------------------------------------------------
