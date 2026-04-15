package settings_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type hookCall struct {
	user uint
	keys []string
}

type hookLog struct {
	mu    sync.Mutex
	calls []hookCall
}

func (l *hookLog) hook(_ context.Context, user uint, keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, hookCall{user, append([]string(nil), keys...)})
}

func (l *hookLog) all() []hookCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]hookCall(nil), l.calls...)
}

func userSettings(t *testing.T, h *harness, headers map[string]string) map[string]string {
	t.Helper()
	w := h.do("GET", "/api/v1/user/settings", headers, nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Settings map[string]string `json:"settings"`
	}
	testkit.Decode(t, w, &out)
	return out.Settings
}

var defaultUserSettings = map[string]string{
	"chat.file_mode": "auto", "chat.send_on_enter": "enter", "chat.show_token_usage": "true", "chat.show_model_info": "true",
	"chat.show_latency": "true", "chat.show_billing_cost": "true", "chat.show_process_trace": "true", "chat.default_model": "",
	"chat.auto_generate_title": "true", "chat.auto_generate_labels": "true", "chat.delete_conversation_files_by_default": "false",
	"chat.context_compact_auto": "true", "chat.markdown_render": "true", "chat.auto_expand_thinking": "true",
	"chat.auto_expand_tool_calls": "true", "chat.restore_draft_on_failure": "true", "chat.preserve_conversation_drafts": "true",
	"chat.reuse_model_options": "true", "chat.reasoning_content_passback": "true", "chat.input_height": "standard",
	"chat.content_width": "compact", "chat.default_mcp_tool_ids": "[]", "chat.model_control_placements": "{}",
}

func TestUserSettingsDefaults(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	got := userSettings(t, h, h.user)
	if len(got) != 23 {
		t.Fatalf("expected 23 keys, got %d", len(got))
	}
	for k, want := range defaultUserSettings {
		if v, ok := got[k]; !ok || v != want {
			t.Errorf("%s = %q (present %v), want %q", k, v, ok, want)
		}
	}
	if len(settings.UserSettingKeys()) != 23 {
		t.Fatal("key list")
	}
}

func TestUserSettingsPatchAndIsolation(t *testing.T) {
	log := &hookLog{}
	h := newHarness(t, settings.Ports{UserSettings: log.hook})
	w := h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.send_on_enter": "ctrl_enter", "chat.show_latency": "false"}})
	testkit.Expect(t, w, 200, "")
	var out struct {
		Settings map[string]string `json:"settings"`
	}
	testkit.Decode(t, w, &out)
	if len(out.Settings) != 23 || out.Settings["chat.send_on_enter"] != "ctrl_enter" || out.Settings["chat.show_latency"] != "false" || out.Settings["chat.file_mode"] != "auto" {
		t.Fatalf("response: %v", out.Settings)
	}
	if got := userSettings(t, h, h.user); got["chat.send_on_enter"] != "ctrl_enter" {
		t.Fatalf("persisted: %v", got)
	}
	other := h.env.Bearer(testkit.User(8))
	if got := userSettings(t, h, other); got["chat.send_on_enter"] != "enter" {
		t.Fatal("settings are per user")
	}
	// the hook fires once with the user and exactly the patched keys
	calls := log.all()
	if len(calls) != 1 || calls[0].user != 7 || strings.Join(calls[0].keys, ",") != "chat.send_on_enter,chat.show_latency" {
		t.Fatalf("hook calls: %+v", calls)
	}
	// update again: upsert
	testkit.Expect(t, h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.send_on_enter": "meta_enter"}}), 200, "")
	var rows int64
	h.env.DB.Model(&settings.UserSetting{}).Where("user_id = ? AND \"key\" = ?", 7, "chat.send_on_enter").Count(&rows)
	if rows != 1 || userSettings(t, h, h.user)["chat.send_on_enter"] != "meta_enter" {
		t.Fatalf("upsert: rows=%d", rows)
	}
	if len(log.all()) != 2 {
		t.Fatal("hook fires per successful write")
	}
}

func TestUserSettingsRejectionsWriteNothing(t *testing.T) {
	log := &hookLog{}
	h := newHarness(t, settings.Ports{UserSettings: log.hook})
	w := h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.nope": "x", "chat.send_on_enter": "ctrl_enter"}})
	testkit.Expect(t, w, 400, "user_settings.unknown_key")
	if testkit.Envelope(t, w).ErrorMsg != "unknown setting key" {
		t.Fatalf("message: %s", w.Body)
	}
	w = h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.show_latency": "yes", "chat.send_on_enter": "ctrl_enter"}})
	testkit.Expect(t, w, 400, "user_settings.invalid_value")
	if testkit.Envelope(t, w).ErrorMsg != "invalid user setting value" || strings.Contains(w.Body.String(), "chat.show_latency") {
		t.Fatalf("message must not reveal the key: %s", w.Body)
	}
	// unknown key wins over an invalid value
	w = h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.show_latency": "yes", "chat.nope": "x"}})
	testkit.Expect(t, w, 400, "user_settings.unknown_key")

	if got := userSettings(t, h, h.user); got["chat.send_on_enter"] != "enter" {
		t.Fatal("nothing may be written by a rejected request")
	}
	if len(log.all()) != 0 {
		t.Fatal("hook must not fire for rejected requests")
	}
}

func TestUserSettingsBodyErrors(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	for name, body := range map[string]any{
		"empty":         "",
		"not json":      "{",
		"missing field": map[string]any{},
		"null settings": `{"settings":null}`,
		"non string":    `{"settings":{"chat.show_latency":true}}`,
		"wrong type":    `{"settings":[]}`,
	} {
		w := h.do("PATCH", "/api/v1/user/settings", h.user, body)
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// an empty map is a valid no-op
	testkit.Expect(t, h.do("PATCH", "/api/v1/user/settings", h.user, `{"settings":{}}`), 200, "")
}

func TestUserSettingValidationRules(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	ok := func(key, value string) {
		t.Helper()
		w := h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{key: value}})
		if w.Code != 200 {
			t.Errorf("%s=%q should be accepted: %d %s", key, value, w.Code, w.Body)
		}
	}
	bad := func(key, value string) {
		t.Helper()
		w := h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{key: value}})
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "user_settings.invalid_value" {
			t.Errorf("%s=%q should be rejected: %d %s", key, value, w.Code, w.Body)
		}
	}
	for key, def := range defaultUserSettings {
		if def == "true" || def == "false" {
			ok(key, "true")
			ok(key, "false")
			for _, v := range []string{"yes", "TRUE", "1", "", " true", "true "} {
				bad(key, v)
			}
		}
	}
	for _, v := range []string{"auto", "full_context", "rag"} {
		ok("chat.file_mode", v)
	}
	bad("chat.file_mode", "other")
	bad("chat.file_mode", "")
	for _, v := range []string{"enter", "ctrl_enter", "meta_enter"} {
		ok("chat.send_on_enter", v)
	}
	bad("chat.send_on_enter", "shift_enter")
	for _, v := range []string{"compact", "standard", "loose"} {
		ok("chat.input_height", v)
	}
	bad("chat.input_height", "tall")
	for _, v := range []string{"compact", "standard", "wide"} {
		ok("chat.content_width", v)
	}
	bad("chat.content_width", "full")
	ok("chat.default_model", "")
	ok("chat.default_model", "  any model name ")
	if got := userSettings(t, h, h.user)["chat.default_model"]; got != "  any model name " {
		t.Fatalf("values are not trimmed: %q", got)
	}

	// MCP tool ids
	ok("chat.default_mcp_tool_ids", "[1,2]")
	ok("chat.default_mcp_tool_ids", "  [ 3 , 4 ]  ")
	ok("chat.default_mcp_tool_ids", "[]")
	bad("chat.default_mcp_tool_ids", "[1,2,0]")
	bad("chat.default_mcp_tool_ids", "[-1]")
	bad("chat.default_mcp_tool_ids", "[1.5]")
	bad("chat.default_mcp_tool_ids", `["1"]`)
	bad("chat.default_mcp_tool_ids", `{"a":1}`)
	bad("chat.default_mcp_tool_ids", "")
	bad("chat.default_mcp_tool_ids", "null")
	ids := make([]string, 129)
	for i := range ids {
		ids[i] = "5"
	}
	bad("chat.default_mcp_tool_ids", "["+strings.Join(ids, ",")+"]")
	ok("chat.default_mcp_tool_ids", "["+strings.Join(ids[:128], ",")+"]")

	// control placements
	ok("chat.model_control_placements", `{"web":"toolbar","img":"menu"}`)
	ok("chat.model_control_placements", `{}`)
	seven := `{"a":"toolbar","b":"toolbar","c":"toolbar","d":"toolbar","e":"toolbar","f":"toolbar","g":"toolbar"}`
	bad("chat.model_control_placements", seven)
	ok("chat.model_control_placements", `{"a":"toolbar","b":"toolbar","c":"toolbar","d":"toolbar","e":"toolbar","f":"toolbar"}`)
	bad("chat.model_control_placements", `{"`+strings.Repeat("x", 97)+`":"menu"}`)
	ok("chat.model_control_placements", `{"`+strings.Repeat("x", 96)+`":"menu"}`)
	bad("chat.model_control_placements", `{"a":"dock"}`)
	bad("chat.model_control_placements", `{" ":"menu"}`)
	bad("chat.model_control_placements", `[]`)
	bad("chat.model_control_placements", `{"a":1}`)
}

func TestUserSettingsKeysAreTrimmedValuesAreNot(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"  chat.file_mode ": "rag"}})
	testkit.Expect(t, w, 200, "")
	if userSettings(t, h, h.user)["chat.file_mode"] != "rag" {
		t.Fatal("key should be trimmed")
	}
	// values keep their whitespace and are validated as given
	w = h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.file_mode": " rag"}})
	testkit.Expect(t, w, 400, "user_settings.invalid_value")
}

func TestUserSettingsIgnoreStoredRowsOfRemovedKeys(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.env.DB.Create(&settings.UserSetting{UserID: 7, Key: "chat.removed_option", Value: "x"})
	got := userSettings(t, h, h.user)
	if _, ok := got["chat.removed_option"]; ok || len(got) != 23 {
		t.Fatalf("removed key must not be returned: %v", got)
	}
}

func TestUserSettingLookup(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	ctx := ctxBG()
	if _, ok, err := h.svc.UserSetting(ctx, 7, "chat.file_mode"); ok || err != nil {
		t.Fatalf("unset: %v %v", ok, err)
	}
	h.do("PATCH", "/api/v1/user/settings", h.user, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}})
	if v, ok, err := h.svc.UserSetting(ctx, 7, "chat.file_mode"); v != "rag" || !ok || err != nil {
		t.Fatalf("set: %q %v %v", v, ok, err)
	}
	if _, ok, _ := h.svc.UserSetting(ctx, 7, "chat.unknown"); ok {
		t.Fatal("unknown key")
	}
}
