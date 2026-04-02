package llm

import (
	"reflect"
	"testing"
)

func TestDefaultPolicyAllowsDocumentedOptions(t *testing.T) {
	opts := map[string]any{
		"temperature": 0.5, "service_tier": "flex", "frequency_penalty": 0.1, "top_k": 5.0, "tools": []any{map[string]any{"type": "x"}},
		"stream_options":  map[string]any{"include_usage": true, "include_obfuscation": false},
		"thinking":        map[string]any{"type": "enabled", "budget_tokens": 5000},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "n"}},
		"model":           "evil", "headers": map[string]any{"x": "y"}, "stream": true,
	}
	kept, dropped := FilterOptions(opts, "openai_chat_completions", "", DefaultAllowedPaths, DefaultDeniedPaths)
	want := map[string]any{
		"temperature": 0.5, "service_tier": "flex", "frequency_penalty": 0.1,
		"stream_options":  map[string]any{"include_usage": true},
		"thinking":        map[string]any{"type": "enabled"},
		"response_format": map[string]any{"type": "json_schema"},
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept %v", kept)
	}
	wantDropped := []string{"headers", "model", "response_format.json_schema", "stream", "stream_options.include_obfuscation", "thinking.budget_tokens", "tools", "top_k"}
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Fatalf("dropped %v", dropped)
	}
}

func TestPolicyUsesProtocolKeysAndAliases(t *testing.T) {
	opts := map[string]any{"top_k": 3.0, "thinking": map[string]any{"type": "enabled", "budget_tokens": 2000.0}, "service_tier": "x"}
	for _, proto := range []string{"anthropic_messages", "claude", "ANTHROPIC"} {
		kept, dropped := FilterOptions(opts, proto, ModeAllowlist, DefaultAllowedPaths, DefaultDeniedPaths)
		if kept["top_k"] != 3.0 || kept["thinking"].(map[string]any)["budget_tokens"] != 2000.0 || len(dropped) != 1 || dropped[0] != "service_tier" {
			t.Errorf("%s: %v %v", proto, kept, dropped)
		}
	}
	g, _ := FilterOptions(map[string]any{"generationConfig": map[string]any{"temperature": 1, "topK": 2, "thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": 9}}}, "google_generate_content", "", DefaultAllowedPaths, DefaultDeniedPaths)
	gc := g["generationConfig"].(map[string]any)
	if len(gc) != 2 || gc["temperature"] != 1 || len(gc["thinkingConfig"].(map[string]any)) != 1 {
		t.Fatalf("%v", g)
	}
	v, _ := FilterOptions(map[string]any{"duration": 5, "n": 1}, "xai_video_extensions", "", DefaultAllowedPaths, DefaultDeniedPaths)
	if len(v) != 1 || v["duration"] != 5 {
		t.Fatalf("%v", v)
	}
	// Unknown protocols use the openai_responses key.
	r, _ := FilterOptions(map[string]any{"reasoning": map[string]any{"effort": "low", "x": 1}}, "who knows", "", DefaultAllowedPaths, DefaultDeniedPaths)
	if !reflect.DeepEqual(r, map[string]any{"reasoning": map[string]any{"effort": "low"}}) {
		t.Fatalf("%v", r)
	}
}

func TestDenylistModeAndDeniedAlwaysWin(t *testing.T) {
	denied := PathSet{"default": {"model", "nested.secret"}, "openai_responses": {"reasoning.generate_summary"}}
	opts := map[string]any{"model": "x", "anything": 1, "reasoning": map[string]any{"effort": "low", "generate_summary": "auto"}, "nested": map[string]any{"secret": 1, "ok": 2}}
	kept, dropped := FilterOptions(opts, "openai_responses", ModeDenylist, nil, denied)
	want := map[string]any{"anything": 1, "reasoning": map[string]any{"effort": "low"}, "nested": map[string]any{"ok": 2}}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("%v", kept)
	}
	if !reflect.DeepEqual(dropped, []string{"model", "nested.secret", "reasoning.generate_summary"}) {
		t.Fatalf("%v", dropped)
	}
	// Denied descendants of an allowed subtree are removed in allowlist mode too.
	k2, d2 := FilterOptions(map[string]any{"provider": map[string]any{"only": []any{"a"}, "api_key": "k"}}, "openrouter_images", "allowlist",
		PathSet{"openrouter_images": {"provider"}}, PathSet{"default": {"provider.api_key"}})
	if !reflect.DeepEqual(k2, map[string]any{"provider": map[string]any{"only": []any{"a"}}}) || !reflect.DeepEqual(d2, []string{"provider.api_key"}) {
		t.Fatalf("%v %v", k2, d2)
	}
	if k, _ := FilterOptions(nil, "x", "", nil, nil); len(k) != 0 {
		t.Fatal("nil options")
	}
}

func TestParsePathSet(t *testing.T) {
	ps, err := ParsePathSet(`{"Default":["a","b.c"],"openai_responses":["d"]}`)
	if err != nil || !reflect.DeepEqual(ps["default"], []string{"a", "b.c"}) || ps["openai_responses"][0] != "d" {
		t.Fatalf("%v %v", ps, err)
	}
	if ps, err := ParsePathSet(""); err != nil || len(ps) != 0 {
		t.Fatal("empty")
	}
	if _, err := ParsePathSet(`[1]`); err == nil {
		t.Fatal("invalid document")
	}
	// Every default key is a real policy key.
	for k := range DefaultAllowedPaths {
		if k != "default" && PolicyKeyFor(k) != k {
			t.Errorf("default policy key %q is not a policy key", k)
		}
	}
}
