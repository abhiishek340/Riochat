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

type chatStream struct {
	x       *exchange
	frames  int
	parsed  int
	res     Result
	text    strings.Builder
	think   strings.Builder
	tools   map[int]*toolAcc
	order   []int
	dsml    *dsmlFilter
	cites   []Citation
	usage   Usage
	haveUse bool
	flushed bool
	tail    string
	dsmlErr error
}

func (chatAdapter) stream(x *exchange) streamer {
	s := &chatStream{x: x, tools: map[int]*toolAcc{}}
	if isDeepSeek(x) {
		s.dsml = &dsmlFilter{}
	}
	return s
}

func (s *chatStream) frame(f sseFrame, emit func(Event) error) error {
	s.frames++
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	s.parsed++
	if id := strOf(m["id"]); id != "" && s.res.ID == "" {
		s.res.ID = id
		s.res.ServiceTier = strOf(m["service_tier"])
		if err := emit(Event{Type: EventCreated, ResponseID: id, ServiceTier: s.res.ServiceTier}); err != nil {
			return err
		}
	} else if t := strOf(m["service_tier"]); t != "" {
		s.res.ServiceTier = t
	}
	if u, ok := m["usage"].(map[string]any); ok && u != nil {
		nu := openAIUsage(u, false)
		if nu.ServiceTier == "" {
			nu.ServiceTier = s.res.ServiceTier
		}
		mergeUsage(&s.usage, nu)
		s.haveUse = true
		cp := s.usage
		if err := emit(Event{Type: EventUsage, Usage: &cp}); err != nil {
			return err
		}
	}
	for _, c := range asList(m["citations"]) {
		if str, ok := c.(string); ok {
			before := len(s.cites)
			s.cites = addCitation(s.cites, Citation{URL: str})
			if len(s.cites) > before {
				cc := Citation{URL: str}
				if err := emit(Event{Type: EventCitation, Citation: &cc}); err != nil {
					return err
				}
			}
		}
	}
	choices := asList(m["choices"])
	if len(choices) == 0 {
		return nil
	}
	ch, _ := choices[0].(map[string]any)
	if fr := strOf(ch["finish_reason"]); fr != "" {
		s.res.FinishReason = fr
	}
	delta, _ := ch["delta"].(map[string]any)
	if delta == nil {
		return nil
	}
	switch c := delta["content"].(type) {
	case string:
		if err := s.pushText(c, emit); err != nil {
			return err
		}
	case []any:
		for _, p := range c {
			pm, _ := p.(map[string]any)
			pt := strOf(pm["type"])
			t := firstNonEmpty(strOf(pm["text"]), strOf(pm["reasoning"]), strOf(pm["thinking"]))
			switch {
			case t == "":
			case strings.Contains(pt, "reason") || strings.Contains(pt, "think"):
				s.think.WriteString(t)
				if err := emit(Event{Type: EventReasoning, Text: t}); err != nil {
					return err
				}
			default:
				if err := s.pushText(t, emit); err != nil {
					return err
				}
			}
		}
	}
	if r := firstNonEmpty(strOf(delta["reasoning_content"]), strOf(delta["reasoning"])); r != "" {
		s.think.WriteString(r)
		if err := emit(Event{Type: EventReasoning, Text: r}); err != nil {
			return err
		}
	}
	for pos, t := range asList(delta["tool_calls"]) {
		tm, _ := t.(map[string]any)
		if tm == nil {
			continue
		}
		idx := pos
		if n, ok := toInt(tm["index"]); ok && n >= 0 && n <= 1024 {
			idx = int(n)
		}
		acc, ok := s.tools[idx]
		if !ok {
			acc = &toolAcc{index: idx}
			s.tools[idx] = acc
			s.order = append(s.order, idx)
		}
		if id := strOf(tm["id"]); id != "" {
			acc.callID = id
		}
		frag := ToolCall{ID: acc.callID}
		if fn, ok := tm["function"].(map[string]any); ok {
			if n := strOf(fn["name"]); n != "" {
				acc.name += n
				frag.Name = n
			}
			if a := strOf(fn["arguments"]); a != "" {
				acc.args.WriteString(a)
				frag.Arguments = a
			}
		} else if cu, ok := tm["custom"].(map[string]any); ok {
			acc.custom = true
			if n := strOf(cu["name"]); n != "" {
				acc.name += n
				frag.Name = n
			}
			if a := strOf(cu["input"]); a != "" {
				acc.args.WriteString(a)
				frag.Arguments = a
			}
		}
		if err := emit(Event{Type: EventToolCall, Index: idx, ToolCall: &frag}); err != nil {
			return err
		}
	}
	return nil
}

func (s *chatStream) pushText(t string, emit func(Event) error) error {
	if t == "" {
		return nil
	}
	if s.dsml != nil {
		t = s.dsml.push(t)
		if t == "" {
			return nil
		}
	}
	s.text.WriteString(t)
	return emit(Event{Type: EventText, Text: t})
}

// flush releases withheld text and converts completed text-encoded tool calls.
func (s *chatStream) flush(emit func(Event) error) error {
	if s.flushed || s.dsml == nil {
		return nil
	}
	s.flushed = true
	s.tail, s.dsmlErr = s.dsml.finish()
	if s.dsmlErr != nil {
		return s.dsmlErr
	}
	if s.tail != "" {
		if err := emit(Event{Type: EventText, Text: s.tail}); err != nil {
			return err
		}
	}
	for i := range s.dsml.calls {
		tc := s.dsml.calls[i]
		if err := emit(Event{Type: EventToolCall, Index: len(s.order) + i, ToolCall: &tc}); err != nil {
			return err
		}
	}
	return nil
}

func (s *chatStream) result() (*Result, error) {
	if s.frames == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream ended before any event", Accepted: true}
	}
	if s.parsed == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream carried no readable event", Accepted: true}
	}
	res := s.res
	res.Text = s.text.String() + s.tail
	res.Reasoning = s.think.String()
	for _, idx := range s.order {
		t := s.tools[idx]
		args := t.args.String()
		if t.custom {
			b, _ := json.Marshal(map[string]any{"input": args})
			args = string(b)
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: t.callID, Name: t.name, Arguments: argsOrEmpty(args)})
	}
	if s.dsml != nil && s.flushed && s.dsmlErr == nil {
		res.ToolCalls = append(res.ToolCalls, s.dsml.calls...)
	}
	res.Citations = s.cites
	res.Usage = s.usage
	if len(res.ToolCalls) > 0 && (res.FinishReason == "" || res.FinishReason == "stop") {
		res.FinishReason = "tool_calls"
	}
	return &res, s.dsmlErr
}
