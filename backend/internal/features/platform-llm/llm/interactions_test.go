package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func interRoute(f *fakeUpstream) Route { return routeFor(GeminiInteractions, f, "gemini-3") }

func TestInteractionsRequestShape(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"i1","status":"completed","outputs":[{"type":"text","text":"hi"}],"usage":{"total_input_tokens":10,"total_cached_tokens":4,"total_output_tokens":2,"total_thought_tokens":1}}`))
	c := testClient(t)
	res, err := c.Generate(context.Background(), interRoute(f), &Request{Messages: userMsg("hello"), Instructions: "be nice", PreviousResponseID: "i0",
		Options: map[string]any{"temperature": 0.5, "thinking_level": "low", "web_search": true}})
	if err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1beta/interactions" || r.Header.Get("X-Goog-Api-Key") != "sk-test" {
		t.Fatalf("%+v", r)
	}
	b := r.JSON(t)
	mustEq(t, b["input"], "hello", "a single user text is a string")
	mustEq(t, b["system_instruction"], "be nice", "system instruction")
	mustEq(t, b["previous_interaction_id"], "i0", "continuation")
	mustEq(t, at(b, "generation_config.temperature"), "0.5", "generation config")
	mustEq(t, at(b, "generation_config.thinking_level"), "low", "thinking level")
	mustEq(t, at(b, "tools.0.type"), "google_search", "web search tool")
	if res.Text != "hi" || res.Usage.InputTokens != 6 || res.Usage.CacheReadTokens != 4 || res.Usage.OutputTokens != 2 || res.Usage.ReasoningTokens != 1 {
		t.Fatalf("%+v", res)
	}

	// Multi-turn input becomes steps.
	_, err = c.Generate(context.Background(), interRoute(f), &Request{Messages: []Message{
		{Role: RoleUser, Content: "q"}, {Role: RoleAssistant, Content: "a", ToolCalls: []ToolCall{{ID: "f1", Name: "fn", Arguments: `{"x":1}`}}},
		{Role: RoleTool, ToolResults: []ToolResult{{CallID: "f1", Name: "fn", Output: "ok"}}},
	}, Tools: []ToolDef{{Name: "fn", Description: "d"}}})
	if err != nil {
		t.Fatal(err)
	}
	b = f.last(t).JSON(t)
	mustEq(t, at(b, "input.0.type"), "user_input", "user step")
	mustEq(t, at(b, "input.1.type"), "model_output", "model step")
	mustEq(t, at(b, "input.2.type"), "function_call", "function call step")
	mustEq(t, at(b, "input.2.arguments.x"), "1", "arguments object")
	mustEq(t, at(b, "input.3.type"), "function_result", "function result step")
	mustEq(t, at(b, "tools.0.name"), "fn", "function tool")
	mustEq(t, at(b, "tools.0.parameters.type"), "object", "parameters")
}

func TestInteractionsRequiredFields(t *testing.T) {
	f := newUpstream(t, jsonReply(`{}`))
	r := interRoute(f)
	r.Model = ""
	if _, err := testClient(t).Generate(context.Background(), r, &Request{Messages: userMsg("x")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing model: %v", err)
	}
	if _, err := testClient(t).Generate(context.Background(), interRoute(f), &Request{Messages: []Message{{Role: RoleSystem, Content: "only system"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing input: %v", err)
	}
}

func TestInteractionsResponseFormat(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"id":"i","outputs":[]}`))
	c := testClient(t)
	run := func(opts map[string]any) map[string]any {
		if _, err := c.Generate(context.Background(), interRoute(f), &Request{Messages: userMsg("x"), Options: opts}); err != nil {
			t.Fatal(err)
		}
		return f.last(t).JSON(t)
	}
	b := run(map[string]any{"response_format": map[string]any{"type": "video", "aspect_ratio": "16:9", "image_size": "2K"}, "video_task": "text_to_video"})
	mustEq(t, at(b, "response_format.type"), "video", "type")
	mustEq(t, at(b, "response_format.delivery"), "uri", "video forces uri delivery")
	mustEq(t, at(b, "response_format.aspect_ratio"), "16:9", "video ratio")
	if at(b, "response_format.image_size") != nil {
		t.Fatal("image_size is meaningless for video")
	}
	mustEq(t, at(b, "generation_config.video_config.task"), "text_to_video", "task")
	b = run(map[string]any{"response_format": map[string]any{"type": "video", "aspect_ratio": "1:1"}})
	if at(b, "response_format.aspect_ratio") != nil {
		t.Fatal("1:1 is not a video ratio")
	}
	b = run(map[string]any{"response_format": map[string]any{"type": "image", "aspect_ratio": "21:9", "image_size": "4k", "mime_type": "image/jpeg"}})
	mustEq(t, at(b, "response_format.aspect_ratio"), "21:9", "image ratio")
	mustEq(t, at(b, "response_format.image_size"), "4K", "size")
	b = run(map[string]any{"response_format": map[string]any{"type": "image", "aspect_ratio": "7:3", "image_size": "8K", "mime_type": "image/gif"}})
	for _, k := range []string{"aspect_ratio", "image_size", "mime_type"} {
		if at(b, "response_format."+k) != nil {
			t.Errorf("invalid %s must be dropped", k)
		}
	}
	b = run(map[string]any{"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"schema": map[string]any{"type": "object"}}}})
	mustEq(t, at(b, "response_format.type"), "text", "common json formats map to text")
	mustEq(t, at(b, "response_format.mime_type"), "application/json", "mime")
	mustEq(t, at(b, "response_format.schema.type"), "object", "schema")
	b = run(map[string]any{"video_task": "dance"})
	if at(b, "generation_config.video_config") != nil {
		t.Fatal("unknown video tasks are dropped")
	}
}

func TestInteractionsParseRich(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"interaction":{"id":"i9","status":"completed","steps":[
 {"type":"thought","summary":[{"text":"considering"}],"signature":"SG"},
 {"type":"google_search_call","id":"gs","arguments":{"queries":["q1"]}},
 {"type":"google_search_result","call_id":"gs","result":[{"url":"https://a.example","title":"A"}]},
 {"type":"model_output","content":[{"type":"text","text":"The answer"},{"type":"image","data":"SU1H","mime_type":"image/png"},{"type":"video","uri":"https://v.example/x.mp4","duration":"5.2s"}]},
 {"type":"function_call","id":"fc","name":"f","arguments":{"a":1}}],
 "usage":{"total_input_tokens":3,"total_output_tokens":4}}}`))
	res, err := testClient(t).Generate(context.Background(), interRoute(f), &Request{Messages: userMsg("x")})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "i9" || res.Text != "The answer" || res.Reasoning != "considering" || res.ReasoningSignature != "SG" || len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"a":1}` {
		t.Fatalf("%+v", res)
	}
	if len(res.Images) != 1 || len(res.Videos) != 1 || res.Videos[0].DurationSec != 6 || res.Videos[0].URL != "https://v.example/x.mp4" || res.Videos[0].MIME != "video/mp4" {
		t.Fatalf("%+v %+v", res.Images, res.Videos)
	}
	if len(res.Citations) != 1 || res.ServerToolUsage["google_search"] != 1 || res.Usage.InputTokens != 3 {
		t.Fatalf("%+v", res)
	}
}

func TestInteractionsPollingUntilComplete(t *testing.T) {
	polls := 0
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.Write([]byte(`{"id":"i7","status":"in_progress"}`))
			return
		}
		polls++
		if polls < 2 {
			w.Write([]byte(`{"id":"i7","status":"in_progress"}`))
			return
		}
		w.Write([]byte(`{"id":"i7","status":"completed","outputs":[{"type":"video","uri":"https://v.example/a.mp4"}],"usage":{"total_input_tokens":1}}`))
	})
	res, err := testClient(t).Generate(context.Background(), interRoute(f), &Request{Messages: userMsg("x")})
	if err != nil || len(res.Videos) != 1 || polls != 2 {
		t.Fatalf("%v polls=%d %+v", err, polls, res)
	}
	if p := f.all()[1]; p.Method != "GET" || p.Path != "/v1beta/interactions/i7" {
		t.Fatalf("%+v", p)
	}
	// A failed interaction is an accepted upstream error.
	f2 := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.Method == "POST" {
			w.Write([]byte(`{"id":"i8","status":"in_progress"}`))
			return
		}
		w.Write([]byte(`{"id":"i8","status":"failed","error":{"message":"video blocked"}}`))
	})
	_, err = testClient(t).Generate(context.Background(), interRoute(f2), &Request{Messages: userMsg("x")})
	e, ok := AsError(err)
	if !ok || e.Message != "video blocked" || !e.Accepted {
		t.Fatalf("%v", err)
	}
}

func TestInteractionsStream(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("interaction.start", `{"event_type":"interaction.start","interaction":{"id":"i5","status":"in_progress"}}`),
		ev("content.delta", `{"event_type":"content.delta","index":0,"delta":{"type":"thought_summary","content":{"text":"hmm"}}}`),
		ev("content.delta", `{"event_type":"content.delta","index":1,"delta":{"type":"text","text":"Hel"}}`),
		ev("content.delta", `{"event_type":"content.delta","index":1,"delta":{"type":"text","text":"lo"}}`),
		ev("content.delta", `{"event_type":"content.delta","index":2,"delta":{"type":"function_call","id":"fc","name":"f","arguments":{"x":1}}}`),
		ev("interaction.complete", `{"event_type":"interaction.complete","interaction":{"id":"i5","status":"completed","usage":{"total_input_tokens":5,"total_output_tokens":6}}}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), interRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "i5" || res.Text != "Hello" || res.Reasoning != "hmm" || len(res.ToolCalls) != 1 || res.Usage.InputTokens != 5 || res.Usage.OutputTokens != 6 || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res)
	}
	mustEq(t, f.last(t).JSON(t)["stream"], true, "stream flag")
	if len(eventsOf(*evs, EventText)) != 2 || len(eventsOf(*evs, EventUsage)) != 1 {
		t.Fatal("events")
	}
	if strings.TrimSpace(res.Debug.Path) != "/v1beta/interactions" {
		t.Fatal(res.Debug.Path)
	}
}
