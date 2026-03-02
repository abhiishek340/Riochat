package llm

import (
	"encoding/json"
	"testing"
)

func decodeMap(t testing.TB, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUsageOpenAIStyle(t *testing.T) {
	u := decodeMap(t, `{"prompt_tokens":1000,"prompt_tokens_details":{"cached_tokens":400},"completion_tokens":200,"completion_tokens_details":{"reasoning_tokens":50}}`)
	got := openAIUsage(u, false)
	if got.InputTokens != 600 || got.CacheReadTokens != 400 || got.OutputTokens != 150 || got.ReasoningTokens != 50 {
		t.Fatalf("%+v", got)
	}
	x := openAIUsage(u, true)
	if x.OutputTokens != 200 || x.ReasoningTokens != 50 || x.InputTokens != 600 {
		t.Fatalf("xai keeps output as reported: %+v", x)
	}
}

func TestUsageResponsesAliasesAndCacheWrite(t *testing.T) {
	u := decodeMap(t, `{"input_tokens":1000,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200},"output_tokens":80,"output_tokens_details":{"reasoning_tokens":30},"service_tier":"flex"}`)
	got := openAIUsage(u, false)
	if got.InputTokens != 700 || got.CacheReadTokens != 100 || got.CacheWriteTokens != 200 || got.OutputTokens != 50 || got.ServiceTier != "flex" {
		t.Fatalf("%+v", got)
	}
	nested := decodeMap(t, `{"prompt_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30},"cache_read_input_tokens":10,"completion_tokens":5}`)
	n := openAIUsage(nested, false)
	if n.CacheWriteTokens != 50 || n.CacheWrite5mTokens != 20 || n.CacheWrite1hTokens != 30 || n.CacheReadTokens != 10 || n.InputTokens != 440 {
		t.Fatalf("%+v", n)
	}
	flat := decodeMap(t, `{"prompt_tokens":10,"reasoning_tokens":4,"completion_tokens":9}`)
	if f := openAIUsage(flat, false); f.ReasoningTokens != 4 || f.OutputTokens != 5 {
		t.Fatalf("%+v", f)
	}
}

func TestUsageNeverNegativeAndFullyCachedObserved(t *testing.T) {
	u := openAIUsage(decodeMap(t, `{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":100},"completion_tokens":2}`), false)
	if u.InputTokens != 0 || !u.InputObserved() || !u.OutputObserved() {
		t.Fatalf("fully cached prompt must count as observed: %+v", u)
	}
	weird := openAIUsage(decodeMap(t, `{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":50},"completion_tokens":1,"completion_tokens_details":{"reasoning_tokens":9}}`), false)
	if weird.InputTokens != 0 || weird.OutputTokens != 0 {
		t.Fatalf("floors at zero: %+v", weird)
	}
	var zero Usage
	if zero.InputObserved() || zero.OutputObserved() || !zero.IsZero() {
		t.Fatal("zero usage observes nothing")
	}
}

func TestUsageAnthropic(t *testing.T) {
	u := anthropicUsage(decodeMap(t, `{"input_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":70,"output_tokens":9,"speed":"fast"}`))
	if u.InputTokens != 100 || u.CacheWriteTokens != 30 || u.CacheReadTokens != 70 || u.OutputTokens != 9 || u.Speed != "fast" {
		t.Fatalf("%+v", u)
	}
	split := anthropicUsage(decodeMap(t, `{"input_tokens":1,"cache_creation_input_tokens":99,"cache_creation":{"ephemeral_1h_input_tokens":7,"ephemeral_5m_input_tokens":3}}`))
	if split.CacheWriteTokens != 10 || split.CacheWrite1hTokens != 7 || split.CacheWrite5mTokens != 3 {
		t.Fatalf("split wins over the flat field: %+v", split)
	}
}

func TestUsageGemini(t *testing.T) {
	u := geminiUsage(decodeMap(t, `{"promptTokenCount":1000,"cachedContentTokenCount":300,"candidatesTokenCount":100,"thoughtsTokenCount":40}`))
	if u.InputTokens != 700 || u.CacheReadTokens != 300 || u.OutputTokens != 100 || u.ReasoningTokens != 40 {
		t.Fatalf("%+v", u)
	}
	i := interactionsUsage(decodeMap(t, `{"total_input_tokens":50,"total_cached_tokens":20,"total_output_tokens":7,"total_thought_tokens":3}`))
	if i.InputTokens != 30 || i.CacheReadTokens != 20 || i.OutputTokens != 7 || i.ReasoningTokens != 3 {
		t.Fatalf("%+v", i)
	}
}

func TestRawUsageMerge(t *testing.T) {
	a := json.RawMessage(`{"a":1}`)
	b := json.RawMessage(`{"b": 2}`)
	got := mergeRawUsage(a, b)
	if string(got) != `[{"a":1},{"b":2}]` {
		t.Fatalf("%s", got)
	}
	if string(mergeRawUsage(got, json.RawMessage(`{"a":1}`))) != `[{"a":1},{"b":2}]` {
		t.Fatal("duplicates are not repeated")
	}
	if string(mergeRawUsage(a, a)) != `{"a":1}` || mergeRawUsage(nil, nil) != nil {
		t.Fatal("single document stays an object")
	}
}

func TestMergeUsageKeepsObservedValues(t *testing.T) {
	var u Usage
	mergeUsage(&u, Usage{InputTokens: 5, CacheReadTokens: 2, Raw: json.RawMessage(`{"s":1}`)})
	mergeUsage(&u, Usage{OutputTokens: 9, Raw: json.RawMessage(`{"d":1}`)})
	if u.InputTokens != 5 || u.CacheReadTokens != 2 || u.OutputTokens != 9 || string(u.Raw) != `[{"s":1},{"d":1}]` {
		t.Fatalf("%+v %s", u, u.Raw)
	}
}
