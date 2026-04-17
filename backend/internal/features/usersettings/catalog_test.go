package usersettings

import (
	"fmt"
	"strings"
	"testing"
)

func TestCatalogHasTwentyThreeKeysWithValidDefaults(t *testing.T) {
	if len(Keys()) != 23 {
		t.Fatalf("keys: %d", len(Keys()))
	}
	for _, k := range Keys() {
		d, _ := Default(k)
		if !catalog[k].valid(d) {
			t.Errorf("default %q of %s fails its own validator", d, k)
		}
	}
	want := map[string]string{
		"chat.file_mode": "auto", "chat.default_mcp_tool_ids": "[]", "chat.model_control_placements": "{}",
		"chat.content_width": "compact", "chat.delete_conversation_files_by_default": "false",
		"chat.send_on_enter": "enter", "chat.input_height": "standard", "chat.default_model": "",
		"chat.show_latency": "true", "chat.reasoning_content_passback": "true",
	}
	for k, v := range want {
		if got, ok := Default(k); !ok || got != v {
			t.Errorf("default of %s = %q, want %q", k, got, v)
		}
	}
}

func TestBooleanValidation(t *testing.T) {
	for _, v := range []string{"true", "false"} {
		if !validBool(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"True", "FALSE", "1", "0", "", " true", "true ", "yes"} {
		if validBool(v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestEnumValidation(t *testing.T) {
	cases := []struct {
		key, val string
		ok       bool
	}{
		{"chat.file_mode", "auto", true}, {"chat.file_mode", "full_context", true}, {"chat.file_mode", "rag", true},
		{"chat.file_mode", "RAG", false}, {"chat.file_mode", " rag", false}, {"chat.file_mode", "", false},
		{"chat.send_on_enter", "ctrl_enter", true}, {"chat.send_on_enter", "meta_enter", true},
		{"chat.send_on_enter", "shift_enter", false},
		{"chat.input_height", "loose", true}, {"chat.input_height", "tall", false},
		{"chat.content_width", "wide", true}, {"chat.content_width", "loose", false},
	}
	for _, c := range cases {
		if got := catalog[c.key].valid(c.val); got != c.ok {
			t.Errorf("%s=%q: got %v want %v", c.key, c.val, got, c.ok)
		}
	}
}

func TestDefaultModelAcceptsAnything(t *testing.T) {
	for _, v := range []string{"", " ", "gpt/x y", strings.Repeat("m", 100000)} {
		if !catalog["chat.default_model"].valid(v) {
			t.Errorf("rejected %d-byte value", len(v))
		}
	}
}

func ids(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprint(i + 1)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestToolIDsValidation(t *testing.T) {
	valid := []string{"[]", "[1]", "[1,2,3]", " [42] ", "\n[7]\t", "null", " null ", "[ 1 , 2 ]", ids(128)}
	invalid := []string{"", " ", "{}", "[0]", "[-1]", "[1.5]", `["1"]`, "[null]", "[1,]", "[1] x", "[1][2]",
		"[true]", "[[1]]", "[{}]", "abc", "5", `"[1]"`, "[1e2]", "[99999999999999999999]", ids(129)}
	for _, v := range valid {
		if !validToolIDs(v) {
			t.Errorf("should be valid: %q", v)
		}
	}
	for _, v := range invalid {
		if validToolIDs(v) {
			t.Errorf("should be invalid: %q", v)
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

func TestPlacementsValidation(t *testing.T) {
	valid := []string{"{}", `{"reasoning":"toolbar","temperature":"menu"}`, placements(6, 1), placements(6, 0),
		" {} ", "null", placements(0, 128), placements(6, 122),
		`{"` + strings.Repeat("a", 96) + `":"menu"}`, `{" x ":"menu"}`}
	invalid := []string{"", "[]", "5", `"x"`, `{"a":"sidebar"}`, `{"":"menu"}`, `{"  ":"menu"}`, placements(7, 0),
		placements(7, 1), placements(0, 129), placements(6, 123), `{"` + strings.Repeat("a", 97) + `":"menu"}`,
		`{"a":1}`, `{"a":null}`, `{"a":"Toolbar"}`, `{"a":"menu"} x`, `{"a":["menu"]}`, `{"a":"menu",}`,
		`{"` + strings.Repeat("é", 49) + `":"menu"}`}
	for _, v := range valid {
		if !validPlacements(v) {
			t.Errorf("should be valid: %.80q", v)
		}
	}
	for _, v := range invalid {
		if validPlacements(v) {
			t.Errorf("should be invalid: %.80q", v)
		}
	}
}

// The raw spelling that sorts last in byte order wins ("chat.file_mode\t" sorts after "chat.file_mode").
func TestNormalizeCollapsesWhitespaceDuplicatesDeterministically(t *testing.T) {
	in := map[string]string{"chat.file_mode": "auto", " chat.file_mode ": "rag", "chat.file_mode\t": "full_context"}
	for i := 0; i < 20; i++ {
		out, err := normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out["chat.file_mode"] != "full_context" {
			t.Fatalf("got %v", out)
		}
	}
}

func TestNormalizeChecksKeyBeforeValueAndRejectsNUL(t *testing.T) {
	if _, err := normalize(map[string]string{"chat.nope": "also bad"}); err == nil || !isClientError(err) {
		t.Fatalf("err: %v", err)
	}
	if _, err := normalize(map[string]string{"chat.default_model": "a\x00b"}); err == nil {
		t.Fatal("NUL byte must be rejected")
	}
	if _, err := normalize(map[string]string{"chat.file_mode": "bad"}); !isClientError(err) || err.Error() == "" {
		t.Fatalf("err: %v", err)
	}
}
