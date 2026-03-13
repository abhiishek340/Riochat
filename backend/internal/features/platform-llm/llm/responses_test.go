package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const okResponses = `{"id":"resp_1","status":"completed","service_tier":"default","output":[
 {"type":"reasoning","id":"rs1","summary":[{"type":"summary_text","text":"I considered"}],"encrypted_content":"ENC"},
 {"type":"message","role":"assistant","content":[{"type":"output_text","text":"Answer","annotations":[{"type":"url_citation","url":"https://a.example","title":"A"}]}]},
 {"type":"function_call","id":"fc1","call_id":"call_1","name":"lookup","arguments":"{\"q\":1}"},
 {"type":"web_search_call","id":"ws1","status":"completed","action":{"type":"search","query":"go","sources":[{"url":"https://b.example","title":"B"}]}}
],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":40},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10}}}`

func respRoute(f *fakeUpstream) Route { return routeFor(OpenAIResponses, f, "gpt-5") }

func TestResponsesBuild(t *testing.T) {
	f := newUpstream(t, jsonReply(okResponses))
	req := &Request{
		Instructions:       "Be helpful",
		PromptCacheKey:     "pck",
		PreviousResponseID: "resp_0",
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"}, {Role: RoleSystem, Content: "s2"},
			{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "see"}, {Kind: PartImage, MIME: "image/png", Data: []byte("IMG")}, {Kind: PartDocument, Data: []byte("PDF")}}},
			{Role: RoleAssistant, Content: "ok", Parts: []Part{{Kind: PartImage, Data: []byte("DROP")}}, ToolCalls: []ToolCall{{ID: "c1", Name: "lookup"}}},
			{Role: RoleTool, ToolResults: []ToolResult{{CallID: "c1", Output: `{"v":1}`}}},
		},
		Tools: []ToolDef{{Name: "lookup", Description: "d"}},
		Options: map[string]any{
			"max_completion_tokens": float64(500), "temperature": 0.2, "reasoning": map[string]any{"effort": "low", "generate_summary": "auto"},
			"reasoning_effort": "high", "reasoning_summary": "detailed", "verbosity": "low", "response_format": "json",
			"web_search": true, "include": []any{"file_search_call.results", "reasoning.encrypted_content"},
			"service_tier": "priority", "parallel_tool_calls": false, "text": map[string]any{"foo": "bar"},
		},
	}
	res, err := testClient(t).Generate(context.Background(), respRoute(f), req)
	if err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	if f.last(t).Path != "/v1/responses" {
		t.Fatal(f.last(t).Path)
	}
	mustEq(t, b["instructions"], "Be helpful", "instructions only for the official protocol")
	mustEq(t, b["max_output_tokens"], "500", "max_output_tokens")
	mustEq(t, b["previous_response_id"], "resp_0", "previous_response_id")
	mustEq(t, b["prompt_cache_key"], "pck", "prompt_cache_key")
	mustEq(t, at(b, "reasoning.effort"), "high", "explicit effort overrides object")
	mustEq(t, at(b, "reasoning.summary"), "detailed", "reasoning summary")
	mustEq(t, at(b, "reasoning.generate_summary"), "auto", "object keys preserved")
	mustEq(t, at(b, "text.format.type"), "json_object", "response format")
	mustEq(t, at(b, "text.verbosity"), "low", "verbosity")
	mustEq(t, at(b, "text.foo"), "bar", "text object merged")
	mustEq(t, b["service_tier"], "priority", "passthrough")
	mustEq(t, b["parallel_tool_calls"], false, "passthrough false")
	mustEq(t, at(b, "tools.0.type"), "web_search", "native web search first")
	mustEq(t, at(b, "tools.1.type"), "function", "function tool")
	mustEq(t, at(b, "tools.1.name"), "lookup", "flat function form")
	mustEq(t, at(b, "tools.1.parameters.type"), "object", "default schema")
	inc := b["include"].([]any)
	if len(inc) != 3 || inc[0] != "reasoning.encrypted_content" || inc[1] != "web_search_call.action.sources" || inc[2] != "file_search_call.results" {
		t.Fatalf("include: %v", inc)
	}
	if _, ok := b["store"]; ok {
		t.Fatal("no store flag for ordinary requests")
	}
	mustEq(t, at(b, "input.0.role"), "system", "merged system")
	mustEq(t, at(b, "input.0.content.0.text"), "s1\n\ns2", "leading systems merged")
	mustEq(t, at(b, "input.1.content.0.type"), "input_text", "user text type")
	mustEq(t, at(b, "input.1.content.1.type"), "input_image", "image block")
	mustEq(t, at(b, "input.1.content.1.image_url"), "data:image/png;base64,SU1H", "image data url")
	mustEq(t, at(b, "input.1.content.2.type"), "input_file", "document block")
	mustEq(t, at(b, "input.1.content.2.filename"), "document.pdf", "default document name")
	mustEq(t, at(b, "input.2.content.0.type"), "output_text", "assistant text type")
	if n := len(at(b, "input.2.content").([]any)); n != 1 {
		t.Fatalf("images in assistant messages are dropped, have %d blocks", n)
	}
	mustEq(t, at(b, "input.3.type"), "function_call", "function call item")
	mustEq(t, at(b, "input.3.call_id"), "c1", "call id")
	mustEq(t, at(b, "input.3.arguments"), "{}", "default arguments")
	mustEq(t, at(b, "input.4.type"), "function_call_output", "tool result item")
	mustEq(t, at(b, "input.4.output"), `{"v":1}`, "tool output")

	if res.ID != "resp_1" || res.Text != "Answer" || res.Reasoning != "I considered" || res.ReasoningSignature != "ENC" {
		t.Fatalf("%+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" || res.ToolCalls[0].Arguments != `{"q":1}` || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res.ToolCalls)
	}
	if len(res.Citations) != 2 || res.Citations[0].URL != "https://a.example" || res.Citations[1].URL != "https://b.example" {
		t.Fatalf("citations %+v", res.Citations)
	}
	if len(res.ServerToolCalls) != 1 || res.ServerToolCalls[0].Type != "web_search" || res.ServerToolCalls[0].Queries[0] != "go" || res.ServerToolUsage["web_search"] != 1 {
		t.Fatalf("%+v %v", res.ServerToolCalls, res.ServerToolUsage)
	}
	if res.Usage.InputTokens != 60 || res.Usage.CacheReadTokens != 40 || res.Usage.OutputTokens != 20 || res.Usage.ReasoningTokens != 10 || res.ServiceTier != "default" {
		t.Fatalf("%+v", res.Usage)
	}
}

func TestResponsesEphemeralBackgroundAndStatefulRules(t *testing.T) {
	f := newUpstream(t, jsonReply(okResponses))
	c := testClient(t)
	base := func() *Request {
		return &Request{Messages: userMsg("x"), PreviousResponseID: "p", PromptCacheKey: "k"}
	}
	r := base()
	r.Ephemeral, r.Background = true, true
	if _, err := c.Generate(context.Background(), respRoute(f), r); err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	mustEq(t, b["store"], false, "ephemeral store:false")
	for _, k := range []string{"previous_response_id", "prompt_cache_key", "background"} {
		if _, ok := b[k]; ok {
			t.Errorf("ephemeral requests ignore %s", k)
		}
	}
	r = base()
	r.Background = true
	if _, err := c.Generate(context.Background(), respRoute(f), r); err != nil {
		t.Fatal(err)
	}
	b = f.last(t).JSON(t)
	mustEq(t, b["background"], true, "background")
	mustEq(t, b["store"], true, "background implies store")
	// Other protocols ignore stateful continuation and background.
	for _, p := range []Protocol{XAIResponses, OpenRouterResponses} {
		r = base()
		r.Background = true
		if _, err := c.Generate(context.Background(), routeFor(p, f, "m"), r); err != nil {
			t.Fatal(err)
		}
		b = f.last(t).JSON(t)
		for _, k := range []string{"previous_response_id", "prompt_cache_key", "background", "instructions"} {
			if _, ok := b[k]; ok {
				t.Errorf("%s must not send %s", p, k)
			}
		}
	}
}

func TestResponsesXAIAndOpenRouterSpecifics(t *testing.T) {
	f := newUpstream(t, jsonReply(okResponses))
	c := testClient(t)
	req := &Request{Instructions: "inst", Messages: []Message{{Role: RoleUser, Content: "q"}, {Role: RoleAssistant, Content: "a", ToolCalls: []ToolCall{{ID: "call 1", Name: "f"}}},
		{Role: RoleTool, ToolResults: []ToolResult{{CallID: "call 1", Output: "{}"}}}},
		Options: map[string]any{"web_search": true, "metadata": map[string]any{"x": 1}, "instructions": "evil", "prompt": "evil", "top_k": float64(3)}}
	if _, err := c.Generate(context.Background(), routeFor(XAIResponses, f, "grok"), req); err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	if _, ok := b["metadata"]; ok {
		t.Fatal("xai metadata is protected")
	}
	mustEq(t, b["top_k"], "3", "xai passthrough")
	inc := b["include"].([]any)
	if len(inc) != 4 || inc[3] != "code_interpreter_call.outputs" {
		t.Fatalf("xai includes: %v", inc)
	}
	mustEq(t, at(b, "input.0.content.0.text"), "inst", "xai instructions become a system message")

	if _, err := c.Generate(context.Background(), routeFor(OpenRouterResponses, f, "m"), req); err != nil {
		t.Fatal(err)
	}
	b = f.last(t).JSON(t)
	if _, ok := b["store"]; ok {
		t.Fatal("openrouter sends no store")
	}
	mustEq(t, at(b, "input.1.type"), "message", "typed messages")
	mustEq(t, at(b, "input.1.role"), "user", "user")
	mustEq(t, at(b, "input.2.id"), "msg_riochat_2", "assistant id")
	mustEq(t, at(b, "input.2.status"), "completed", "assistant status")
	mustEq(t, at(b, "input.3.id"), "fc_call_1", "sanitized call id")
	mustEq(t, at(b, "input.4.id"), "fc_output_call_1", "output id")
	if b["include"] != nil && len(b["include"].([]any)) > 0 {
		t.Fatalf("openrouter sends no default include: %v", b["include"])
	}
	if sanitizeID("a b/c-d_e") != "a_b_c-d_e" {
		t.Fatal(sanitizeID("a b/c-d_e"))
	}
}

func TestResponsesExplicitPromptCaching(t *testing.T) {
	f := newUpstream(t, jsonReply(okResponses))
	req := &Request{Messages: []Message{
		{Role: RoleSystem, Content: "s", CacheHint: true},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "a"}, {Kind: PartImage, Data: []byte("x"), CacheHint: true}}},
		{Role: RoleAssistant, Content: "answer", CacheHint: true},
	}, Options: map[string]any{"prompt_cache_options": map[string]any{"mode": "explicit"}}}
	if _, err := testClient(t).Generate(context.Background(), respRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	mustEq(t, at(b, "input.0.content.0.prompt_cache_breakpoint.mode"), "explicit", "system hint")
	mustEq(t, at(b, "input.1.content.1.prompt_cache_breakpoint.mode"), "explicit", "image part hint")
	if at(b, "input.1.content.0.prompt_cache_breakpoint") != nil {
		t.Fatal("unhinted part")
	}
	if at(b, "input.2.content.0.prompt_cache_breakpoint") != nil {
		t.Fatal("output_text is not a supported breakpoint type")
	}
}

func TestResponsesParseErrorsAndFallbacks(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"r","status":"failed","error":{"message":"boom","code":"server_error"}}`))
	_, err := testClient(t).Generate(context.Background(), respRoute(f), &Request{Messages: userMsg("x")})
	e, ok := AsError(err)
	if !ok || e.Message != "boom" || !e.Accepted || e.Status != 502 {
		t.Fatalf("%+v", e)
	}
	f2 := newUpstream(t, jsonReply(`{"id":"r","output_text":"flat","tool_calls":[{"id":"t","function":{"name":"n","arguments":"{}"}}],"citations":["https://x.example"],"usage":{"input_tokens":1,"output_tokens":2,"server_side_tool_usage_details":{"x_search_calls":3,"web_search_calls":1}}}`))
	res, err := testClient(t).Generate(context.Background(), routeFor(XAIResponses, f2, "g"), &Request{Messages: userMsg("x")})
	if err != nil || res.Text != "flat" || len(res.ToolCalls) != 1 || res.Citations[0].URL != "https://x.example" ||
		res.ServerToolUsage["x_search"] != 3 || res.ServerToolUsage["web_search"] != 1 {
		t.Fatalf("%v %+v %v", err, res, res.ServerToolUsage)
	}
	if res.Usage.OutputTokens != 2 {
		t.Fatal("xai output stays as reported")
	}
}

func TestResponsesRefusalAndImageGenerationCall(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"r","output":[{"type":"message","content":[{"type":"refusal","refusal":"I can't"}]},{"type":"image_generation_call","id":"ig","status":"completed","result":"SU1H","output_format":"webp","revised_prompt":"rp"},{"type":"custom_tool_call","call_id":"cc","name":"raw","input":"txt"}]}`))
	res, err := testClient(t).Generate(context.Background(), respRoute(f), &Request{Messages: userMsg("x")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "I can't" || len(res.Images) != 1 || string(res.Images[0].Data) != "IMG" || res.Images[0].MIME != "image/webp" || res.Images[0].RevisedPrompt != "rp" {
		t.Fatalf("%+v", res)
	}
	if res.ToolCalls[0].Arguments != `{"input":"txt"}` {
		t.Fatalf("%+v", res.ToolCalls)
	}
	if res.ServerToolUsage["image_generation"] != 1 {
		t.Fatalf("%v", res.ServerToolUsage)
	}
}

// ---- streaming ------------------------------------------------------------

func TestResponsesStreamEvents(t *testing.T) {
	completed := strings.ReplaceAll(okResponses, "\n", "")
	f := newUpstream(t, sseReply(
		ev("response.created", `{"type":"response.created","response":{"id":"resp_1","service_tier":"default"}}`),
		ev("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs1"}}`),
		ev("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"I consid"}`),
		ev("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"ered"}`),
		ev("response.output_item.added", `{"type":"response.output_item.added","item":{"type":"message","id":"m1"}}`),
		ev("response.output_text.delta", `{"type":"response.output_text.delta","delta":"Ans"}`),
		ev("response.output_text.delta", `{"type":"response.output_text.delta","delta":"wer"}`),
		ev("response.output_text.done", `{"type":"response.output_text.done","text":"Answer"}`),
		ev("response.output_item.added", `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc1","call_id":"call_1","name":"lookup","arguments":""}}`),
		ev("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"{\"q\""}`),
		ev("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc1","delta":":1}"}`),
		ev("response.output_item.done", `{"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","call_id":"call_1","name":"lookup","arguments":"{\"q\":1}"}}`),
		ev("response.web_search_call.searching", `{"type":"response.web_search_call.searching","item_id":"ws1"}`),
		ev("response.image_generation_call.partial_image", `{"type":"response.image_generation_call.partial_image","partial_image_b64":"SU1H","partial_image_index":2}`),
		ev("response.completed", `{"type":"response.completed","response":`+completed+`}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), respRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Answer" || res.Reasoning != "I considered" || res.ID != "resp_1" || res.Usage.InputTokens != 60 {
		t.Fatalf("%+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"q":1}` {
		t.Fatalf("%+v", res.ToolCalls)
	}
	if len(eventsOf(*evs, EventText)) != 2 || len(eventsOf(*evs, EventReasoning)) != 2 {
		t.Fatalf("text/reasoning events: %d/%d", len(eventsOf(*evs, EventText)), len(eventsOf(*evs, EventReasoning)))
	}
	imgs := eventsOf(*evs, EventImage)
	if len(imgs) != 1 || !imgs[0].Image.Partial || imgs[0].Image.Index != 2 || string(imgs[0].Image.Data) != "IMG" {
		t.Fatalf("%+v", imgs)
	}
	srv := eventsOf(*evs, EventServerTool)
	if len(srv) != 1 || srv[0].ServerTool.Type != "web_search" || srv[0].ServerTool.Status != "searching" {
		t.Fatalf("%+v", srv)
	}
	if len(eventsOf(*evs, EventUsage)) != 1 || len(eventsOf(*evs, EventCreated)) != 1 {
		t.Fatal("usage/created events")
	}
	tc := eventsOf(*evs, EventToolCall)
	if len(tc) != 3 || tc[0].ToolCall.Name != "lookup" || tc[1].ToolCall.Arguments != `{"q"` {
		t.Fatalf("%+v", tc)
	}
}

func TestResponsesStreamWithoutCompletedEvent(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"type":"response.created","response":{"id":"r"}}`),
		ev("", `{"type":"response.refusal.delta","delta":"No."}`),
		ev("", `{"type":"response.custom_tool_call_input.delta","item_id":"c1","delta":"abc"}`),
		ev("", `{"type":"response.output_item.done","item":{"type":"custom_tool_call","id":"c1","call_id":"cc","name":"raw","input":"abc"}}`),
	))
	sink, _ := collect()
	res, err := testClient(t).Stream(context.Background(), respRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil || res.Text != "No." || len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"input":"abc"}` || res.ToolCalls[0].ID != "cc" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestResponsesStreamFailed(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"type":"response.created","response":{"id":"r"}}`),
		ev("", `{"type":"response.failed","response":{"error":{"message":"quota","code":429}}}`),
	))
	sink, _ := collect()
	_, err := testClient(t).Stream(context.Background(), respRoute(f), &Request{Messages: userMsg("x")}, sink)
	e, ok := AsError(err)
	if !ok || e.Status != 429 || e.Message != "quota" || !e.Accepted {
		t.Fatalf("%+v", e)
	}
}

func TestStreamEmptyBody(t *testing.T) {
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "")
	})
	sink, _ := collect()
	_, err := testClient(t).Stream(context.Background(), respRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err == nil || !IsAccepted(err) {
		t.Fatalf("a stream with no events is an accepted failure: %v", err)
	}
}

func TestBackgroundRetrieveAndCancel(t *testing.T) {
	f := newUpstream(t, jsonReply(okResponses))
	c := testClient(t)
	res, err := c.RetrieveResponse(context.Background(), respRoute(f), "resp_1")
	if err != nil || res.Text != "Answer" {
		t.Fatalf("%v", err)
	}
	r := f.last(t)
	if r.Method != "GET" || r.Path != "/v1/responses/resp_1" {
		t.Fatalf("%+v", r)
	}
	if _, err := c.CancelResponse(context.Background(), respRoute(f), "resp_1"); err != nil {
		t.Fatal(err)
	}
	r = f.last(t)
	if r.Method != "POST" || r.Path != "/v1/responses/resp_1/cancel" {
		t.Fatalf("%+v", r)
	}
	if _, err := c.RetrieveResponse(context.Background(), respRoute(f), "../x"); err == nil {
		t.Fatal("invalid ids are rejected")
	}
}
