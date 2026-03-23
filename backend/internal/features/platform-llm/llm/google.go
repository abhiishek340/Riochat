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
