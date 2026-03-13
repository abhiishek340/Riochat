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
