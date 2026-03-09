package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func chatBody(t *testing.T, f *fakeUpstream) map[string]any { return f.last(t).JSON(t) }

func TestChatRequestShape(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleDeveloper, Content: "no emoji"},
			{Role: RoleUser, Content: "hi"},
			{Role: RoleAssistant, Content: "calling", Reasoning: "thinking...", ToolCalls: []ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":1}`}}},
			{Role: RoleTool, ToolResults: []ToolResult{{CallID: "c1", Name: "lookup", Output: `{"ok":true}`}}},
		},
		Instructions: "inst",
		Tools:        []ToolDef{{Name: "lookup", Description: "find", Schema: map[string]any{"properties": map[string]any{"q": map[string]any{"type": "number"}}}}},
		Options: map[string]any{
			"max_output_tokens": float64(300), "temperature": 0.5, "reasoning_effort": "low", "seed": float64(7),
			"stop": []any{"END"}, "frequency_penalty": 0.1, "verbosity": "low", "service_tier": "flex",
			"thinking": true, "enable_thinking": true, "thinking_budget": float64(2048),
			"response_format": map[string]any{"type": "json_schema", "name": "out", "schema": map[string]any{"type": "object"}, "strict": true},
		},
		PromptCacheKey: "pck",
	}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	mustEq(t, b["model"], "gpt-test", "model")
	mustEq(t, b["max_completion_tokens"], "300", "max_completion_tokens")
	mustEq(t, b["temperature"], "0.5", "temperature")
	mustEq(t, b["reasoning_effort"], "low", "reasoning_effort")
	mustEq(t, b["seed"], "7", "seed")
	mustEq(t, at(b, "stop.0"), "END", "stop")
	mustEq(t, b["verbosity"], "low", "verbosity")
	mustEq(t, b["service_tier"], "flex", "unmapped option forwarded")
	mustEq(t, at(b, "thinking.type"), "enabled", "thinking")
	mustEq(t, b["enable_thinking"], true, "enable_thinking")
	mustEq(t, b["thinking_budget"], "2048", "thinking_budget")
	mustEq(t, b["prompt_cache_key"], "pck", "prompt cache key")
	mustEq(t, at(b, "response_format.type"), "json_schema", "response_format type")
	mustEq(t, at(b, "response_format.json_schema.name"), "out", "wrapped schema name")
	mustEq(t, at(b, "response_format.json_schema.strict"), true, "wrapped strict")
	if _, ok := b["max_output_tokens"]; ok {
		t.Fatal("normalized keys must not be copied raw")
	}
	if _, ok := b["stream"]; ok {
		t.Fatal("buffered request must not set stream")
	}
	// Messages: instructions first, then merged system, etc.
	mustEq(t, at(b, "messages.0.role"), "system", "instructions")
	mustEq(t, at(b, "messages.0.content"), "inst\n\nbe brief\n\nno emoji", "instructions and leading system messages merge")
	mustEq(t, at(b, "messages.1.role"), "user", "user")
	mustEq(t, at(b, "messages.2.reasoning_content"), "thinking...", "reasoning echo")
	mustEq(t, at(b, "messages.2.tool_calls.0.function.name"), "lookup", "tool call")
	mustEq(t, at(b, "messages.2.tool_calls.0.function.arguments"), `{"q":1}`, "tool args")
	mustEq(t, at(b, "messages.3.role"), "tool", "tool result role")
	mustEq(t, at(b, "messages.3.tool_call_id"), "c1", "tool_call_id")
	mustEq(t, at(b, "tools.0.function.parameters.type"), "object", "schema gets type")
	mustEq(t, at(b, "tools.0.type"), "function", "tool type")
}

func TestChatOpenRouterReasoningEchoAndNoCacheKey(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	route := routeFor(OpenRouterChat, f, "m")
	req := &Request{PromptCacheKey: "k", Messages: []Message{{Role: RoleUser, Content: "q"}, {Role: RoleAssistant, Content: "a", Reasoning: "why"}}}
	if _, err := testClient(t).Generate(context.Background(), route, req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	mustEq(t, at(b, "messages.1.reasoning"), "why", "openrouter reasoning field")
	if at(b, "messages.1.reasoning_content") != nil || b["prompt_cache_key"] != nil {
		t.Fatalf("%v", b)
	}
}

func TestChatMultimodalParts(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{
		{Kind: PartText, Text: "look"},
		{Kind: PartImage, MIME: "image/png", Data: []byte("IMG")},
		{Kind: PartDocument, Data: []byte("PDF")},
		{Kind: PartAudio, MIME: "audio/mpeg", Data: []byte("AUD")},
		{Kind: PartVideo, MIME: "video/mp4", Data: []byte("VID")},
		{Kind: PartFile, FileName: "a.txt", Text: "file text"},
	}}}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	mustEq(t, at(b, "messages.0.content.0.type"), "text", "text")
	mustEq(t, at(b, "messages.0.content.1.image_url.url"), "data:image/png;base64,SU1H", "image data url")
	mustEq(t, at(b, "messages.0.content.2.file.filename"), "document.pdf", "default pdf name")
	mustEq(t, at(b, "messages.0.content.3.input_audio.format"), "mp3", "audio format from mime")
	mustEq(t, at(b, "messages.0.content.4.video_url.url"), "data:video/mp4;base64,VklE", "video")
	mustEq(t, at(b, "messages.0.content.5.text"), "File: a.txt\nfile text", "file text part")
	if audioFormat("") != "wav" || audioFormat("audio/x-wav") != "wav" || audioFormat("audio") != "wav" {
		t.Fatal("audio format defaults")
	}
}

func TestMessageNormalization(t *testing.T) {
	msgs := normalizeMessages([]Message{{Role: "weird", Content: "x"}, {Role: RoleUser}, {Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "n"}}}})
	if len(msgs) != 2 || msgs[0].Role != RoleUser || msgs[1].Role != RoleAssistant {
		t.Fatalf("%+v", msgs)
	}
	empty := normalizeMessages(nil)
	if len(empty) != 1 || empty[0].Role != RoleUser {
		t.Fatalf("empty lists become one empty user message: %+v", empty)
	}
	f := newUpstream(t, jsonReply(okChat))
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), &Request{}); err != nil {
		t.Fatal(err)
	}
	mustEq(t, at(chatBody(t, f), "messages.0.role"), "user", "empty request still sends a user message")
}

func TestToolResultEncoding(t *testing.T) {
	if got := toolResultOutput(ToolResult{}); got != "{}" {
		t.Fatal(got)
	}
	if got := toolResultOutput(ToolResult{Output: `{"a":1}`}); got != `{"a":1}` {
		t.Fatal(got)
	}
	got := toolResultOutput(ToolResult{IsError: true, Status: "timeout", Error: "too slow"})
	m := decodeMap(t, got)
	if m["ok"] != false || m["status"] != "timeout" || m["error"] != "too slow" {
		t.Fatal(got)
	}
}

func TestChatToolsDisabledAndNativeTools(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{Messages: userMsg("x"), DisableTools: true, Tools: []ToolDef{{Name: "t"}},
		Options: map[string]any{"tools": []any{map[string]any{"type": "web_search"}}, "web_search": true}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	if b["tools"] != nil || b["web_search_options"] != nil {
		t.Fatalf("disabled tools declare nothing: %v", b)
	}
	req.DisableTools = false
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b = chatBody(t, f)
	mustEq(t, at(b, "tools.0.type"), "web_search", "native tools come first")
	mustEq(t, at(b, "tools.1.function.name"), "t", "function tools follow")
}

func TestOptionValidationErrors(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	cases := []struct {
		name string
		opts map[string]any
		want string
	}{
		{"tools not array", map[string]any{"tools": "x"}, `invalid option "tools": must be an array`},
		{"tools entry", map[string]any{"tools": []any{map[string]any{}, "bad"}}, `invalid option "tools[1]": must be an object`},
		{"stream_options", map[string]any{"stream_options": 5}, `invalid option "stream_options": must be an object`},
	}
	for _, tc := range cases {
		_, err := testClient(t).Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("x"), Options: tc.opts})
		if !errors.Is(err, ErrInvalidOption) || err.Error() != tc.want {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if len(f.all()) != 0 {
		t.Fatal("validation errors happen before any network call")
	}
}

func TestProtectedOptionsCannotOverride(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{Messages: userMsg("hello"), Options: map[string]any{
		"model": "evil", "messages": []any{}, "stream": true, "headers": map[string]any{"x": "y"}, "api_key": "k", "base_url": "http://evil",
		"previous_response_id": "p", "prompt": "p", "system": "s", "input": "i", "instructions": "i",
	}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	mustEq(t, b["model"], "gpt-test", "model")
	for _, k := range []string{"stream", "headers", "api_key", "base_url", "previous_response_id", "prompt", "system", "input", "instructions"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s leaked from options", k)
		}
	}
	mustEq(t, at(b, "messages.0.content"), "hello", "messages")
}

func TestNumericOptionRules(t *testing.T) {
	if n, ok := toInt(float64(5)); !ok || n != 5 {
		t.Fatal("integral float")
	}
	if _, ok := toInt(5.5); ok {
		t.Fatal("fractional floats are not integers")
	}
	o := options{"a": 3.0, "b": "3", "c": -1.0}
	if n, ok := o.positiveInt("a"); !ok || n != 3 {
		t.Fatal("a")
	}
	if _, ok := o.int("b"); ok {
		t.Fatal("strings are not integers")
	}
	if _, ok := o.positiveInt("c"); ok {
		t.Fatal("negative")
	}
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{Messages: userMsg("x"), Options: map[string]any{"seed": float64(0), "max_output_tokens": 1.5, "thinking_budget": float64(-1)}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	for _, k := range []string{"seed", "max_completion_tokens", "thinking_budget"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s should have been dropped", k)
		}
	}
}

// ---- streaming ------------------------------------------------------------

func TestChatStreamTextReasoningUsage(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"id":"c9","service_tier":"auto","choices":[{"delta":{"role":"assistant","reasoning_content":"hm"}}]}`),
		": keep-alive comment\n\n",
		ev("", `{"choices":[{"delta":{"content":"Hel"}}]}`),
		ev("", `{"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`),
		ev("", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"completion_tokens_details":{"reasoning_tokens":1}}}`),
		"data: [DONE]\n\n",
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || res.Reasoning != "hm" || res.ID != "c9" || res.FinishReason != "stop" || res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 3 {
		t.Fatalf("%+v", res)
	}
	if len(eventsOf(*evs, EventText)) != 2 || len(eventsOf(*evs, EventReasoning)) != 1 || len(eventsOf(*evs, EventUsage)) != 1 || len(eventsOf(*evs, EventCreated)) != 1 {
		t.Fatalf("%+v", *evs)
	}
	b := chatBody(t, f)
	mustEq(t, b["stream"], true, "stream flag")
	mustEq(t, at(b, "stream_options.include_usage"), true, "include_usage default")
	if f.last(t).Header.Get("Accept") != "text/event-stream" {
		t.Fatal("streaming Accept header")
	}
	if res.Debug == nil || res.Debug.ResponseBody != "" || res.Debug.Status != 200 {
		t.Fatalf("stream debug carries no body: %+v", res.Debug)
	}
}

func TestChatStreamToolCallFragmentsMergeByIndex(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_w","arguments":""}},{"index":1,"id":"call_b","function":{"name":"get_t","arguments":"{\"x\""}}]}}]}`),
		ev("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"eather","arguments":"{\"city\":"}}]}}]}`),
		ev("", `{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":":1}"}},{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`),
		ev("", `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("%+v", res.ToolCalls)
	}
	a, b := res.ToolCalls[0], res.ToolCalls[1]
	if a.ID != "call_a" || a.Name != "get_weather" || a.Arguments != `{"city":"Paris"}` {
		t.Fatalf("%+v", a)
	}
	if b.ID != "call_b" || b.Name != "get_t" || b.Arguments != `{"x":1}` {
		t.Fatalf("%+v", b)
	}
	if res.FinishReason != "tool_calls" || len(eventsOf(*evs, EventToolCall)) < 5 {
		t.Fatalf("%v %d", res.FinishReason, len(eventsOf(*evs, EventToolCall)))
	}
}

func TestChatStreamHugeToolIndexFallsBackToPosition(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"id":"c","choices":[{"delta":{"tool_calls":[{"index":5000,"id":"x","function":{"name":"f","arguments":"{}"}}]}}]}`),
	))
	sink, _ := collect()
	res, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "f" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestChatIncludeUsageRetry(t *testing.T) {
	calls := 0
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		calls++
		if strings.Contains(string(rec.Body), `"include_usage":true`) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"Unknown parameter: stream_options.include_usage"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ev("", `{"id":"c","choices":[{"delta":{"content":"ok"}}]}`))
	})
	sink, _ := collect()
	res, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil || res.Text != "ok" || calls != 2 {
		t.Fatalf("err=%v calls=%d res=%+v", err, calls, res)
	}
	reqs := f.all()
	mustEq(t, at(reqs[1].JSON(t), "stream_options.include_usage"), false, "retry sends include_usage:false")
}

func TestChatIncludeUsageRetryNotWhenUserSetOrAfterEvents(t *testing.T) {
	// User explicitly set include_usage: no transparent retry.
	calls := 0
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		calls++
		w.WriteHeader(422)
		io.WriteString(w, `{"error":{"message":"include_usage not supported"}}`)
	})
	sink, _ := collect()
	req := &Request{Messages: userMsg("x"), Options: map[string]any{"stream_options": map[string]any{"include_usage": true}}}
	_, err := testClient(t).Stream(context.Background(), chatRoute(f), req, sink)
	if err == nil || calls != 1 {
		t.Fatalf("explicit setting: calls=%d err=%v", calls, err)
	}
	// Unrelated 400 mentions nothing: no retry.
	calls = 0
	f2 := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		calls++
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad model"}}`)
	})
	if _, err := testClient(t).Stream(context.Background(), chatRoute(f2), &Request{Messages: userMsg("x")}, sink); err == nil || calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	// After the first event, a failure is returned without retry.
	calls = 0
	f3 := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ev("", `{"id":"c","choices":[{"delta":{"content":"a"}}]}`))
		io.WriteString(w, ev("", `{"error":{"message":"stream_options exploded","code":400}}`))
	})
	_, err = testClient(t).Stream(context.Background(), chatRoute(f3), &Request{Messages: userMsg("x")}, sink)
	if err == nil || calls != 1 {
		t.Fatalf("no retry once events were delivered: calls=%d err=%v", calls, err)
	}
}

func TestStreamFallsBackWhenUpstreamIgnoresStreaming(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil || res.Text != "hi" {
		t.Fatalf("%v %+v", err, res)
	}
	if len(eventsOf(*evs, EventText)) != 1 || len(eventsOf(*evs, EventUsage)) != 1 {
		t.Fatalf("%+v", *evs)
	}
}

func TestChatCitationsAndAnnotations(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"c","citations":["https://a.example","https://b.example"],"choices":[{"message":{"content":"x","annotations":[{"type":"url_citation","url_citation":{"url":"https://a.example","title":"A"}},{"type":"url_citation","url_citation":{"url":"https://c.example"}}]}}]}`))
	res, err := testClient(t).Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")})
	if err != nil || len(res.Citations) != 3 || res.Citations[0].Title != "A" {
		t.Fatalf("%v %+v", err, res.Citations)
	}
}

func TestChatParseVariants(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"c","choices":[{"message":{"content":[{"type":"text","text":"vis"},{"type":"reasoning","text":"hid"}],"reasoning":"extra","tool_calls":[{"id":"t1","type":"custom","custom":{"name":"raw","input":"free text"}},{"id":"t2","type":"function","function":{"name":"f"}}]},"finish_reason":"stop"}]}`))
	res, err := testClient(t).Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "vis" || !strings.Contains(res.Reasoning, "hid") || !strings.Contains(res.Reasoning, "extra") {
		t.Fatalf("reasoning-typed parts are excluded from visible text: %+v", res)
	}
	if len(res.ToolCalls) != 2 || res.ToolCalls[0].Arguments != `{"input":"free text"}` || res.ToolCalls[1].Arguments != "{}" || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res.ToolCalls)
	}
}

func TestChatExplicitPromptCaching(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	req := &Request{Messages: []Message{
		{Role: RoleSystem, Content: "s1"}, {Role: RoleSystem, Content: "s2", CacheHint: true},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "a", CacheHint: true}, {Kind: PartText, Text: "b"}}},
	}, Options: map[string]any{"prompt_cache_options": map[string]any{"mode": "explicit", "ttl": "30m"}}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := chatBody(t, f)
	// Explicit caching disables the leading-system merge.
	mustEq(t, at(b, "messages.0.content"), "s1", "system 1 stays separate")
	mustEq(t, at(b, "messages.1.content.0.prompt_cache_breakpoint.mode"), "explicit", "message hint lands on last part")
	mustEq(t, at(b, "messages.2.content.0.prompt_cache_breakpoint.mode"), "explicit", "part hint")
	if at(b, "messages.2.content.1.prompt_cache_breakpoint") != nil {
		t.Fatal("only hinted parts are marked")
	}
	if b["prompt_cache_options"] != nil {
		t.Fatal("prompt_cache_options is interpreted, not forwarded")
	}
	// A different ttl is not honored.
	f2 := newUpstream(t, jsonReply(okChat))
	req.Options = map[string]any{"prompt_cache_options": map[string]any{"mode": "explicit", "ttl": "1h"}}
	if _, err := testClient(t).Generate(context.Background(), chatRoute(f2), req); err != nil {
		t.Fatal(err)
	}
	mustEq(t, at(chatBody(t, f2), "messages.0.content"), "s1\n\ns2", "unsupported ttl: ordinary behaviour")
}
