package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// dsmlFW swaps ASCII "|" for the fullwidth vertical line (U+FF5C) that the DSML
// tool-call markers use, so no fullwidth glyph appears in this file.
// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
func dsmlFW(s string) string { return strings.ReplaceAll(s, "|", "\uff5c") }

var dsmlBlock = dsmlFW(`<|DSML|tool_calls>
<|DSML|invoke name="get_weather">
<|DSML|parameter name="city" string="true">Par&amp;is</|DSML|parameter>
<|DSML|parameter name="days">3</|DSML|parameter>
</|DSML|invoke>
<|DSML|invoke name="ping">
</|DSML|invoke>
</|DSML|tool_calls>`)

func TestDSMLExtraction(t *testing.T) {
	text, calls, err := extractDSML("before " + dsmlBlock + " after")
	if err != nil {
		t.Fatal(err)
	}
	if text != "before  after" || len(calls) != 2 {
		t.Fatalf("%q %+v", text, calls)
	}
	if calls[0].ID != "dsml_call_1" || calls[0].Name != "get_weather" || calls[1].ID != "dsml_call_2" || calls[1].Name != "ping" {
		t.Fatalf("%+v", calls)
	}
	args := decodeMap(t, calls[0].Arguments)
	if args["city"] != "Par&is" || args["days"] != float64(3) {
		t.Fatalf("html-unescaped strings and JSON-parsed values: %v", args)
	}
	if calls[1].Arguments != "{}" {
		t.Fatal(calls[1].Arguments)
	}
}

func TestDSMLMarkerVariantsAndDuplicates(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	for _, m := range []string{"||", "|", "\uff5c\uff5c", "\uff5c"} {
		block := strings.ReplaceAll(dsmlBlock, "\uff5c", m)
		_, calls, err := extractDSML(block)
		if err != nil || len(calls) != 2 {
			t.Errorf("marker %q: %v %d", m, err, len(calls))
		}
	}
	dup := dsmlFW(`<|DSML|tool_calls><|DSML|invoke name="a"><|DSML|parameter name="x">1</|DSML|parameter><|DSML|parameter name="x">2</|DSML|parameter></|DSML|invoke><|DSML|invoke name="b"></|DSML|invoke></|DSML|tool_calls>`)
	_, calls, err := extractDSML(dup)
	if err != nil || len(calls) != 1 || calls[0].Name != "b" || calls[0].ID != "dsml_call_1" {
		t.Fatalf("a repeated parameter name voids the invoke: %v %+v", err, calls)
	}
	// Non-JSON, non-string value stays raw text.
	_, calls, _ = extractDSML(dsmlFW(`<|DSML|tool_calls><|DSML|invoke name="a"><|DSML|parameter name="x">hello world</|DSML|parameter></|DSML|invoke></|DSML|tool_calls>`))
	if decodeMap(t, calls[0].Arguments)["x"] != "hello world" {
		t.Fatal(calls[0].Arguments)
	}
	// string="true" suppresses JSON parsing.
	_, calls, _ = extractDSML(dsmlFW(`<|DSML|tool_calls><|DSML|invoke name="a"><|DSML|parameter name="x" string="true">5</|DSML|parameter></|DSML|invoke></|DSML|tool_calls>`))
	if decodeMap(t, calls[0].Arguments)["x"] != "5" {
		t.Fatal(calls[0].Arguments)
	}
}

func TestDSMLIncompleteBlock(t *testing.T) {
	_, _, err := extractDSML(dsmlFW(`hi <|DSML|tool_calls><|DSML|invoke name="a">`))
	if err == nil || !strings.Contains(err.Error(), "tool-call block ended before it was complete") {
		t.Fatalf("%v", err)
	}
}

func TestDSMLFilterWithholdsPartialOpeners(t *testing.T) {
	var f dsmlFilter
	var out strings.Builder
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	chunks := []string{"Hello <", "\uff5cDS", "ML\uff5ctool_", "calls><\uff5cDSML\uff5cinvoke name=\"x\"></\uff5cDSML\uff5cinvoke></\uff5cDSML\uff5ctool_calls> tail"}
	for _, c := range chunks {
		got := f.push(c)
		out.WriteString(got)
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		if strings.Contains(got, "\uff5c") || strings.Contains(got, "<\uff5c") {
			t.Fatalf("leaked partial marker: %q", got)
		}
	}
	rest, err := f.finish()
	out.WriteString(rest)
	if err != nil || out.String() != "Hello  tail" || len(f.calls) != 1 {
		t.Fatalf("%q %v %d", out.String(), err, len(f.calls))
	}
	// A lone "<" that never becomes a block is released at the end.
	var g dsmlFilter
	a := g.push("a < b")
	rest, _ = g.finish()
	if a+rest != "a < b" {
		t.Fatalf("%q", a+rest)
	}
	var h dsmlFilter
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	x := h.push("end <\uff5cDS")
	rest, _ = h.finish()
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if x+rest != "end <\uff5cDS" {
		t.Fatalf("%q", x+rest)
	}
}

func TestDeepSeekBufferedAndStreamed(t *testing.T) {
	body := `{"id":"d","choices":[{"message":{"content":` + jsonQuote("Sure. "+dsmlBlock+" Done.") + `},"finish_reason":"stop"}]}`
	f := newUpstream(t, jsonReply(body))
	route := routeFor(OpenAIChatCompletions, f, "DeepSeek-V3.2")
	res, err := testClient(t).Generate(context.Background(), route, &Request{Messages: userMsg("x")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Sure.  Done." || len(res.ToolCalls) != 2 || res.FinishReason != "tool_calls" {
		t.Fatalf("%+v", res)
	}
	// A non-deepseek model keeps the text as is.
	route2 := routeFor(OpenAIChatCompletions, f, "gpt-4o")
	res, err = testClient(t).Generate(context.Background(), route2, &Request{Messages: userMsg("x")})
	if err != nil || len(res.ToolCalls) != 0 || !strings.Contains(res.Text, "DSML") {
		t.Fatalf("%v %+v", err, res)
	}

	// Streamed in awkward pieces.
	full := "Sure. " + dsmlBlock + " Done."
	var frames []string
	for i := 0; i < len(full); {
		n := 7
		runes := []rune(full[i:])
		if len(runes) < n {
			n = len(runes)
		}
		piece := string(runes[:n])
		frames = append(frames, ev("", `{"id":"d","choices":[{"delta":{"content":`+jsonQuote(piece)+`}}]}`))
		i += len(piece)
	}
	f2 := newUpstream(t, sseReply(frames...))
	sink, evs := collect()
	res, err = testClient(t).Stream(context.Background(), routeFor(OpenAIChatCompletions, f2, "deepseek-chat"), &Request{Messages: userMsg("x")}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Sure.  Done." || len(res.ToolCalls) != 2 || res.ToolCalls[1].ID != "dsml_call_2" {
		t.Fatalf("%+v", res)
	}
	var streamed strings.Builder
	for _, e := range eventsOf(*evs, EventText) {
		streamed.WriteString(e.Text)
	}
	if streamed.String() != "Sure.  Done." || strings.Contains(streamed.String(), "DSML") {
		t.Fatalf("streamed text must never expose the block: %q", streamed.String())
	}
	if len(eventsOf(*evs, EventToolCall)) != 2 {
		t.Fatalf("dsml tool calls are emitted as events: %d", len(eventsOf(*evs, EventToolCall)))
	}

	// Truncated block.
	f3 := newUpstream(t, sseReply(ev("", dsmlFW(`{"id":"d","choices":[{"delta":{"content":"<|DSML|tool_calls><|DSML|invoke name=\"a\">"}}]}`))))
	_, err = testClient(t).Stream(context.Background(), routeFor(OpenAIChatCompletions, f3, "deepseek-chat"), &Request{Messages: userMsg("x")}, sink)
	if err == nil || !strings.Contains(err.Error(), "ended before it was complete") || !IsAccepted(err) {
		t.Fatalf("%v", err)
	}
	var _ = errors.New
}

func TestDeepSeekDetectionByBaseURL(t *testing.T) {
	x := &exchange{model: "m", route: Route{BaseURL: "https://api.deepseek.com"}}
	if !isDeepSeek(x) {
		t.Fatal("base URL detection")
	}
	x = &exchange{model: "gpt", route: Route{BaseURL: "https://api.openai.com"}}
	if isDeepSeek(x) {
		t.Fatal("false positive")
	}
}
