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

// renderResponsesMessage converts one message to Responses input items.
func renderResponsesMessage(m Message, idx int, openrouter, explicit bool) []any {
	var items []any
	role := m.Role
	assistant := role == RoleAssistant
	textType := "input_text"
	if assistant {
		textType = "output_text"
	}
	rolename := string(role)
	if role == RoleTool || role == RoleFunction {
		rolename = "user"
	}

	var blocks []any
	var lastSupported = -1
	for _, p := range effectiveParts(m) {
		var blk map[string]any
		if t, ok := partAsText(p); ok {
			blk = map[string]any{"type": textType, "text": t}
		} else {
			switch p.Kind {
			case PartImage:
				if assistant || len(p.Data) == 0 {
					continue
				}
				blk = map[string]any{"type": "input_image", "image_url": dataURL(imageMIME(p.MIME), p.Data)}
			case PartDocument:
				if len(p.Data) == 0 {
					continue
				}
				name := p.FileName
				if strings.TrimSpace(name) == "" {
					name = "document.pdf"
				}
				blk = map[string]any{"type": "input_file", "filename": name, "file_data": dataURL("application/pdf", p.Data)}
			default:
				continue
			}
		}
		blocks = append(blocks, blk)
		if explicit && cacheBreakpointTypes[blk["type"].(string)] {
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
		item := map[string]any{"role": rolename, "content": blocks}
		if openrouter {
			item["type"] = "message"
			if assistant {
				item["id"] = "msg_riochat_" + sanitizeID(fmt.Sprint(idx))
				item["status"] = "completed"
			}
		}
		items = append(items, item)
	}
	for _, tc := range m.ToolCalls {
		it := map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": argsOrEmpty(tc.Arguments)}
		if openrouter {
			it["id"] = "fc_" + sanitizeID(tc.ID)
			it["status"] = "completed"
		}
		items = append(items, it)
	}
	for _, tr := range m.ToolResults {
		it := map[string]any{"type": "function_call_output", "call_id": tr.CallID, "output": toolResultOutput(tr)}
		if openrouter {
			it["id"] = "fc_output_" + sanitizeID(tr.CallID)
		}
		items = append(items, it)
	}
	return items
}

func imageMIME(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
		if m == "image/jpg" {
			return "image/jpeg"
		}
		return m
	}
	if strings.HasPrefix(m, "image/") {
		return m
	}
	return "image/jpeg"
}

// ---- parsing --------------------------------------------------------------

func (responsesAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	return parseResponsesDoc(m, x.proto)
}

func upstreamFromObject(m map[string]any, holder map[string]any) *Error {
	f := sseFrame{Event: "error"}
	b, _ := json.Marshal(holder)
	f.Data = string(b)
	if e := inStreamError(f); e != nil {
		return e
	}
	msg, code := errorFromMap(m)
	if msg == "" {
		msg = "upstream reported a failure"
	}
	return &Error{Kind: KindUpstream, Status: 502, Code: code, Message: msg, Accepted: true}
}

func parseResponsesDoc(m map[string]any, proto Protocol) (*Result, error) {
	if e, ok := m["error"].(map[string]any); ok && e != nil {
		return nil, upstreamFromObject(m, m)
	}
	if st, _ := m["status"].(string); st == "failed" {
		return nil, upstreamFromObject(m, map[string]any{"type": "response.failed", "error": firstObj(m["error"], map[string]any{"message": "response failed"})})
	}
	res := &Result{}
	res.ID, _ = m["id"].(string)
	res.ServiceTier, _ = m["service_tier"].(string)
	xai := proto == XAIResponses

	var text, reasoning strings.Builder
	serverCounts := map[string]int{}
	items, _ := m["output"].([]any)
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := item["type"].(string)
		switch {
		case typ == "message":
			content, _ := item["content"].([]any)
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				switch ct, _ := cm["type"].(string); ct {
				case "output_text", "text":
					if s, ok := cm["text"].(string); ok {
						text.WriteString(s)
					}
					for _, a := range asList(cm["annotations"]) {
						am, _ := a.(map[string]any)
						if at, _ := am["type"].(string); at == "url_citation" || am["url"] != nil {
							res.Citations = addCitation(res.Citations, Citation{URL: strOf(am["url"]), Title: strOf(am["title"])})
						}
					}
				case "refusal":
					if s, ok := cm["refusal"].(string); ok {
						text.WriteString(s)
					}
				}
			}
		case typ == "reasoning":
			var parts []string
			for _, s := range asList(item["summary"]) {
				if sm, ok := s.(map[string]any); ok {
					if t := strOf(sm["text"]); t != "" {
						parts = append(parts, t)
					}
				}
			}
			if len(parts) == 0 {
				for _, s := range asList(item["content"]) {
					if sm, ok := s.(map[string]any); ok {
						if t := strOf(sm["text"]); t != "" {
							parts = append(parts, t)
						}
					}
				}
			}
			if len(parts) > 0 {
				if reasoning.Len() > 0 {
					reasoning.WriteString("\n\n")
				}
				reasoning.WriteString(strings.Join(parts, "\n\n"))
			}
			if enc := strOf(item["encrypted_content"]); enc != "" {
				res.ReasoningSignature = enc
			}
		case typ == "function_call":
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID: firstNonEmpty(strOf(item["call_id"]), strOf(item["id"])), Name: strOf(item["name"]),
				Arguments: argsOrEmpty(strOf(item["arguments"]))})
		case typ == "custom_tool_call":
			in := strOf(item["input"])
			b, _ := json.Marshal(map[string]any{"input": in})
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID: firstNonEmpty(strOf(item["call_id"]), strOf(item["id"])), Name: strOf(item["name"]), Arguments: string(b)})
		case strings.HasSuffix(typ, "_call") || strings.HasSuffix(typ, "_calls"):
			name := normalizeServerToolName(typ)
			sc := ServerToolCall{ID: strOf(item["id"]), Type: name, Status: strOf(item["status"]), Detail: rawJSON(stripEncrypted(item))}
			if action, ok := item["action"].(map[string]any); ok {
				if q := strOf(action["query"]); q != "" {
					sc.Queries = append(sc.Queries, q)
				}
				for _, q := range asList(action["queries"]) {
					sc.Queries = uniqueAppend(sc.Queries, strOf(q))
				}
				for _, s := range asList(action["sources"]) {
					if sm, ok := s.(map[string]any); ok {
						c := Citation{URL: strOf(sm["url"]), Title: strOf(sm["title"])}
						sc.Sources = addCitation(sc.Sources, c)
						res.Citations = addCitation(res.Citations, c)
					}
				}
			}
			if name == "image_generation" {
				if b64s := strOf(item["result"]); b64s != "" {
					if data, err := base64.StdEncoding.DecodeString(b64s); err == nil {
						mime := "image/png"
						if f := strOf(item["output_format"]); f != "" {
							mime = "image/" + strings.ToLower(f)
						}
						res.Images = append(res.Images, Image{Index: len(res.Images), Data: data, MIME: mime, RevisedPrompt: strOf(item["revised_prompt"])})
					}
				}
			}
			serverCounts[name]++
			res.ServerToolCalls = append(res.ServerToolCalls, sc)
		}
	}
	res.Text = text.String()
	res.Reasoning = reasoning.String()
	if res.Text == "" {
		if s, ok := m["output_text"].(string); ok {
			res.Text = s
		}
	}
	if len(res.ToolCalls) == 0 {
		for _, t := range asList(m["tool_calls"]) {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			fn, _ := tm["function"].(map[string]any)
			if fn == nil {
				fn = tm
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID: firstNonEmpty(strOf(tm["call_id"]), strOf(tm["id"])), Name: strOf(fn["name"]), Arguments: argsOrEmpty(strOf(fn["arguments"]))})
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
	res.Usage = openAIUsage(usage, xai)
	if res.Usage.ServiceTier == "" {
		res.Usage.ServiceTier = res.ServiceTier
	}
	res.ServerToolUsage = serverToolUsage(usage, serverCounts)
	if inc, ok := m["incomplete_details"].(map[string]any); ok {
		res.FinishReason = strOf(inc["reason"])
	} else if len(res.ToolCalls) > 0 {
		res.FinishReason = "tool_calls"
	} else if m["status"] != nil || len(items) > 0 {
		res.FinishReason = "stop"
	}
	return res, nil
}

func firstObj(v any, def map[string]any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return def
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func strOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprint(x)
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// normalizeServerToolName strips a trailing _call / _calls.
func normalizeServerToolName(s string) string {
	s = strings.TrimSuffix(s, "_calls")
	s = strings.TrimSuffix(s, "_call")
	s = strings.TrimSuffix(s, "_requests")
	return s
}

func stripEncrypted(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		if strings.Contains(strings.ToLower(k), "encrypted") {
			continue
		}
		out[k] = v
	}
	return out
}

// serverToolUsage derives counts of provider-hosted tool invocations. Counts
// reported by the provider win over counts derived from output items.
func serverToolUsage(usage map[string]any, derived map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range derived {
		out[k] = v
	}
	reported := map[string]int{}
	for _, key := range []string{"server_side_tool_usage_details", "server_tool_use", "num_server_side_tools_used"} {
		if det, ok := usage[key].(map[string]any); ok {
			for name, v := range det {
				if n, ok := toInt(v); ok && n > 0 {
					reported[normalizeServerToolName(name)] += int(n)
				}
			}
		}
	}
	if len(reported) > 0 {
		out = reported
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
