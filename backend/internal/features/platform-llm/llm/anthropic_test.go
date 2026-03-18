package llm

import (
	"context"
	"strings"
	"testing"
)

const okAnthropic = `{"id":"msg_1","type":"message","role":"assistant","stop_reason":"tool_use","content":[
 {"type":"thinking","thinking":"hmm","signature":"SIG"},
 {"type":"text","text":" Hello ","citations":[{"type":"web_search_result_location","url":"https://a.example","title":"A"}]},
 {"type":"server_tool_use","id":"srv1","name":"web_search","input":{"query":"go"}},
 {"type":"web_search_tool_result","tool_use_id":"srv1","content":[{"type":"web_search_result","url":"https://b.example","title":"B","encrypted_content":"XXX"}]},
 {"type":"tool_use","id":"tu1","name":"lookup","input":{"q":1}}],
 "usage":{"input_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":70,"output_tokens":12,"server_tool_use":{"web_search_requests":1}}}`

func anthRoute(f *fakeUpstream) Route { return routeFor(AnthropicMessages, f, "claude-test") }

func anthBody(t *testing.T, f *fakeUpstream) map[string]any { return f.last(t).JSON(t) }

func TestAnthropicDefaultsAndParse(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	req := &Request{Messages: userMsg("hi"), Tools: []ToolDef{{Name: "lookup"}}}
	res, err := testClient(t).Generate(context.Background(), anthRoute(f), req)
	if err != nil {
		t.Fatal(err)
	}
	b := anthBody(t, f)
	mustEq(t, b["max_tokens"], "64000", "default max_tokens")
	mustEq(t, at(b, "messages.0.content"), "hi", "plain string content")
	mustEq(t, at(b, "tools.0.input_schema.type"), "object", "input_schema")
	r := f.last(t)
	if r.Path != "/v1/messages" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("%+v", r)
	}
	if res.ID != "msg_1" || res.Text != "Hello" || res.Reasoning != "hmm" || res.ReasoningSignature != "SIG" || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"q":1}` {
		t.Fatalf("%+v", res.ToolCalls)
	}
	if res.Usage.InputTokens != 100 || res.Usage.CacheWriteTokens != 30 || res.Usage.CacheReadTokens != 70 || res.Usage.OutputTokens != 12 {
		t.Fatalf("%+v", res.Usage)
	}
	if res.ServerToolUsage["web_search"] != 1 || len(res.ServerToolCalls) != 2 {
		t.Fatalf("%v %+v", res.ServerToolUsage, res.ServerToolCalls)
	}
	if len(res.Citations) != 2 {
		t.Fatalf("%+v", res.Citations)
	}
	for _, sc := range res.ServerToolCalls {
		if strings.Contains(string(sc.Detail), "XXX") || strings.Contains(string(sc.Detail), "encrypted") {
			t.Fatalf("encrypted content must be stripped: %s", sc.Detail)
		}
	}
}

func TestAnthropicMaxTokensOptions(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	c := testClient(t)
	for opts, want := range map[string]string{"max_output_tokens": "1234", "max_tokens": "777"} {
		req := &Request{Messages: userMsg("x"), Options: map[string]any{opts: float64(map[string]int{"1234": 1234, "777": 777}[want])}}
		if _, err := c.Generate(context.Background(), anthRoute(f), req); err != nil {
			t.Fatal(err)
		}
		mustEq(t, anthBody(t, f)["max_tokens"], want, opts)
	}
}

func TestAnthropicThinkingBudgets(t *testing.T) {
	c := testClient(t)
	f := newUpstream(t, jsonReply(okAnthropic))
	run := func(opts map[string]any) map[string]any {
		t.Helper()
		if _, err := c.Generate(context.Background(), anthRoute(f), &Request{Messages: userMsg("x"), Options: opts}); err != nil {
			t.Fatal(err)
		}
		return anthBody(t, f)
	}
	// Spec scenario: budget 500 with the default 64000 max → 16000.
	b := run(map[string]any{"thinking": true, "budget_tokens": float64(500)})
	mustEq(t, at(b, "thinking.type"), "enabled", "type")
	mustEq(t, at(b, "thinking.budget_tokens"), "16000", "default max_tokens/4")
	// Small max_tokens → 1024.
	b = run(map[string]any{"thinking": true, "budget_tokens": float64(500), "max_output_tokens": float64(2000)})
	mustEq(t, at(b, "thinking.budget_tokens"), "1024", "floor")
	// A valid budget is kept.
	b = run(map[string]any{"thinking": true, "budget_tokens": float64(5000)})
	mustEq(t, at(b, "thinking.budget_tokens"), "5000", "kept")
	// Missing budget.
	b = run(map[string]any{"thinking": true, "max_output_tokens": float64(8000)})
	mustEq(t, at(b, "thinking.budget_tokens"), "2000", "max/4")
	// Never at or above max_tokens.
	b = run(map[string]any{"thinking": true, "budget_tokens": float64(9000), "max_output_tokens": float64(4000)})
	mustEq(t, at(b, "thinking.budget_tokens"), "3999", "below max_tokens")
	// false disables.
	b = run(map[string]any{"thinking": false})
	mustEq(t, at(b, "thinking.type"), "disabled", "disabled")
	// Object and string forms.
	b = run(map[string]any{"thinking": map[string]any{"type": "adaptive"}, "thinking_display": "omitted"})
	mustEq(t, at(b, "thinking.type"), "adaptive", "object")
	mustEq(t, at(b, "thinking.display"), "omitted", "display")
	b = run(map[string]any{"thinking": "disabled"})
	mustEq(t, at(b, "thinking.type"), "disabled", "string")
	// enable_thinking / budget_tokens combinations.
	b = run(map[string]any{"enable_thinking": true, "budget_tokens": float64(2048)})
	mustEq(t, at(b, "thinking.type"), "enabled", "enabled with budget")
	mustEq(t, at(b, "thinking.budget_tokens"), "2048", "budget")
	b = run(map[string]any{"enable_thinking": true})
	mustEq(t, at(b, "thinking.type"), "adaptive", "enabled without budget")
	b = run(map[string]any{"enable_thinking": false, "budget_tokens": float64(4096)})
	mustEq(t, at(b, "thinking.type"), "disabled", "disabled wins")
	b = run(nil)
	if b["thinking"] != nil {
		t.Fatal("no thinking by default")
	}
}

func TestAnthropicCacheBreakpoints(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	c := testClient(t)
	var msgs []Message
	for i := 0; i < 5; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		msgs = append(msgs, Message{Role: role, Parts: []Part{{Kind: PartText, Text: strings.Repeat("m", i+1), CacheHint: true}}})
	}
	if _, err := c.Generate(context.Background(), anthRoute(f), &Request{Messages: msgs, Options: map[string]any{"cache_timeout": "1h"}}); err != nil {
		t.Fatal(err)
	}
	b := anthBody(t, f)
	marked := 0
	var first int = -1
	for i := 0; i < 5; i++ {
		if cc := at(b, "messages."+string(rune('0'+i))+".content.0.cache_control"); cc != nil {
			marked++
			if first < 0 {
				first = i
			}
			mustEq(t, at(b, "messages."+string(rune('0'+i))+".content.0.cache_control.ttl"), "1h", "ttl")
			mustEq(t, at(b, "messages."+string(rune('0'+i))+".content.0.cache_control.type"), "ephemeral", "type")
		}
	}
	if marked != 4 || first != 1 {
		t.Fatalf("five hints → only the last four remain (marked=%d first=%d)", marked, first)
	}
}

func TestAnthropicCacheControls(t *testing.T) {
	c := testClient(t)
	build := func(opts map[string]any, ephemeral bool, msgs []Message) map[string]any {
		f := newUpstream(t, jsonReply(okAnthropic))
		if _, err := c.Generate(context.Background(), anthRoute(f), &Request{Messages: msgs, Options: opts, Ephemeral: ephemeral}); err != nil {
			t.Fatal(err)
		}
		return anthBody(t, f)
	}
	hinted := []Message{{Role: RoleSystem, Content: "sys", CacheHint: true}, {Role: RoleUser, Content: "q", CacheHint: true}}
	b := build(nil, false, hinted)
	if _, ok := b["system"].([]any); !ok {
		t.Fatalf("a cache marker turns system into an array of blocks: %v", b["system"])
	}
	mustEq(t, at(b, "system.0.cache_control.type"), "ephemeral", "system marker")
	mustEq(t, at(b, "messages.0.content.0.cache_control.type"), "ephemeral", "message hint")
	for name, tc := range map[string]struct {
		opts map[string]any
		eph  bool
	}{"enable_cache false": {map[string]any{"enable_cache": false}, false}, "prompt_cache false": {map[string]any{"prompt_cache": false}, false}, "ephemeral": {nil, true}} {
		b := build(tc.opts, tc.eph, hinted)
		if s, ok := b["system"].(string); !ok || s != "sys" || at(b, "messages.0.content") != "q" {
			t.Errorf("%s: no markers expected: %v", name, b)
		}
	}
	// Request-level cache_control applies when there are no block markers.
	b = build(map[string]any{"cache_control": map[string]any{"type": "ephemeral"}}, false, userMsg("x"))
	mustEq(t, at(b, "cache_control.type"), "ephemeral", "request-level cache_control")
	b = build(map[string]any{"cache_control": map[string]any{"type": "ephemeral"}}, false, hinted)
	if b["cache_control"] != nil {
		t.Fatal("not applied when explicit markers exist")
	}
	// Dynamic parts are never cached.
	dyn := []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "static"}, {Kind: PartText, Text: "volatile", Dynamic: true, CacheHint: true}}, CacheHint: true}}
	b = build(nil, false, dyn)
	if at(b, "messages.0.content.1.cache_control") != nil {
		t.Fatal("dynamic part must not carry a marker")
	}
	mustEq(t, at(b, "messages.0.content.0.cache_control.type"), "ephemeral", "message hint falls back to the last non-dynamic block")
}

func TestAnthropicMessageRendering(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	req := &Request{Instructions: "inst", Messages: []Message{
		{Role: RoleSystem, Content: "s1"},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "pic"}, {Kind: PartImage, Data: []byte("IMG")}, {Kind: PartDocument, FileName: "a.pdf", Data: []byte("PDF")}}},
		{Role: RoleAssistant, Content: "calling", ToolCalls: []ToolCall{{ID: "t1", Name: "f", Arguments: `{"a":1}`}, {ID: "t2", Name: "g", Arguments: `not json`}}},
		{Role: RoleTool, ToolResults: []ToolResult{{CallID: "t1", Output: "{}"}, {CallID: "t2", IsError: true, Status: "failed", Error: "bad"}}},
	}}
	if _, err := testClient(t).Generate(context.Background(), anthRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := anthBody(t, f)
	mustEq(t, b["system"], "inst\n\ns1", "system lifted and joined")
	mustEq(t, at(b, "messages.0.role"), "user", "role")
	mustEq(t, at(b, "messages.0.content.1.source.media_type"), "image/jpeg", "default image mime")
	mustEq(t, at(b, "messages.0.content.1.source.data"), "SU1H", "base64 data")
	mustEq(t, at(b, "messages.0.content.2.type"), "document", "document block")
	mustEq(t, at(b, "messages.0.content.2.title"), "a.pdf", "title")
	mustEq(t, at(b, "messages.1.role"), "assistant", "assistant")
	mustEq(t, at(b, "messages.1.content.1.type"), "tool_use", "tool_use")
	mustEq(t, at(b, "messages.1.content.1.input.a"), "1", "parsed input")
	mustEq(t, at(b, "messages.1.content.2.input.arguments"), "not json", "raw fallback")
	mustEq(t, at(b, "messages.2.role"), "user", "tool results are user turns")
	mustEq(t, at(b, "messages.2.content.0.type"), "tool_result", "tool_result")
	mustEq(t, at(b, "messages.2.content.0.tool_use_id"), "t1", "id")
	mustEq(t, at(b, "messages.2.content.1.is_error"), true, "is_error")
	if at(b, "messages.2.content.0.is_error") != nil {
		t.Fatal("successful results carry no is_error")
	}
}

func TestAnthropicOptionsAndNativeTools(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	req := &Request{Messages: userMsg("x"), Tools: []ToolDef{{Name: "lookup"}}, Options: map[string]any{
		"temperature": 0.3, "top_p": 0.9, "top_k": float64(40), "stop": "STOP", "tool_choice": "lookup", "speed": "fast",
		"web_search": true, "betas": []any{"b1", "b2"}, "output_format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}},
		"effort": "high", "metadata": map[string]any{"user_id": "u"},
		"tools": []any{
			map[string]any{"type": "web_fetch_20260209"},
			map[string]any{"type": "advisor_20260301", "model": "x"},
			map[string]any{"type": "code_execution_20250825", "name": "custom_name"},
			map[string]any{"type": "mcp_toolset", "mcp_server_name": "s"},
		},
	}}
	if _, err := testClient(t).Generate(context.Background(), anthRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := anthBody(t, f)
	mustEq(t, b["temperature"], "0.3", "temperature")
	mustEq(t, b["top_k"], "40", "top_k")
	mustEq(t, at(b, "stop_sequences.0"), "STOP", "stop → stop_sequences")
	mustEq(t, at(b, "tool_choice.type"), "tool", "named tool choice")
	mustEq(t, at(b, "tool_choice.name"), "lookup", "tool name")
	mustEq(t, b["speed"], "fast", "speed")
	mustEq(t, at(b, "output_config.format.type"), "json_schema", "output format")
	mustEq(t, at(b, "output_config.effort"), "high", "effort")
	mustEq(t, at(b, "metadata.user_id"), "u", "passthrough")
	mustEq(t, at(b, "tools.0.name"), "web_fetch", "default name filled")
	mustEq(t, at(b, "tools.0.allowed_callers.0"), "direct", "allowed_callers for _20260209 web tools")
	mustEq(t, at(b, "tools.1.name"), "advisor", "advisor name")
	mustEq(t, at(b, "tools.2.name"), "custom_name", "explicit name kept")
	mustEq(t, at(b, "tools.4.name"), "web_search", "web_search option adds the native tool")
	mustEq(t, at(b, "tools.4.type"), "web_search_20250305", "web search type")
	mustEq(t, at(b, "tools.5.name"), "lookup", "client tool last")
	beta := f.last(t).Header.Get("Anthropic-Beta")
	for _, want := range []string{"b1", "b2", "fast-mode-2026-02-01", "advisor-2026-03-01", "mcp-client-2025-11-20"} {
		if !strings.Contains(beta, want) {
			t.Errorf("beta header %q lacks %s", beta, want)
		}
	}
}

func TestAnthropicBetaMergesWithCustomHeader(t *testing.T) {
	f := newUpstream(t, jsonReply(okAnthropic))
	route := anthRoute(f)
	route.HeadersJSON = `{"anthropic-beta":"custom-beta, b1"}`
	req := &Request{Messages: userMsg("x"), Options: map[string]any{"betas": []any{"b1", "b2"}}}
	if _, err := testClient(t).Generate(context.Background(), route, req); err != nil {
		t.Fatal(err)
	}
	mustEq(t, f.last(t).Header.Get("Anthropic-Beta"), "b1,b2,custom-beta", "union")
}

func TestAnthropicUndeclaredNativeToolUse(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"m","content":[{"type":"tool_use","id":"x","name":"web_search","input":{}}]}`))
	_, err := testClient(t).Generate(context.Background(), anthRoute(f), &Request{Messages: userMsg("x")})
	e, ok := AsError(err)
	if !ok || e.Status != 502 || !strings.Contains(e.Message, `"web_search"`) || !strings.Contains(e.Message, "disable") {
		t.Fatalf("%v", err)
	}
	// Declared as a client tool: allowed.
	if _, err := testClient(t).Generate(context.Background(), anthRoute(f), &Request{Messages: userMsg("x"), Tools: []ToolDef{{Name: "web_search"}}}); err != nil {
		t.Fatalf("a client tool with the same name is fine: %v", err)
	}
}

func TestAnthropicEmptyMessagesAndErrors(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	_, err := testClient(t).Generate(context.Background(), anthRoute(f), &Request{})
	e, _ := AsError(err)
	if e == nil || e.Message != "Overloaded" {
		t.Fatalf("%v", err)
	}
	if len(anthBody(t, f)["messages"].([]any)) != 1 {
		t.Fatal("an empty request still sends one message")
	}
}

func TestAnthropicStream(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("message_start", `{"type":"message_start","message":{"id":"msg_s","usage":{"input_tokens":50,"cache_read_input_tokens":10,"output_tokens":1}}}`),
		ev("ping", `{"type":"ping"}`),
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIGX"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`),
		ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}`),
		ev("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"server_tool_use","id":"srv","name":"web_search"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"go\"}"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":2}`),
		ev("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"tu","name":"lookup","input":{}}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"1}"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":3}`),
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42,"server_tool_use":{"web_search_requests":1}}}`),
		ev("message_stop", `{"type":"message_stop"}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), anthRoute(f), &Request{Messages: userMsg("x"), Tools: []ToolDef{{Name: "lookup"}}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "msg_s" || res.Text != "Hello" || res.Reasoning != "let me" || res.ReasoningSignature != "SIGX" || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"q":1}` {
		t.Fatalf("tool input fragments accumulate by block index: %+v", res.ToolCalls)
	}
	if res.Usage.InputTokens != 50 || res.Usage.CacheReadTokens != 10 || res.Usage.OutputTokens != 42 || res.ServerToolUsage["web_search"] != 1 {
		t.Fatalf("%+v %v", res.Usage, res.ServerToolUsage)
	}
	if len(res.ServerToolCalls) != 1 || res.ServerToolCalls[0].Queries[0] != "go" {
		t.Fatalf("%+v", res.ServerToolCalls)
	}
	if len(eventsOf(*evs, EventText)) != 2 || len(eventsOf(*evs, EventReasoning)) != 1 {
		t.Fatal("events")
	}
	mustEq(t, f.last(t).JSON(t)["stream"], true, "stream flag")
}

func TestAnthropicStreamErrorEvent(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("message_start", `{"type":"message_start","message":{"id":"m","usage":{"input_tokens":1}}}`),
		ev("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
	))
	sink, _ := collect()
	_, err := testClient(t).Stream(context.Background(), anthRoute(f), &Request{Messages: userMsg("x")}, sink)
	e, ok := AsError(err)
	if !ok || e.Status != 502 || e.Message != "Overloaded" || !e.Accepted {
		t.Fatalf("%+v", e)
	}
}
