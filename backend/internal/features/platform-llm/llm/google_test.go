package llm

import (
	"context"
	"strings"
	"testing"
)

const okGoogle = `{"responseId":"g1","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[
 {"text":"thinking...","thought":true,"thoughtSignature":"TS"},
 {"text":"Hello "},{"text":"world"},
 {"functionCall":{"name":"lookup","args":{"q":1}},"thoughtSignature":"FSIG"},
 {"executableCode":{"language":"PYTHON","code":"print(1)"}},{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1"}},
 {"inlineData":{"mimeType":"image/png","data":"SU1H"}}]},
 "groundingMetadata":{"webSearchQueries":["go lang","golang"],"groundingChunks":[{"web":{"uri":"https://a.example","title":"A"}}]},
 "urlContextMetadata":{"urlMetadata":[{"retrievedUrl":"https://u.example"}]}}],
 "usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":300,"candidatesTokenCount":100,"thoughtsTokenCount":40}}`

func googRoute(f *fakeUpstream) Route { return routeFor(GoogleGenerateContent, f, "gemini-2.5-flash") }

func TestGoogleRequestShape(t *testing.T) {
	f := newUpstream(t, jsonReply(okGoogle))
	req := &Request{
		Instructions: "inst",
		Messages: []Message{
			{Role: RoleSystem, Content: "sys"},
			{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "look"}, {Kind: PartImage, MIME: "image/png", Data: []byte("IMG")}, {Kind: PartAudio, MIME: "audio/wav", Data: []byte("AUD")}}},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":1}`, ThoughtSignature: "SIG"}}},
			{Role: RoleTool, ToolResults: []ToolResult{{CallID: "c1", Name: "lookup", Output: "found"}}},
			{Role: RoleTool, ToolResults: []ToolResult{{CallID: "c2", Name: "other", IsError: true, Error: "nope"}}},
		},
		Tools: []ToolDef{{Name: "lookup", Description: "d", Schema: map[string]any{"type": "object"}}},
		Options: map[string]any{
			"max_output_tokens": float64(256), "temperature": 0.4, "top_p": 0.8, "top_k": float64(20), "stop": "Z", "n": float64(2),
			"seed": float64(5), "response_modalities": []any{"text"}, "thinking": true, "thinking_budget": float64(512), "thinking_level": "low",
			"response_format": "json", "web_search": true,
			"safetySettings": []any{map[string]any{"category": "X"}}, "cachedContent": "cachedContents/1", "service_tier": "flex",
			"generationConfig": map[string]any{"temperature": 0.99, "candidateCount": float64(9), "mediaResolution": "LOW"},
		},
	}
	res, err := testClient(t).Generate(context.Background(), googRoute(f), req)
	if err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1beta/models/gemini-2.5-flash:generateContent" || r.RawQuery != "" {
		t.Fatalf("%s?%s", r.Path, r.RawQuery)
	}
	if r.Header.Get("X-Goog-Api-Key") != "sk-test" || strings.Contains(r.RawQuery, "key=") {
		t.Fatal("the key travels in a header only")
	}
	b := r.JSON(t)
	mustEq(t, at(b, "systemInstruction.parts.0.text"), "inst", "instructions first")
	mustEq(t, at(b, "systemInstruction.parts.1.text"), "sys", "system messages")
	mustEq(t, at(b, "contents.0.role"), "user", "user")
	mustEq(t, at(b, "contents.0.parts.1.inlineData.mimeType"), "image/png", "inline image")
	mustEq(t, at(b, "contents.0.parts.1.inlineData.data"), "SU1H", "data")
	mustEq(t, at(b, "contents.0.parts.2.inlineData.mimeType"), "audio/wav", "audio")
	mustEq(t, at(b, "contents.1.role"), "model", "assistant becomes model")
	mustEq(t, at(b, "contents.1.parts.0.functionCall.name"), "lookup", "functionCall")
	mustEq(t, at(b, "contents.1.parts.0.functionCall.args.q"), "1", "args")
	mustEq(t, at(b, "contents.1.parts.0.thoughtSignature"), "SIG", "signature preserved")
	mustEq(t, at(b, "contents.2.role"), "user", "function responses are user turns")
	mustEq(t, at(b, "contents.2.parts.0.functionResponse.name"), "lookup", "functionResponse")
	mustEq(t, at(b, "contents.2.parts.0.functionResponse.response.content"), "found", "response content")
	mustEq(t, at(b, "contents.2.parts.1.functionResponse.response.error"), "nope", "adjacent same-role contents merge")
	gc := at(b, "generationConfig")
	mustEq(t, at(gc, "maxOutputTokens"), "256", "maxOutputTokens")
	mustEq(t, at(gc, "topP"), "0.8", "topP")
	mustEq(t, at(gc, "topK"), "20", "topK")
	mustEq(t, at(gc, "stopSequences.0"), "Z", "stopSequences")
	mustEq(t, at(gc, "candidateCount"), "2", "alias wins over the object")
	mustEq(t, at(gc, "temperature"), "0.4", "alias wins over the object")
	mustEq(t, at(gc, "mediaResolution"), "LOW", "object fills what aliases did not set")
	mustEq(t, at(gc, "responseModalities.0"), "TEXT", "modalities upper-cased")
	mustEq(t, at(gc, "responseMimeType"), "application/json", "json response")
	mustEq(t, at(gc, "thinkingConfig.includeThoughts"), true, "thinking bool")
	mustEq(t, at(gc, "thinkingConfig.thinkingBudget"), "512", "budget")
	mustEq(t, at(gc, "thinkingConfig.thinkingLevel"), "low", "level")
	mustEq(t, at(b, "tools.0.google_search"), "map[]", "native search")
	mustEq(t, at(b, "tools.1.functionDeclarations.0.name"), "lookup", "declarations")
	mustEq(t, at(b, "tools.1.functionDeclarations.0.parametersJsonSchema.type"), "object", "parametersJsonSchema")
	mustEq(t, at(b, "toolConfig.includeServerSideToolInvocations"), true, "server-side invocations flag")
	mustEq(t, at(b, "cachedContent"), "cachedContents/1", "root passthrough")
	mustEq(t, at(b, "serviceTier"), "flex", "serviceTier")
	if at(b, "safetySettings.0.category") != "X" {
		t.Fatal("safetySettings")
	}

	// Parsing.
	if res.ID != "g1" || res.Text != "Hello world" || res.Reasoning != "thinking..." || res.ReasoningSignature != "TS" {
		t.Fatalf("%+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "lookup" || res.ToolCalls[0].ID != "call_1" || res.ToolCalls[0].ThoughtSignature != "FSIG" || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res.ToolCalls)
	}
	if len(res.Images) != 1 || string(res.Images[0].Data) != "IMG" {
		t.Fatalf("%+v", res.Images)
	}
	if res.Usage.InputTokens != 700 || res.Usage.CacheReadTokens != 300 || res.Usage.OutputTokens != 100 || res.Usage.ReasoningTokens != 40 {
		t.Fatalf("%+v", res.Usage)
	}
	if res.ServerToolUsage["google_search"] != 2 || res.ServerToolUsage["code_execution"] != 1 || res.ServerToolUsage["url_context"] != 1 {
		t.Fatalf("%v", res.ServerToolUsage)
	}
	if len(res.Citations) != 1 || res.Citations[0].URL != "https://a.example" || len(res.ServerToolCalls) != 3 {
		t.Fatalf("%+v %d", res.Citations, len(res.ServerToolCalls))
	}
}

func TestGoogleThinkingTypeAndProviderToolObjects(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`))
	c := testClient(t)
	req := &Request{Messages: userMsg("x"), Options: map[string]any{
		"thinking":   map[string]any{"type": "disabled"},
		"tools":      []any{map[string]any{"type": "google_search"}, map[string]any{"code_execution": map[string]any{}}},
		"web_search": true,
	}}
	if _, err := c.Generate(context.Background(), googRoute(f), req); err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	mustEq(t, at(b, "generationConfig.thinkingConfig.includeThoughts"), false, "disabled → false")
	if n := len(b["tools"].([]any)); n != 2 {
		t.Fatalf("web_search must not duplicate an existing google_search tool: %d", n)
	}
	for _, typ := range []string{"enabled", "auto"} {
		req.Options = map[string]any{"thinking": typ}
		c.Generate(context.Background(), googRoute(f), req)
		mustEq(t, at(f.last(t).JSON(t), "generationConfig.thinkingConfig.includeThoughts"), true, typ)
	}
	req.Options = map[string]any{"responseSchema": map[string]any{"type": "object"}}
	c.Generate(context.Background(), googRoute(f), req)
	mustEq(t, at(f.last(t).JSON(t), "generationConfig.responseMimeType"), "application/json", "a schema forces JSON")
}

func TestGoogleBaseURLWithOpenAIStyleProxy(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`))
	route := googRoute(f)
	route.BaseURL = f.URL + "/v1"
	route.Model = "models/gemini-x"
	if _, err := testClient(t).Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	if f.last(t).Path != "/v1beta/models/gemini-x:generateContent" {
		t.Fatal(f.last(t).Path)
	}
}

func TestGoogleStream(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("", `{"responseId":"g","candidates":[{"content":{"parts":[{"text":"think","thought":true}]}}]}`),
		ev("", `{"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`),
		ev("", `{"candidates":[{"content":{"parts":[{"text":"lo"},{"functionCall":{"name":"f","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), googRoute(f), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1beta/models/gemini-2.5-flash:streamGenerateContent" || r.RawQuery != "alt=sse" {
		t.Fatalf("%s?%s", r.Path, r.RawQuery)
	}
	if res.Text != "Hello" || res.Reasoning != "think" || len(res.ToolCalls) != 1 || res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 3 {
		t.Fatalf("%+v", res)
	}
	if len(eventsOf(*evs, EventText)) != 2 || len(eventsOf(*evs, EventToolCall)) != 1 || len(eventsOf(*evs, EventUsage)) != 1 {
		t.Fatalf("%d", len(*evs))
	}
}

func TestGoogleBlockedPromptAndErrors(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	_, err := testClient(t).Generate(context.Background(), googRoute(f), &Request{Messages: userMsg("x")})
	e, ok := AsError(err)
	if !ok || e.Status != 400 || !strings.Contains(e.Message, "SAFETY") {
		t.Fatalf("%v", err)
	}
}

func TestGoogleImageGeneration(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"candidates":[{"content":{"parts":[{"text":"A cat in a hat"},{"inlineData":{"mimeType":"image/png","data":"SU1H"}}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1290}}`))
	route := routeFor(GoogleImageGeneration, f, "gemini-image")
	req := &Request{Messages: []Message{
		{Role: RoleUser, Content: "old prompt"},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "draw a cat"}, {Kind: PartImage, MIME: "image/jpeg", Data: []byte("REF")}}},
	}, Options: map[string]any{"aspect_ratio": "16:9", "image_size": "2k"}}
	res, err := testClient(t).Generate(context.Background(), route, req)
	if err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	if n := len(b["contents"].([]any)); n != 1 {
		t.Fatalf("a single user content, got %d", n)
	}
	mustEq(t, at(b, "contents.0.parts.0.text"), "draw a cat", "last user text")
	mustEq(t, at(b, "contents.0.parts.1.inlineData.data"), "UkVG", "input image")
	mustEq(t, at(b, "generationConfig.responseModalities.0"), "TEXT", "default modalities")
	mustEq(t, at(b, "generationConfig.responseModalities.1"), "IMAGE", "default modalities")
	mustEq(t, at(b, "generationConfig.imageConfig.aspectRatio"), "16:9", "aspect")
	mustEq(t, at(b, "generationConfig.imageConfig.imageSize"), "2K", "size normalized")
	if len(res.Images) != 1 || res.Images[0].RevisedPrompt != "A cat in a hat" {
		t.Fatalf("%+v", res.Images)
	}
	// Prompt required.
	_, err = testClient(t).Generate(context.Background(), route, &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartImage, Data: []byte("x")}}}}})
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("a missing prompt is an error: %v", err)
	}
}
