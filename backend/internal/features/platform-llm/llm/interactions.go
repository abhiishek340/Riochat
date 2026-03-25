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
