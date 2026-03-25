package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// interactionsAdapter serves gemini_interactions.
type interactionsAdapter struct{}

var (
	interactionImageRatios = map[string]bool{
		"1:1": true, "2:3": true, "3:2": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true,
		"9:16": true, "16:9": true, "21:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true,
	}
	interactionVideoRatios = map[string]bool{"9:16": true, "16:9": true}
	interactionImageSizes  = map[string]bool{"512": true, "1K": true, "2K": true, "4K": true}
	interactionMIMEs       = map[string]bool{"image/jpeg": true, "application/json": true, "text/plain": true}
	videoTasks             = map[string]bool{"text_to_video": true, "image_to_video": true, "reference_to_video": true, "edit": true}
)

const interactionPollDelay = 2 * time.Second

func (interactionsAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	req := x.req
	if x.model == "" {
		return nil, invalid(ErrInvalidRequest, "model is required")
	}
	native, err := o.nativeTools()
	if err != nil {
		return nil, err
	}
	msgs := normalizeMessages(req.Messages)

	var sys []string
	if strings.TrimSpace(req.Instructions) != "" {
		sys = append(sys, req.Instructions)
	}
	var steps []any
	for _, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper {
			for _, p := range effectiveParts(m) {
				if t, ok := partAsText(p); ok {
					sys = append(sys, t)
				}
			}
			continue
		}
		steps = append(steps, interactionSteps(m)...)
	}
	var input any
	if len(steps) == 0 {
		return nil, invalid(ErrInvalidRequest, "interaction input is required")
	}
	if s, ok := singleUserText(msgs); ok && len(steps) == 1 {
		input = s
	} else {
		input = steps
	}

	body := map[string]any{"model": x.model, "input": input}
	if x.stream {
		body["stream"] = true
	}
	if len(sys) > 0 {
		body["system_instruction"] = strings.Join(sys, "\n\n")
	}
	if rf, err := interactionResponseFormat(o); err != nil {
		return nil, err
	} else if rf != nil {
		body["response_format"] = rf
	}
	gc, err := interactionGenerationConfig(o)
	if err != nil {
		return nil, err
	}
	if len(gc) > 0 {
		body["generation_config"] = gc
	}
	if !req.Ephemeral {
		if id := strings.TrimSpace(req.PreviousResponseID); id != "" {
			body["previous_interaction_id"] = id
		}
	} else {
		body["store"] = false
	}

	var tools []any
	if !req.DisableTools {
		hasSearch := false
		for _, t := range native {
			nt := cloneJSON(t).(map[string]any)
			if nativeToolType(nt) == "" {
				for _, k := range []string{"google_search", "googleSearch", "code_execution", "url_context"} {
					if _, ok := nt[k]; ok {
						nt = map[string]any{"type": strings.ToLower(camelToSnake(k))}
						break
					}
				}
			}
			if nativeToolType(nt) == "google_search" {
				hasSearch = true
			}
			tools = append(tools, nt)
		}
		if ws, _ := o.boolean("web_search"); ws && !hasSearch {
			tools = append(tools, map[string]any{"type": "google_search"})
		}
		for _, td := range req.Tools {
			t := map[string]any{"type": "function", "name": td.Name, "parameters": toolSchema(td.Schema)}
			if td.Description != "" {
				t["description"] = td.Description
			}
			tools = append(tools, t)
		}
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	consumed := []string{"response_format", "generation_config", "temperature", "top_p", "max_output_tokens", "max_tokens",
		"max_completion_tokens", "thinking_level", "thinking_summaries", "seed", "stop", "stop_sequences", "task", "video_task",
		"aspect_ratio", "image_size", "mime_type", "schema", "duration", "resolution"}
	copyExtras(body, o, consumed, "store", "previous_interaction_id")

	u, err := buildURL(googleBase(x.route.BaseURL), "v1beta", "/interactions")
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, u, body)
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func singleUserText(msgs []Message) (string, bool) {
	var only *Message
	for i := range msgs {
		if msgs[i].Role == RoleSystem || msgs[i].Role == RoleDeveloper {
			continue
		}
		if only != nil {
			return "", false
		}
		only = &msgs[i]
	}
	if only == nil || only.Role != RoleUser || !isPlainText(*only) {
		return "", false
	}
	return plainText(*only), true
}

func interactionContent(m Message) []any {
	var out []any
	for _, p := range effectiveParts(m) {
		if t, ok := partAsText(p); ok {
			out = append(out, map[string]any{"type": "text", "text": t})
			continue
		}
		if len(p.Data) == 0 {
			continue
		}
		switch p.Kind {
		case PartImage:
			out = append(out, map[string]any{"type": "image", "data": b64(p.Data), "mime_type": imageMIME(p.MIME)})
		case PartDocument:
			out = append(out, map[string]any{"type": "document", "data": b64(p.Data), "mime_type": "application/pdf"})
		case PartAudio:
			out = append(out, map[string]any{"type": "audio", "data": b64(p.Data), "mime_type": firstNonEmpty(p.MIME, "audio/wav")})
		case PartVideo:
			out = append(out, map[string]any{"type": "video", "data": b64(p.Data), "mime_type": firstNonEmpty(p.MIME, "video/mp4")})
		}
	}
	return out
}

func interactionSteps(m Message) []any {
	var steps []any
	for _, tr := range m.ToolResults {
		res := map[string]any{"type": "function_result", "call_id": tr.CallID, "name": tr.Name, "result": toolResultOutput(tr)}
		if tr.IsError {
			res["is_error"] = true
		}
		steps = append(steps, res)
	}
	if c := interactionContent(m); len(c) > 0 {
		typ := "user_input"
		if m.Role == RoleAssistant {
			typ = "model_output"
		}
		steps = append(steps, map[string]any{"type": typ, "content": c})
	}
	if m.Role == RoleAssistant {
		for _, tc := range m.ToolCalls {
			steps = append(steps, map[string]any{"type": "function_call", "id": tc.ID, "name": tc.Name, "arguments": parseArgsObject(tc.Arguments)})
		}
	}
	return steps
}

// interactionResponseFormat builds response_format from the option, accepting
// the native shape (type text|image|video) and the common JSON forms.
func interactionResponseFormat(o options) (map[string]any, error) {
	rf := map[string]any{}
	if v, ok := o["response_format"]; ok && v != nil {
		switch t := v.(type) {
		case map[string]any:
			typ := strings.ToLower(strOf(t["type"]))
			switch typ {
			case "text", "image", "video":
				for k, e := range t {
					rf[k] = cloneJSON(e)
				}
			case "json_object", "json_schema", "json":
				rf["type"] = "text"
				rf["mime_type"] = "application/json"
				src := t
				if js, ok := t["json_schema"].(map[string]any); ok {
					src = js
				}
				if s, ok := src["schema"]; ok {
					rf["schema"] = cloneJSON(s)
				}
			default:
				return nil, optionError("response_format", "unknown type "+fmt.Sprintf("%q", typ))
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "json", "json_object":
				rf["type"] = "text"
				rf["mime_type"] = "application/json"
			case "text", "image", "video":
				rf["type"] = strings.ToLower(strings.TrimSpace(t))
			default:
				return nil, optionError("response_format", "unknown format")
			}
		default:
			return nil, optionError("response_format", "must be a string or an object")
		}
	}
	if s, ok := o.str("aspect_ratio"); ok {
		if _, set := rf["aspect_ratio"]; !set {
			rf["aspect_ratio"] = s
		}
	}
	if s, ok := o.str("image_size"); ok {
		if _, set := rf["image_size"]; !set {
			rf["image_size"] = s
		}
	}
	if len(rf) == 0 {
		return nil, nil
	}
	typ, _ := rf["type"].(string)
	if typ == "" {
		typ = "text"
		rf["type"] = typ
	}
	// Drop values outside the sets each type accepts.
	if ar, ok := rf["aspect_ratio"].(string); ok {
		valid := typ == "image" && interactionImageRatios[ar] || typ == "video" && interactionVideoRatios[ar]
		if !valid {
			delete(rf, "aspect_ratio")
		}
	} else {
		delete(rf, "aspect_ratio")
	}
	if sz, ok := rf["image_size"].(string); ok {
		n := strings.ToUpper(strings.TrimSpace(sz))
		if typ == "image" && interactionImageSizes[n] {
			rf["image_size"] = n
		} else {
			delete(rf, "image_size")
		}
	} else {
		delete(rf, "image_size")
	}
	if mt, ok := rf["mime_type"].(string); ok && !interactionMIMEs[strings.ToLower(mt)] {
		delete(rf, "mime_type")
	}
	if typ == "video" {
		rf["delivery"] = "uri"
	}
	return rf, nil
}

func interactionGenerationConfig(o options) (map[string]any, error) {
	gc := map[string]any{}
	user, err := o.requireObject("generation_config")
	if err != nil {
		return nil, err
	}
	for k, v := range user {
		gc[k] = cloneJSON(v)
	}
	set := func(key string, v any) {
		if _, exists := gc[key]; !exists {
			gc[key] = v
		}
	}
	if f, ok := o.float("temperature"); ok {
		set("temperature", f)
	}
	if f, ok := o.float("top_p"); ok {
		set("top_p", f)
	}
	if n, ok := o.positiveInt("max_output_tokens", "max_tokens", "max_completion_tokens"); ok {
		set("max_output_tokens", n)
	}
	if s, ok := o.str("thinking_level"); ok {
		set("thinking_level", s)
	}
	if s, ok := o.str("thinking_summaries"); ok {
		set("thinking_summaries", s)
	}
	if n, ok := o.int("seed"); ok {
		set("seed", n)
	}
	if l := o.stringList("stop_sequences", "stop"); len(l) > 0 {
		set("stop_sequences", l)
	}
	if task, ok := o.str("video_task", "task"); ok {
		vc, _ := gc["video_config"].(map[string]any)
		if vc == nil {
			vc = map[string]any{}
		}
		if _, has := vc["task"]; !has {
			vc["task"] = task
		}
		gc["video_config"] = vc
	}
	if vc, ok := gc["video_config"].(map[string]any); ok {
		if t, _ := vc["task"].(string); t != "" && !videoTasks[t] {
			delete(vc, "task")
		}
		if len(vc) == 0 {
			delete(gc, "video_config")
		}
	}
	return gc, nil
}

// ---- parsing --------------------------------------------------------------

func (interactionsAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	if e, ok := m["error"].(map[string]any); ok && e != nil {
		return nil, upstreamFromObject(m, m)
	}
	return parseInteraction(m)
}

func parseInteraction(m map[string]any) (*Result, error) {
	root := m
	if inner, ok := m["interaction"].(map[string]any); ok {
		root = inner
	}
	res := &Result{ID: strOf(root["id"])}
	switch st := strings.ToLower(strOf(root["status"])); st {
	case "failed", "cancelled", "canceled":
		msg, _ := errorFromMap(root)
		if msg == "" {
			msg = "interaction " + st
		}
		return nil, &Error{Kind: KindUpstream, Status: http.StatusBadGateway, Message: msg, Accepted: true}
	case "in_progress", "running", "queued", "pending":
		res.FinishReason = "in_progress"
	}
	var acc interactionAcc
	for _, key := range []string{"outputs", "steps", "output"} {
		if items := asList(root[key]); len(items) > 0 {
			acc.walk(items)
		}
	}
	acc.fill(res)
	var usage map[string]any
	for _, cand := range []any{root["usage"], m["usage"], objField(objField(root, "metadata"), "total_usage"), objField(objField(m, "metadata"), "total_usage")} {
		if u, ok := cand.(map[string]any); ok && u != nil {
			usage = u
			break
		}
	}
	res.Usage = interactionsUsage(usage)
	if res.FinishReason == "" {
		if len(res.ToolCalls) > 0 {
			res.FinishReason = "tool_calls"
		} else {
			res.FinishReason = "stop"
		}
	}
	return res, nil
}

type interactionAcc struct {
	text, thought strings.Builder
	sig           string
	tools         []ToolCall
	images        []Image
	videos        []Video
	servers       []ServerToolCall
	cites         []Citation
	counts        map[string]int
}

func (a *interactionAcc) walk(items []any) {
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		a.item(m)
	}
}

func (a *interactionAcc) count(name string) {
	if a.counts == nil {
		a.counts = map[string]int{}
	}
	a.counts[name]++
}

func (a *interactionAcc) item(m map[string]any) {
	typ := strings.ToLower(strOf(m["type"]))
	switch {
	case typ == "text":
		a.text.WriteString(strOf(m["text"]))
		for _, an := range asList(m["annotations"]) {
			if am, ok := an.(map[string]any); ok {
				a.cites = addCitation(a.cites, Citation{URL: firstNonEmpty(strOf(am["url"]), strOf(am["uri"])), Title: strOf(am["title"])})
			}
		}
	case typ == "thought" || typ == "thinking" || typ == "thought_summary":
		switch s := m["summary"].(type) {
		case string:
			a.thought.WriteString(s)
		case []any:
			for _, e := range s {
				if em, ok := e.(map[string]any); ok {
					a.thought.WriteString(strOf(em["text"]))
				} else if str, ok := e.(string); ok {
					a.thought.WriteString(str)
				}
			}
		default:
			a.thought.WriteString(firstNonEmpty(strOf(m["text"]), strOf(objField(m, "content")["text"])))
		}
		if sg := strOf(m["signature"]); sg != "" {
			a.sig = sg
		}
	case typ == "function_call":
		args, _ := json.Marshal(m["arguments"])
		if m["arguments"] == nil {
			args = []byte("{}")
		}
		a.tools = append(a.tools, ToolCall{ID: firstNonEmpty(strOf(m["id"]), fmt.Sprintf("call_%d", len(a.tools)+1)), Name: strOf(m["name"]),
			Arguments: string(args), ThoughtSignature: strOf(m["signature"])})
	case typ == "google_search_call" || typ == "url_context_call" || typ == "code_execution_call":
		name := strings.TrimSuffix(typ, "_call")
		sc := ServerToolCall{ID: strOf(m["id"]), Type: name, Status: "completed", Detail: rawJSON(m)}
		if args, ok := m["arguments"].(map[string]any); ok {
			for _, q := range asList(args["queries"]) {
				sc.Queries = uniqueAppend(sc.Queries, strOf(q))
			}
		}
		a.count(name)
		a.servers = append(a.servers, sc)
	case typ == "google_search_result" || typ == "url_context_result" || typ == "code_execution_result":
		name := strings.TrimSuffix(typ, "_result")
		sc := ServerToolCall{ID: firstNonEmpty(strOf(m["call_id"]), strOf(m["id"])), Type: name, Status: "completed", Detail: rawJSON(m)}
		for _, r := range asList(m["result"]) {
			if rm, ok := r.(map[string]any); ok {
				cit := Citation{URL: firstNonEmpty(strOf(rm["url"]), strOf(rm["uri"])), Title: strOf(rm["title"])}
				sc.Sources = addCitation(sc.Sources, cit)
				a.cites = addCitation(a.cites, cit)
			}
		}
		a.servers = append(a.servers, sc)
	case typ == "image":
		img := Image{Index: len(a.images), MIME: firstNonEmpty(strOf(m["mime_type"]), "image/png"), URL: firstNonEmpty(strOf(m["uri"]), strOf(m["url"]))}
		if d := strOf(m["data"]); d != "" {
			if data, err := base64.StdEncoding.DecodeString(d); err == nil {
				img.Data = data
			}
		}
		if len(img.Data) > 0 || img.URL != "" {
			a.images = append(a.images, img)
		}
	case typ == "video":
		v := Video{URL: firstNonEmpty(strOf(m["uri"]), strOf(m["url"])), MIME: firstNonEmpty(strOf(m["mime_type"]), "video/mp4")}
		if d := strOf(m["data"]); d != "" {
			if data, err := base64.StdEncoding.DecodeString(d); err == nil {
				v.Data = data
			}
		}
		if secs, ok := parseDurationSeconds(m["duration"]); ok {
			v.DurationSec = secs
		}
		if len(v.Data) > 0 || v.URL != "" {
			v.FileName = "video.mp4"
			a.videos = append(a.videos, v)
		}
	case typ == "model_output":
		a.walk(asList(m["content"]))
	}
}

func (a *interactionAcc) fill(res *Result) {
	res.Text = a.text.String()
	res.Reasoning = a.thought.String()
	res.ReasoningSignature = a.sig
	res.ToolCalls = a.tools
	res.Images = a.images
	res.Videos = a.videos
	res.ServerToolCalls = a.servers
	res.Citations = a.cites
	res.ServerToolUsage = a.counts
}

// generate runs the exchange and, for buffered video or long-running
// interactions, polls the interaction until it finishes.
func (a interactionsAdapter) generate(ctx context.Context, x *exchange, sink Sink) (*Result, error) {
	res, _, err := x.c.exec(ctx, x, a, sink)
	if err != nil || sink != nil || res == nil || res.FinishReason != "in_progress" || res.ID == "" {
		return res, err
	}
	pollURL, err := buildURL(googleBase(x.route.BaseURL), "v1beta", "/interactions/"+url.PathEscape(res.ID))
	if err != nil {
		return nil, err
	}
	for {
		if err := x.c.sleep(ctx, interactionPollDelay); err != nil {
			e := transportErr(err, true)
			return nil, e
		}
		_, body, _, dbg, err := x.c.doBuffered(ctx, x, &call{method: http.MethodGet, url: pollURL}, false)
		if err != nil {
			if e, ok := AsError(err); ok {
				e.Accepted = true
			}
			return nil, err
		}
		var m map[string]any
		if json.Unmarshal(body, &m) != nil {
			return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Accepted: true, Debug: dbg}
		}
		next, perr := parseInteraction(m)
		if perr != nil {
			if e, ok := AsError(perr); ok {
				e.Accepted, e.Debug = true, dbg
			}
			return nil, perr
		}
		if next.FinishReason != "in_progress" {
			if next.Usage.IsZero() {
				next.Usage = res.Usage
			}
			next.Debug = dbg
			return next, nil
		}
	}
}

type interactionStream struct {
	x       *exchange
	frames  int
	id      string
	acc     interactionAcc
	final   *Result
	usage   Usage
	partial map[int]*ToolCall
}

func (interactionsAdapter) stream(x *exchange) streamer { return &interactionStream{x: x} }

func (s *interactionStream) frame(f sseFrame, emit func(Event) error) error {
	s.frames++
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	et := strings.ToLower(firstNonEmpty(strOf(m["event_type"]), strOf(m["type"]), f.Event))
	if inter, ok := m["interaction"].(map[string]any); ok {
		if id := strOf(inter["id"]); id != "" && s.id == "" {
			s.id = id
			if err := emit(Event{Type: EventCreated, ResponseID: id}); err != nil {
				return err
			}
		}
		if strings.Contains(et, "complete") || strings.Contains(et, "done") {
			res, err := parseInteraction(m)
			if err != nil {
				return err
			}
			s.final = res
			if !res.Usage.IsZero() {
				u := res.Usage
				return emit(Event{Type: EventUsage, Usage: &u})
			}
			return nil
		}
	}
	d, _ := m["delta"].(map[string]any)
	if d == nil {
		return nil
	}
	switch dt := strings.ToLower(strOf(d["type"])); dt {
	case "text", "text_delta":
		t := strOf(d["text"])
		if t != "" {
			s.acc.text.WriteString(t)
			return emit(Event{Type: EventText, Text: t})
		}
	case "thought_summary", "thought", "thinking", "thought_delta":
		t := firstNonEmpty(strOf(d["text"]), strOf(objField(d, "content")["text"]), strOf(d["thought"]))
		if t != "" {
			s.acc.thought.WriteString(t)
			return emit(Event{Type: EventReasoning, Text: t})
		}
	case "function_call":
		before := len(s.acc.tools)
		s.acc.item(d)
		if len(s.acc.tools) > before {
			tc := s.acc.tools[len(s.acc.tools)-1]
			return emit(Event{Type: EventToolCall, Index: len(s.acc.tools) - 1, ToolCall: &tc})
		}
	case "image":
		before := len(s.acc.images)
		s.acc.item(d)
		if len(s.acc.images) > before {
			img := s.acc.images[len(s.acc.images)-1]
			return emit(Event{Type: EventImage, Index: img.Index, Image: &img})
		}
	default:
		s.acc.item(d)
	}
	return nil
}

func (s *interactionStream) result() (*Result, error) {
	if s.frames == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream ended before any event", Accepted: true}
	}
	res := &Result{ID: s.id}
	if s.final != nil {
		cp := *s.final
		res = &cp
		if res.ID == "" {
			res.ID = s.id
		}
	}
	var tmp Result
	s.acc.fill(&tmp)
	if res.Text == "" {
		res.Text = tmp.Text
	}
	if res.Reasoning == "" {
		res.Reasoning = tmp.Reasoning
	}
	if len(res.ToolCalls) == 0 {
		res.ToolCalls = tmp.ToolCalls
	}
	if len(res.Images) == 0 {
		res.Images = tmp.Images
	}
	if len(res.Videos) == 0 {
		res.Videos = tmp.Videos
	}
	if len(res.ServerToolCalls) == 0 {
		res.ServerToolCalls = tmp.ServerToolCalls
	}
	if len(res.Citations) == 0 {
		res.Citations = tmp.Citations
	}
	if res.FinishReason == "" || res.FinishReason == "in_progress" || res.FinishReason == "stop" {
		res.FinishReason = "stop"
		if len(res.ToolCalls) > 0 {
			res.FinishReason = "tool_calls"
		}
	}
	return res, nil
}
