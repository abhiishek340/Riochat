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
