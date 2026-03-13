package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type toolAcc struct {
	id, callID, name string
	args             strings.Builder
	custom           bool
	index            int
	done             bool
}

type responsesStream struct {
	x        *exchange
	frames   int
	id       string
	tier     string
	text     strings.Builder
	thinking strings.Builder
	tools    map[string]*toolAcc
	order    []string
	servers  map[string]*ServerToolCall
	sorder   []string
	images   []Image
	cites    []Citation
	final    *Result
	sig      string
	nextIdx  int
}

func (responsesAdapter) stream(x *exchange) streamer {
	return &responsesStream{x: x, tools: map[string]*toolAcc{}, servers: map[string]*ServerToolCall{}}
}

func (s *responsesStream) tool(itemID string) *toolAcc {
	if t, ok := s.tools[itemID]; ok {
		return t
	}
	t := &toolAcc{id: itemID, index: s.nextIdx}
	s.nextIdx++
	s.tools[itemID] = t
	s.order = append(s.order, itemID)
	return t
}

func (s *responsesStream) frame(f sseFrame, emit func(Event) error) error {
	s.frames++
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	typ := strOf(m["type"])
	if typ == "" {
		typ = f.Event
	}
	switch typ {
	case "response.created", "response.in_progress", "response.queued":
		if r, ok := m["response"].(map[string]any); ok {
			if id := strOf(r["id"]); id != "" && s.id == "" {
				s.id = id
				s.tier = strOf(r["service_tier"])
				return emit(Event{Type: EventCreated, ResponseID: s.id, ServiceTier: s.tier})
			}
			if t := strOf(r["service_tier"]); t != "" {
				s.tier = t
			}
		}
	case "response.output_item.added", "response.output_item.in_progress":
		item, _ := m["item"].(map[string]any)
		return s.itemAdded(item, emit)
	case "response.output_item.done":
		item, _ := m["item"].(map[string]any)
		return s.itemDone(item, emit)
	case "response.output_text.delta", "response.refusal.delta":
		d := strOf(m["delta"])
		if d == "" {
			return nil
		}
		s.text.WriteString(d)
		return emit(Event{Type: EventText, Text: d})
	case "response.output_text.done", "response.refusal.done":
		if s.text.Len() == 0 {
			t := firstNonEmpty(strOf(m["text"]), strOf(m["refusal"]))
			if t != "" {
				s.text.WriteString(t)
				return emit(Event{Type: EventText, Text: t})
			}
		}
	case "response.output_text.annotation.added":
		if a, ok := m["annotation"].(map[string]any); ok && strOf(a["url"]) != "" {
			c := Citation{URL: strOf(a["url"]), Title: strOf(a["title"])}
			before := len(s.cites)
			s.cites = addCitation(s.cites, c)
			if len(s.cites) > before {
				return emit(Event{Type: EventCitation, Citation: &c})
			}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.thinking.delta":
		d := strOf(m["delta"])
		if d == "" {
			return nil
		}
		s.thinking.WriteString(d)
		return emit(Event{Type: EventReasoning, Text: d})
	case "response.reasoning_summary_text.done", "response.reasoning_text.done", "response.thinking.done":
		if s.thinking.Len() == 0 {
			if t := strOf(m["text"]); t != "" {
				s.thinking.WriteString(t)
				return emit(Event{Type: EventReasoning, Text: t})
			}
		}
	case "response.reasoning_summary_part.added":
		if s.thinking.Len() > 0 {
			s.thinking.WriteString("\n\n")
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		t := s.tool(strOf(m["item_id"]))
		t.custom = typ == "response.custom_tool_call_input.delta"
		d := strOf(m["delta"])
		t.args.WriteString(d)
		if d != "" {
			return emit(Event{Type: EventToolCall, Index: t.index, ToolCall: &ToolCall{ID: t.callID, Name: t.name, Arguments: d}})
		}
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		t := s.tool(strOf(m["item_id"]))
		t.custom = typ == "response.custom_tool_call_input.done"
		full := firstNonEmpty(strOf(m["arguments"]), strOf(m["input"]))
		if t.args.Len() == 0 && full != "" {
			t.args.WriteString(full)
		}
		t.done = true
	case "response.image_generation_call.partial_image":
		b := strOf(m["partial_image_b64"])
		data, err := base64.StdEncoding.DecodeString(b)
		if err != nil || len(data) == 0 {
			return nil
		}
		idx, _ := toInt(m["partial_image_index"])
		mime := "image/png"
		if of := strOf(m["output_format"]); of != "" {
			mime = "image/" + strings.ToLower(of)
		}
		img := Image{Index: int(idx), Data: data, MIME: mime, Partial: true}
		return emit(Event{Type: EventImage, Index: int(idx), Image: &img})
	case "response.completed", "response.incomplete":
		r, _ := m["response"].(map[string]any)
		if r == nil {
			return nil
		}
		res, err := parseResponsesDoc(r, s.x.proto)
		if err != nil {
			return err
		}
		s.final = res
		if res.ID != "" && s.id == "" {
			s.id = res.ID
		}
		if !res.Usage.IsZero() {
			u := res.Usage
			return emit(Event{Type: EventUsage, Usage: &u})
		}
	default:
		// response.<tool>_call.<status> progress events for hosted tools.
		if strings.HasPrefix(typ, "response.") {
			rest := strings.TrimPrefix(typ, "response.")
			if name, status, ok := strings.Cut(rest, "."); ok && strings.HasSuffix(name, "_call") {
				switch status {
				case "in_progress", "searching", "generating", "completed", "failed", "error", "interpreting":
					id := strOf(m["item_id"])
					sc := s.server(id, normalizeServerToolName(name))
					sc.Status = status
					c := *sc
					return emit(Event{Type: EventServerTool, ServerTool: &c})
				}
			}
		}
	}
	return nil
}

func (s *responsesStream) server(id, typ string) *ServerToolCall {
	if sc, ok := s.servers[id]; ok {
		return sc
	}
	sc := &ServerToolCall{ID: id, Type: typ}
	s.servers[id] = sc
	s.sorder = append(s.sorder, id)
	return sc
}

func (s *responsesStream) itemAdded(item map[string]any, emit func(Event) error) error {
	if item == nil {
		return nil
	}
	typ := strOf(item["type"])
	switch {
	case typ == "function_call" || typ == "custom_tool_call":
		t := s.tool(strOf(item["id"]))
		t.callID = firstNonEmpty(strOf(item["call_id"]), t.callID, t.id)
		t.name = firstNonEmpty(strOf(item["name"]), t.name)
		t.custom = typ == "custom_tool_call"
		if a := strOf(item["arguments"]); a != "" && t.args.Len() == 0 {
			t.args.WriteString(a)
		}
		return emit(Event{Type: EventToolCall, Index: t.index, ToolCall: &ToolCall{ID: t.callID, Name: t.name}})
	case strings.HasSuffix(typ, "_call"):
		sc := s.server(strOf(item["id"]), normalizeServerToolName(typ))
		sc.Status = firstNonEmpty(strOf(item["status"]), sc.Status)
		c := *sc
		return emit(Event{Type: EventServerTool, ServerTool: &c})
	}
	return nil
}

func (s *responsesStream) itemDone(item map[string]any, emit func(Event) error) error {
	if item == nil {
		return nil
	}
	typ := strOf(item["type"])
	switch {
	case typ == "function_call" || typ == "custom_tool_call":
		t := s.tool(strOf(item["id"]))
		t.callID = firstNonEmpty(strOf(item["call_id"]), t.callID, t.id)
		t.name = firstNonEmpty(strOf(item["name"]), t.name)
		t.custom = typ == "custom_tool_call"
		if a := firstNonEmpty(strOf(item["arguments"]), strOf(item["input"])); a != "" && t.args.Len() == 0 {
			t.args.WriteString(a)
		}
		t.done = true
	case typ == "reasoning":
		if e := strOf(item["encrypted_content"]); e != "" {
			s.sig = e
		}
		if s.thinking.Len() == 0 {
			for _, sm := range asList(item["summary"]) {
				if mm, ok := sm.(map[string]any); ok {
					if t := strOf(mm["text"]); t != "" {
						if s.thinking.Len() > 0 {
							s.thinking.WriteString("\n\n")
						}
						s.thinking.WriteString(t)
					}
				}
			}
		}
	case strings.HasSuffix(typ, "_call"):
		sc := s.server(strOf(item["id"]), normalizeServerToolName(typ))
		sc.Status = firstNonEmpty(strOf(item["status"]), "completed")
		sc.Detail = rawJSON(stripEncrypted(item))
		if normalizeServerToolName(typ) == "image_generation" {
			if data, err := base64.StdEncoding.DecodeString(strOf(item["result"])); err == nil && len(data) > 0 {
				mime := "image/png"
				if of := strOf(item["output_format"]); of != "" {
					mime = "image/" + strings.ToLower(of)
				}
				img := Image{Index: len(s.images), Data: data, MIME: mime, RevisedPrompt: strOf(item["revised_prompt"])}
				s.images = append(s.images, img)
				c := *sc
				if err := emit(Event{Type: EventServerTool, ServerTool: &c}); err != nil {
					return err
				}
				return emit(Event{Type: EventImage, Index: img.Index, Image: &img})
			}
		}
		c := *sc
		return emit(Event{Type: EventServerTool, ServerTool: &c})
	}
	return nil
}

func (s *responsesStream) result() (*Result, error) {
	if s.frames == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream ended before any event", Accepted: true}
	}
	var res Result
	if s.final != nil {
		res = *s.final
	}
	if res.ID == "" {
		res.ID = s.id
	}
	if res.ServiceTier == "" {
		res.ServiceTier = s.tier
	}
	if res.Text == "" {
		res.Text = s.text.String()
	}
	if res.Reasoning == "" {
		res.Reasoning = s.thinking.String()
	}
	if res.ReasoningSignature == "" {
		res.ReasoningSignature = s.sig
	}
	if len(res.ToolCalls) == 0 {
		for _, id := range s.order {
			t := s.tools[id]
			args := t.args.String()
			if t.custom {
				b, _ := json.Marshal(map[string]any{"input": args})
				args = string(b)
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: firstNonEmpty(t.callID, t.id), Name: t.name, Arguments: argsOrEmpty(args)})
		}
	}
	if len(res.ServerToolCalls) == 0 {
		for _, id := range s.sorder {
			res.ServerToolCalls = append(res.ServerToolCalls, *s.servers[id])
		}
	}
	if len(res.Images) == 0 {
		res.Images = s.images
	}
	for _, c := range s.cites {
		res.Citations = addCitation(res.Citations, c)
	}
	if res.FinishReason == "" {
		if len(res.ToolCalls) > 0 {
			res.FinishReason = "tool_calls"
		} else if s.final != nil {
			res.FinishReason = "stop"
		}
	}
	return &res, nil
}
