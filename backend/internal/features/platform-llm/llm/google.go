package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// googleAdapter serves google_generate_content and google_image_generation.
type googleAdapter struct{}

func (googleAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	req := x.req
	image := x.proto == GoogleImageGeneration

	native, err := o.nativeTools()
	if err != nil {
		return nil, err
	}
	gcOpt, err := o.requireObject("generationConfig")
	if err != nil {
		return nil, err
	}
	toolCfgOpt, err := o.requireObject("toolConfig")
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if image {
		contents, err := googleImageContents(req)
		if err != nil {
			return nil, err
		}
		body["contents"] = contents
	} else {
		msgs := normalizeMessages(req.Messages)
		var sys []any
		if strings.TrimSpace(req.Instructions) != "" {
			sys = append(sys, map[string]any{"text": req.Instructions})
		}
		var contents []map[string]any
		for _, m := range msgs {
			if m.Role == RoleSystem || m.Role == RoleDeveloper {
				for _, p := range effectiveParts(m) {
					if t, ok := partAsText(p); ok {
						sys = append(sys, map[string]any{"text": t})
					}
				}
				continue
			}
			role := "user"
			if m.Role == RoleAssistant {
				role = "model"
			}
			parts := googleParts(m)
			if len(parts) == 0 {
				continue
			}
			if n := len(contents); n > 0 && contents[n-1]["role"] == role {
				contents[n-1]["parts"] = append(contents[n-1]["parts"].([]any), parts...)
				continue
			}
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
		if len(contents) == 0 {
			contents = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"text": " "}}})
		}
		arr := make([]any, len(contents))
		for i, c := range contents {
			arr[i] = c
		}
		body["contents"] = arr
		if len(sys) > 0 {
			body["systemInstruction"] = map[string]any{"parts": sys}
		}
	}

	gc, err := googleGenerationConfig(o, gcOpt, image)
	if err != nil {
		return nil, err
	}
	if len(gc) > 0 {
		body["generationConfig"] = gc
	}

	// Tools.
	var tools []any
	serverTool := false
	if !req.DisableTools {
		hasSearch := false
		for _, t := range native {
			nt := normalizeGoogleNative(t)
			if _, ok := nt["google_search"]; ok {
				hasSearch = true
			}
			if _, ok := nt["googleSearch"]; ok {
				hasSearch = true
			}
			serverTool = true
			tools = append(tools, nt)
		}
		if ws, _ := o.boolean("web_search"); ws && !hasSearch {
			tools = append(tools, map[string]any{"google_search": map[string]any{}})
			serverTool = true
		}
		if !image && len(req.Tools) > 0 {
			decls := make([]any, 0, len(req.Tools))
			for _, td := range req.Tools {
				d := map[string]any{"name": td.Name, "parametersJsonSchema": toolSchema(td.Schema)}
				if td.Description != "" {
					d["description"] = td.Description
				}
				decls = append(decls, d)
			}
			tools = append(tools, map[string]any{"functionDeclarations": decls})
		}
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	toolCfg := map[string]any{}
	for k, v := range toolCfgOpt {
		toolCfg[k] = cloneJSON(v)
	}
	if v, ok := o["tool_config"].(map[string]any); ok {
		for k, e := range v {
			if _, set := toolCfg[k]; !set {
				toolCfg[k] = cloneJSON(e)
			}
		}
	}
	if serverTool {
		toolCfg["includeServerSideToolInvocations"] = true
	}
	if len(toolCfg) > 0 {
		body["toolConfig"] = toolCfg
	}
	for _, pair := range [][2]string{{"safetySettings", "safety_settings"}, {"cachedContent", "cached_content"}, {"serviceTier", "service_tier"}, {"store", "store"}} {
		if v, _, ok := o.first(pair[0], pair[1]); ok {
			body[pair[0]] = cloneJSON(v)
		}
	}

	model := googleModelPath(x.model)
	endpoint := "/" + model + ":generateContent"
	if x.stream {
		endpoint = "/" + model + ":streamGenerateContent"
	}
	u, err := buildURL(googleBase(x.route.BaseURL), "v1beta", endpoint)
	if err != nil {
		return nil, err
	}
	if x.stream {
		u += "?alt=sse"
	}
	return jsonCall(http.MethodPost, u, body)
}

func googleParts(m Message) []any {
	var parts []any
	for _, tr := range m.ToolResults {
		resp := map[string]any{"content": tr.Output}
		if tr.IsError {
			msg := tr.Error
			if msg == "" {
				msg = tr.Status
			}
			resp["error"] = msg
		}
		parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": tr.Name, "response": resp}})
	}
	for _, p := range effectiveParts(m) {
		if t, ok := partAsText(p); ok {
			parts = append(parts, map[string]any{"text": t})
			continue
		}
		if len(p.Data) == 0 {
			continue
		}
		mime := p.MIME
		switch p.Kind {
		case PartImage:
			mime = imageMIME(p.MIME)
		case PartDocument:
			mime = "application/pdf"
		case PartAudio, PartVideo:
			if mime == "" {
				mime = "application/octet-stream"
			}
		default:
			continue
		}
		parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": b64(p.Data)}})
	}
	if m.Role == RoleAssistant {
		for _, tc := range m.ToolCalls {
			part := map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": parseArgsObject(tc.Arguments)}}
			if tc.ThoughtSignature != "" {
				part["thoughtSignature"] = tc.ThoughtSignature
			}
			parts = append(parts, part)
		}
	}
	return parts
}

func normalizeGoogleNative(t map[string]any) map[string]any {
	nt := cloneJSON(t).(map[string]any)
	typ, _ := nt["type"].(string)
	if typ == "" {
		return nt
	}
	delete(nt, "type")
	key := typ
	switch strings.ToLower(typ) {
	case "googlesearch", "google_search":
		key = "google_search"
	}
	if _, exists := nt[key]; exists {
		return nt
	}
	return map[string]any{key: nt}
}

// imagePrompt returns the prompt text for image protocols: the last user
// message's text (file text appended), else the last message with text.
func imagePrompt(msgs []Message) string {
	text := func(m Message) string {
		var parts []string
		for _, p := range effectiveParts(m) {
			if t, ok := partAsText(p); ok {
				parts = append(parts, t)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if normalizeRole(msgs[i].Role) == RoleUser {
			if t := text(msgs[i]); t != "" {
				return t
			}
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if t := text(msgs[i]); t != "" {
			return t
		}
	}
	return ""
}

func inputImages(msgs []Message) []Part {
	var out []Part
	for _, m := range msgs {
		for _, p := range effectiveParts(m) {
			if p.Kind == PartImage && len(p.Data) > 0 {
				out = append(out, p)
			}
		}
	}
	return out
}

func googleImageContents(req *Request) ([]any, error) {
	prompt := imagePrompt(req.Messages)
	if prompt == "" {
		return nil, invalid(ErrInvalidRequest, "image generation requires a prompt")
	}
	parts := []any{map[string]any{"text": prompt}}
	for _, im := range inputImages(req.Messages) {
		parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": imageMIME(im.MIME), "data": b64(im.Data)}})
	}
	return []any{map[string]any{"role": "user", "parts": parts}}, nil
}

var googleAspectRatios = map[string]bool{
	"1:1": true, "2:3": true, "3:2": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true,
	"9:16": true, "16:9": true, "21:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true,
}

func normalizeImageSize(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	switch u {
	case "512", "0.5K", "512PX":
		return "512"
	case "1K", "2K", "4K":
		return u
	}
	return ""
}

func googleGenerationConfig(o options, userObj map[string]any, image bool) (map[string]any, error) {
	gc := map[string]any{}
	if n, ok := o.int("maxOutputTokens", "max_output_tokens", "max_tokens", "max_completion_tokens"); ok && n > 0 {
		gc["maxOutputTokens"] = n
	}
	if f, ok := o.float("temperature"); ok {
		gc["temperature"] = f
	}
	if f, ok := o.float("topP", "top_p"); ok {
		gc["topP"] = f
	}
	if n, ok := o.positiveInt("topK", "top_k"); ok {
		gc["topK"] = n
	}
	if l := o.stringList("stopSequences", "stop_sequences", "stop"); len(l) > 0 {
		gc["stopSequences"] = l
	}
	if n, ok := o.positiveInt("candidateCount", "candidate_count", "n"); ok {
		gc["candidateCount"] = n
	}
	if n, ok := o.int("seed"); ok {
		gc["seed"] = n
	}
	if f, ok := o.float("presencePenalty", "presence_penalty"); ok {
		gc["presencePenalty"] = f
	}
	if f, ok := o.float("frequencyPenalty", "frequency_penalty"); ok {
		gc["frequencyPenalty"] = f
	}
	if b, ok := o.boolean("responseLogprobs", "response_logprobs"); ok {
		gc["responseLogprobs"] = b
	}
	if n, ok := o.int("logprobs"); ok && n > 0 {
		gc["logprobs"] = n
	}
	if l := o.stringList("responseModalities", "response_modalities"); len(l) > 0 {
		up := make([]string, len(l))
		for i, s := range l {
			up[i] = strings.ToUpper(s)
		}
		gc["responseModalities"] = up
	} else if image {
		gc["responseModalities"] = []string{"TEXT", "IMAGE"}
	}
	for _, pair := range [][2]string{{"speechConfig", "speech_config"}, {"mediaResolution", "media_resolution"}} {
		if v, _, ok := o.first(pair[0], pair[1]); ok {
			gc[pair[0]] = cloneJSON(v)
		}
	}
	ic := map[string]any{}
	if v, _, ok := o.first("imageConfig", "image_config"); ok {
		m, isMap := v.(map[string]any)
		if !isMap {
			return nil, optionError("imageConfig", "must be an object")
		}
		for k, e := range m {
			ic[k] = cloneJSON(e)
		}
	}
	if s, ok := o.str("aspectRatio", "aspect_ratio"); ok && googleAspectRatios[s] {
		ic["aspectRatio"] = s
	}
	if s, ok := o.str("imageSize", "image_size"); ok {
		if n := normalizeImageSize(s); n != "" {
			ic["imageSize"] = n
		}
	}
	if len(ic) > 0 {
		gc["imageConfig"] = ic
	}
	// Response MIME type and schema.
	rf, err := o.responseFormat()
	if err != nil {
		return nil, err
	}
	if s, ok := o.str("responseMimeType", "response_mime_type"); ok {
		gc["responseMimeType"] = s
	} else if rf != nil && (rf.Type == "json_object" || rf.Type == "json_schema") {
		gc["responseMimeType"] = "application/json"
	}
	if v, _, ok := o.first("responseSchema", "response_schema"); ok {
		gc["responseSchema"] = cloneJSON(v)
		gc["responseMimeType"] = "application/json"
	}
	if v, _, ok := o.first("responseJsonSchema", "response_json_schema"); ok {
		gc["responseJsonSchema"] = cloneJSON(v)
		gc["responseMimeType"] = "application/json"
	} else if rf != nil && rf.Type == "json_schema" && rf.Schema != nil {
		gc["responseJsonSchema"] = cloneJSON(rf.Schema)
		gc["responseMimeType"] = "application/json"
	}
	// Thinking.
	tc := map[string]any{}
	if v, _, ok := o.first("thinkingConfig", "thinking_config"); ok {
		m, isMap := v.(map[string]any)
		if !isMap {
			return nil, optionError("thinkingConfig", "must be an object")
		}
		for k, e := range m {
			tc[k] = cloneJSON(e)
		}
	}
	if b, ok := o.boolean("includeThoughts", "include_thoughts"); ok {
		tc["includeThoughts"] = b
	}
	if n, ok := o.int("thinkingBudget", "thinking_budget"); ok {
		tc["thinkingBudget"] = n
	}
	if s, ok := o.str("thinkingLevel", "thinking_level"); ok {
		tc["thinkingLevel"] = s
	}
	if v, ok := o["thinking"]; ok {
		switch t := v.(type) {
		case bool:
			tc["includeThoughts"] = t
		case string:
			if b, known := thinkingTypeFlag(t); known {
				tc["includeThoughts"] = b
			}
		case map[string]any:
			if b, known := thinkingTypeFlag(strOf(t["type"])); known {
				tc["includeThoughts"] = b
			}
			if n, ok := toInt(t["budget_tokens"]); ok {
				if _, set := tc["thinkingBudget"]; !set {
					tc["thinkingBudget"] = n
				}
			}
		}
	}
	if len(tc) > 0 {
		gc["thinkingConfig"] = tc
	}
	// The generationConfig option fills whatever the aliases did not set.
	for k, v := range userObj {
		mergeInto(gc, k, v)
	}
	return gc, nil
}

func thinkingTypeFlag(t string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "enabled", "auto":
		return true, true
	case "disabled", "none":
		return false, true
	}
	return false, false
}

// ---- parsing --------------------------------------------------------------

type googleAcc struct {
	id        string
	text      strings.Builder
	thought   strings.Builder
	sig       string
	tools     []ToolCall
	images    []Image
	cites     []Citation
	search    *ServerToolCall
	urlCtx    *ServerToolCall
	codeRuns  int
	codeCalls []ServerToolCall
	usage     Usage
	finish    string
	blocked   string
	frames    int
}

func (a *googleAcc) add(m map[string]any, emit func(Event) error) error {
	a.frames++
	if id := strOf(m["responseId"]); id != "" && a.id == "" {
		a.id = id
		if emit != nil {
			if err := emit(Event{Type: EventCreated, ResponseID: id}); err != nil {
				return err
			}
		}
	}
	if pf, ok := m["promptFeedback"].(map[string]any); ok {
		if br := strOf(pf["blockReason"]); br != "" {
			a.blocked = br
		}
	}
	if um, ok := m["usageMetadata"].(map[string]any); ok {
		mergeUsage(&a.usage, geminiUsage(um))
		if emit != nil {
			cp := a.usage
			if err := emit(Event{Type: EventUsage, Usage: &cp}); err != nil {
				return err
			}
		}
	}
	cands := asList(m["candidates"])
	if len(cands) == 0 {
		return nil
	}
	cand, _ := cands[0].(map[string]any)
	if fr := strOf(cand["finishReason"]); fr != "" {
		a.finish = fr
	}
	content, _ := cand["content"].(map[string]any)
	for _, p := range asList(content["parts"]) {
		part, _ := p.(map[string]any)
		if part == nil {
			continue
		}
		switch {
		case part["functionCall"] != nil:
			fc, _ := part["functionCall"].(map[string]any)
			args, _ := json.Marshal(fc["args"])
			if fc["args"] == nil {
				args = []byte("{}")
			}
			tc := ToolCall{ID: firstNonEmpty(strOf(fc["id"]), fmt.Sprintf("call_%d", len(a.tools)+1)),
				Name: strOf(fc["name"]), Arguments: string(args), ThoughtSignature: strOf(part["thoughtSignature"])}
			a.tools = append(a.tools, tc)
			if emit != nil {
				c := tc
				if err := emit(Event{Type: EventToolCall, Index: len(a.tools) - 1, ToolCall: &c}); err != nil {
					return err
				}
			}
		case part["inlineData"] != nil:
			id, _ := part["inlineData"].(map[string]any)
			data, err := base64.StdEncoding.DecodeString(strOf(id["data"]))
			if err != nil || len(data) == 0 {
				continue
			}
			img := Image{Index: len(a.images), Data: data, MIME: firstNonEmpty(strOf(id["mimeType"]), "image/png")}
			if strings.HasPrefix(strings.ToLower(img.MIME), "image/") {
				a.images = append(a.images, img)
				if emit != nil {
					c := img
					if err := emit(Event{Type: EventImage, Index: img.Index, Image: &c}); err != nil {
						return err
					}
				}
			}
		case part["executableCode"] != nil:
			a.codeRuns++
			sc := ServerToolCall{ID: fmt.Sprintf("code_%d", a.codeRuns), Type: "code_execution", Status: "completed", Detail: rawJSON(part["executableCode"])}
			a.codeCalls = append(a.codeCalls, sc)
		case part["codeExecutionResult"] != nil:
			if n := len(a.codeCalls); n > 0 {
				a.codeCalls[n-1].Detail = rawJSON(map[string]any{"code": json.RawMessage(a.codeCalls[n-1].Detail), "result": part["codeExecutionResult"]})
			}
		default:
			t, hasText := part["text"].(string)
			if !hasText {
				continue
			}
			if th, _ := part["thought"].(bool); th {
				a.thought.WriteString(t)
				if sg := strOf(part["thoughtSignature"]); sg != "" {
					a.sig = sg
				}
				if emit != nil && t != "" {
					if err := emit(Event{Type: EventReasoning, Text: t}); err != nil {
						return err
					}
				}
			} else {
				a.text.WriteString(t)
				if sg := strOf(part["thoughtSignature"]); sg != "" {
					a.sig = sg
				}
				if emit != nil && t != "" {
					if err := emit(Event{Type: EventText, Text: t}); err != nil {
						return err
					}
				}
			}
		}
	}
	if gm, ok := cand["groundingMetadata"].(map[string]any); ok {
		if a.search == nil {
			a.search = &ServerToolCall{ID: "google_search_1", Type: "google_search", Status: "completed"}
		}
		for _, q := range asList(gm["webSearchQueries"]) {
			a.search.Queries = uniqueAppend(a.search.Queries, strOf(q))
		}
		for _, c := range asList(gm["groundingChunks"]) {
			cm, _ := c.(map[string]any)
			w, _ := cm["web"].(map[string]any)
			if w == nil {
				continue
			}
			cit := Citation{URL: strOf(w["uri"]), Title: strOf(w["title"])}
			a.search.Sources = addCitation(a.search.Sources, cit)
			before := len(a.cites)
			a.cites = addCitation(a.cites, cit)
			if emit != nil && len(a.cites) > before {
				c := cit
				if err := emit(Event{Type: EventCitation, Citation: &c}); err != nil {
					return err
				}
			}
		}
	}
	if um, ok := cand["urlContextMetadata"].(map[string]any); ok {
		if a.urlCtx == nil {
			a.urlCtx = &ServerToolCall{ID: "url_context_1", Type: "url_context", Status: "completed"}
		}
		for _, u := range asList(um["urlMetadata"]) {
			if mm, ok := u.(map[string]any); ok {
				a.urlCtx.Sources = addCitation(a.urlCtx.Sources, Citation{URL: strOf(mm["retrievedUrl"])})
			}
		}
	}
	return nil
}

func (a *googleAcc) result(image bool) (*Result, error) {
	if a.blocked != "" && a.text.Len() == 0 && len(a.tools) == 0 && len(a.images) == 0 {
		return nil, &Error{Kind: KindUpstream, Status: http.StatusBadRequest, Accepted: true,
			Message: "the provider blocked the request: " + a.blocked}
	}
	res := &Result{ID: a.id, Text: a.text.String(), Reasoning: a.thought.String(), ReasoningSignature: a.sig,
		ToolCalls: a.tools, Images: a.images, Citations: a.cites, Usage: a.usage}
	usage := map[string]int{}
	if a.search != nil {
		res.ServerToolCalls = append(res.ServerToolCalls, *a.search)
		n := len(a.search.Queries)
		if n == 0 {
			n = 1
		}
		usage["google_search"] = n
	}
	if a.urlCtx != nil {
		res.ServerToolCalls = append(res.ServerToolCalls, *a.urlCtx)
		usage["url_context"] = len(a.urlCtx.Sources)
		if usage["url_context"] == 0 {
			usage["url_context"] = 1
		}
	}
	if a.codeRuns > 0 {
		res.ServerToolCalls = append(res.ServerToolCalls, a.codeCalls...)
		usage["code_execution"] = a.codeRuns
	}
	if len(usage) > 0 {
		res.ServerToolUsage = usage
	}
	switch strings.ToUpper(a.finish) {
	case "STOP", "":
		res.FinishReason = "stop"
	case "MAX_TOKENS":
		res.FinishReason = "length"
	default:
		res.FinishReason = strings.ToLower(a.finish)
	}
	if len(res.ToolCalls) > 0 {
		res.FinishReason = "tool_calls"
	}
	if image && res.Text != "" {
		for i := range res.Images {
			res.Images[i].RevisedPrompt = strings.TrimSpace(res.Text)
		}
		res.RevisedPrompt = strings.TrimSpace(res.Text)
	}
	return res, nil
}

func (googleAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	if e, ok := m["error"].(map[string]any); ok && e != nil {
		return nil, upstreamFromObject(m, m)
	}
	var acc googleAcc
	if err := acc.add(m, nil); err != nil {
		return nil, err
	}
	return acc.result(x.proto == GoogleImageGeneration)
}

type googleStream struct {
	x   *exchange
	acc googleAcc
}

func (googleAdapter) stream(x *exchange) streamer { return &googleStream{x: x} }

func (s *googleStream) frame(f sseFrame, emit func(Event) error) error {
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	return s.acc.add(m, emit)
}

func (s *googleStream) result() (*Result, error) {
	if s.acc.frames == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream ended before any event", Accepted: true}
	}
	return s.acc.result(s.x.proto == GoogleImageGeneration)
}
