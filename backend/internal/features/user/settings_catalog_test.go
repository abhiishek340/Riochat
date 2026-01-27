package user

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCatalogHasExactlyTheDocumentedKeys(t *testing.T) {
	defaults := SettingDefaults()
	if len(defaults) != 23 {
		t.Fatalf("catalog has %d keys, want 23", len(defaults))
	}
	wantTrue := []string{
		"chat.show_token_usage", "chat.show_model_info", "chat.show_latency", "chat.show_billing_cost",
		"chat.show_process_trace", "chat.auto_generate_title", "chat.auto_generate_labels",
		"chat.context_compact_auto", "chat.markdown_render", "chat.auto_expand_thinking",
		"chat.auto_expand_tool_calls", "chat.restore_draft_on_failure", "chat.preserve_conversation_drafts",
		"chat.reuse_model_options", "chat.reasoning_content_passback",
	}
	for _, k := range wantTrue {
		if defaults[k] != "true" {
			t.Errorf("%s default = %q, want true", k, defaults[k])
		}
	}
	other := map[string]string{
		"chat.delete_conversation_files_by_default": "false",
		"chat.file_mode":                "auto",
		"chat.send_on_enter":            "enter",
		"chat.input_height":             "standard",
		"chat.content_width":            "compact",
		"chat.default_model":            "",
		"chat.default_mcp_tool_ids":     "[]",
		"chat.model_control_placements": "{}",
	}
	for k, v := range other {
		if got, ok := defaults[k]; !ok || got != v {
			t.Errorf("%s default = %q (present %v), want %q", k, got, ok, v)
		}
	}
	for _, k := range SettingKeys() {
		if len(k) > 64 {
			t.Errorf("key %q longer than 64", k)
		}
	}
	// A fresh map each call: callers may mutate it.
	defaults["chat.file_mode"] = "x"
	if SettingDefaults()["chat.file_mode"] != "auto" {
		t.Fatal("SettingDefaults must return a fresh map")
	}
}

func TestBooleanAndEnumValues(t *testing.T) {
	for _, k := range []string{"chat.show_latency", "chat.delete_conversation_files_by_default", "chat.markdown_render"} {
		for _, v := range []string{"true", "false"} {
			if !ValidateSettingValue(k, v) {
				t.Errorf("%s=%q should be valid", k, v)
			}
		}
		for _, v := range []string{"True", "FALSE", "1", "0", "", "yes", " true", "true "} {
			if ValidateSettingValue(k, v) {
				t.Errorf("%s=%q should be invalid", k, v)
			}
		}
	}
	enums := map[string]struct{ good, bad []string }{
		"chat.file_mode":     {[]string{"auto", "full_context", "rag"}, []string{"", "Auto", "bogus", " auto"}},
		"chat.send_on_enter": {[]string{"enter", "ctrl_enter", "meta_enter"}, []string{"shift_enter", "ENTER", ""}},
		"chat.input_height":  {[]string{"compact", "standard", "loose"}, []string{"tall", "Loose"}},
		"chat.content_width": {[]string{"compact", "standard", "wide"}, []string{"loose", "full"}},
	}
	for k, c := range enums {
		for _, v := range c.good {
			if !ValidateSettingValue(k, v) {
				t.Errorf("%s=%q should be valid", k, v)
			}
		}
		for _, v := range c.bad {
			if ValidateSettingValue(k, v) {
				t.Errorf("%s=%q should be invalid", k, v)
			}
		}
	}
	for _, v := range []string{"", "gpt-x", strings.Repeat("long ", 5000), "  padded  "} {
		if !ValidateSettingValue("chat.default_model", v) {
			t.Errorf("default_model %q should be valid", v)
		}
	}
	if ValidateSettingValue("chat.bogus", "x") {
		t.Error("unknown key must never validate")
	}
}

func ids(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprint(i + 1)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestDefaultMCPToolIDs(t *testing.T) {
	const k = "chat.default_mcp_tool_ids"
	for _, v := range []string{"[]", "[1]", "[1,2,3]", " [42] ", "\n[7, 8]\t", ids(128), "null", "[18446744073709551615]"} {
		if !ValidateSettingValue(k, v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{
		"", "   ", "{}", "[0]", "[-1]", "[1.5]", "[1.0]", "[1e2]", `["1"]`, "[null]", "[true]", "[[1]]", `[{"id":1}]`,
		ids(129), "[1,]", "[1] x", "[1][2]", "1", `"[1]"`, "[1,2", "[01]", "[18446744073709551616]",
	} {
		if ValidateSettingValue(k, v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func placements(toolbar, menu int) string {
	var parts []string
	for i := 0; i < toolbar; i++ {
		parts = append(parts, fmt.Sprintf(`"t%d":"toolbar"`, i))
	}
	for i := 0; i < menu; i++ {
		parts = append(parts, fmt.Sprintf(`"m%d":"menu"`, i))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestModelControlPlacements(t *testing.T) {
	const k = "chat.model_control_placements"
	good := []string{
		"{}", `{"reasoning":"toolbar","temperature":"menu"}`, placements(6, 1), placements(0, 128), placements(6, 122),
		` {"a":"menu"} `, "null", fmt.Sprintf(`{%q:"menu"}`, strings.Repeat("x", 96)),
	}
	for _, v := range good {
		if !ValidateSettingValue(k, v) {
			t.Errorf("%q should be valid", v)
		}
	}
	bad := []string{
		"", "[]", `{"a":"sidebar"}`, `{"":"menu"}`, `{"  ":"menu"}`, placements(7, 0), placements(7, 1), placements(0, 129),
		fmt.Sprintf(`{%q:"menu"}`, strings.Repeat("x", 97)), `{"a":1}`, `{"a":null}`, `{"a":["menu"]}`,
		`{"a":"Menu"}`, `{"a":"menu"} extra`, `"menu"`, "1",
	}
	for _, v := range bad {
		if ValidateSettingValue(k, v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestValidatePatch(t *testing.T) {
	got, err := validatePatch(map[string]string{" chat.show_latency ": "false", "chat.file_mode": "rag"})
	if err != nil || got["chat.show_latency"] != "false" || got["chat.file_mode"] != "rag" || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	// Values are kept exactly as sent.
	got, err = validatePatch(map[string]string{"chat.default_model": "  spaced  "})
	if err != nil || got["chat.default_model"] != "  spaced  " {
		t.Fatalf("value must not be trimmed: %v, %v", got, err)
	}
	if _, err = validatePatch(map[string]string{"chat.bogus": "x"}); !errors.Is(err, ErrUnknownSettingKey) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err = validatePatch(map[string]string{"chat.markdown_render": "yes"}); !errors.Is(err, ErrInvalidSettingVal) {
		t.Fatalf("bad value: %v", err)
	}
	// Both problems at once: the unknown key is reported, deterministically.
	for i := 0; i < 20; i++ {
		_, err = validatePatch(map[string]string{"chat.markdown_render": "yes", "chat.bogus": "x"})
		if !errors.Is(err, ErrUnknownSettingKey) {
			t.Fatalf("expected unknown key first, got %v", err)
		}
	}
	// Keys that collapse after trimming: the later raw key wins, no failure.
	got, err = validatePatch(map[string]string{"chat.file_mode": "auto", " chat.file_mode": "rag"})
	if err != nil || len(got) != 1 {
		t.Fatalf("collapse: %v, %v", got, err)
	}
	if got["chat.file_mode"] != "auto" && got["chat.file_mode"] != "rag" {
		t.Fatalf("collapse value: %v", got)
	}
	if got, err = validatePatch(map[string]string{}); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
	// An invalid value never reveals which key failed to the client.
	_, err = validatePatch(map[string]string{"chat.file_mode": "zzz"})
	if err.Error() != ErrInvalidSettingVal.Message && !errors.Is(err, ErrInvalidSettingVal) {
		t.Fatalf("unexpected error: %v", err)
	}
}
