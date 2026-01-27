package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type settingsBody struct {
	Settings map[string]string `json:"settings"`
}

func getSettings(t *testing.T, env *testkit.Env, uid uint) map[string]string {
	t.Helper()
	w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(uid)), nil)
	testkit.Expect(t, w, 200, "")
	var out settingsBody
	testkit.Decode(t, w, &out)
	return out.Settings
}

func patchSettings(env *testkit.Env, uid uint, body any) (map[string]string, int, string, string) {
	w := env.Do("PATCH", "/api/v1/user/settings", env.Bearer(testkit.User(uid)), body)
	envl := testkit.Envelope(env.T, w)
	var out settingsBody
	if w.Code == 200 {
		testkit.Decode(env.T, w, &out)
	}
	return out.Settings, w.Code, envl.ErrorCode, envl.ErrorMsg
}

func TestSettingsRequireAuthentication(t *testing.T) {
	env := newEnv(t, Ports{})
	testkit.Expect(t, env.Do("GET", "/api/v1/user/settings", nil, nil), 401, "auth.invalid_token")
	testkit.Expect(t, env.Do("PATCH", "/api/v1/user/settings", nil, `{"settings":{}}`), 401, "auth.invalid_token")
	testkit.Expect(t, env.Do("GET", "/api/v1/user/stats/activity", nil, nil), 401, "auth.invalid_token")
}

func TestGetSettingsFreshUserReturnsAllDefaults(t *testing.T) {
	env := newEnv(t, Ports{})
	got := getSettings(t, env, 1)
	if len(got) != 23 {
		t.Fatalf("got %d keys", len(got))
	}
	for k, v := range SettingDefaults() {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["chat.file_mode"] != "auto" || got["chat.default_mcp_tool_ids"] != "[]" ||
		got["chat.model_control_placements"] != "{}" || got["chat.content_width"] != "compact" ||
		got["chat.delete_conversation_files_by_default"] != "false" {
		t.Fatalf("unexpected defaults: %v", got)
	}
}

func TestPatchSettingsChangesOnlyTheNamedKey(t *testing.T) {
	env := newEnv(t, Ports{})
	got, code, _, _ := patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.send_on_enter": "ctrl_enter"}})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got["chat.send_on_enter"] != "ctrl_enter" {
		t.Fatalf("not updated: %v", got)
	}
	want := SettingDefaults()
	want["chat.send_on_enter"] = "ctrl_enter"
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	again := getSettings(t, env, 1)
	if again["chat.send_on_enter"] != "ctrl_enter" {
		t.Fatalf("not persisted: %v", again)
	}
}

func TestPatchSettingsUnknownKey(t *testing.T) {
	env := newEnv(t, Ports{})
	_, code, ec, msg := patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.bogus": "x"}})
	if code != 400 || ec != "user_settings.unknown_key" || msg != "unknown setting key" {
		t.Fatalf("%d %s %q", code, ec, msg)
	}
}

func TestPatchSettingsInvalidValue(t *testing.T) {
	env := newEnv(t, Ports{})
	_, code, ec, msg := patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.markdown_render": "yes"}})
	if code != 400 || ec != "user_settings.invalid_value" || msg != "invalid user setting value" {
		t.Fatalf("%d %s %q", code, ec, msg)
	}
	if strings.Contains(msg, "markdown") {
		t.Fatal("message must not name the key")
	}
}

func TestPatchSettingsIsAllOrNothing(t *testing.T) {
	env := newEnv(t, Ports{})
	_, code, ec, _ := patchSettings(env, 1, map[string]any{"settings": map[string]string{
		"chat.file_mode": "auto", "chat.content_width": "loose"}})
	if code != 400 || ec != "user_settings.invalid_value" {
		t.Fatalf("%d %s", code, ec)
	}
	_, code, ec, _ = patchSettings(env, 1, map[string]any{"settings": map[string]string{
		"chat.file_mode": "rag", "chat.nope": "x"}})
	if code != 400 || ec != "user_settings.unknown_key" {
		t.Fatalf("%d %s", code, ec)
	}
	var n int64
	env.DB.Model(&Setting{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d rows written by failed requests", n)
	}
	if getSettings(t, env, 1)["chat.file_mode"] != "auto" {
		t.Fatal("file_mode changed")
	}
}

func TestPatchSettingsBodyErrors(t *testing.T) {
	env := newEnv(t, Ports{})
	for name, body := range map[string]any{
		"missing settings":   `{}`,
		"null settings":      `{"settings":null}`,
		"array settings":     `{"settings":[]}`,
		"string settings":    `{"settings":"x"}`,
		"not json":           `not json`,
		"truncated json":     `{"settings":{`,
		"empty body":         ``,
		"top-level array":    `[]`,
		"settings is number": `{"settings":5}`,
	} {
		_, code, ec, _ := patchSettings(env, 1, body)
		if code != 400 || ec != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, code, ec)
		}
	}
	w := env.Do("PATCH", "/api/v1/user/settings", env.Bearer(testkit.User(1)), "")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "request body is required" {
		t.Errorf("empty body message %q", msg)
	}
}

func TestPatchSettingsWrongValueTypesNameTheField(t *testing.T) {
	env := newEnv(t, Ports{})
	w := env.Do("PATCH", "/api/v1/user/settings", env.Bearer(testkit.User(1)), `{"settings":{"a":1}}`)
	if e := testkit.Envelope(t, w); w.Code != 400 || e.ErrorCode != "request.invalid_body" || e.ErrorMsg != "settings.a has invalid type" {
		t.Fatalf("number: %d %+v", w.Code, e)
	}
	for _, frag := range []string{`"reason":"json_type"`, `"field":"settings.a"`, `"expected":"string"`, `"value":"number"`} {
		if !strings.Contains(w.Body.String(), frag) {
			t.Fatalf("details lack %s: %s", frag, w.Body.String())
		}
	}
	_, code, ec, msg := patchSettings(env, 1, `{"settings":{"chat.markdown_render":true}}`)
	if code != 400 || ec != "request.invalid_body" || msg != "settings.chat.markdown_render has invalid type" {
		t.Fatalf("bool: %d %s %s", code, ec, msg)
	}
	// A null is read as an empty string, so an unknown key is reported as such.
	_, code, ec, msg = patchSettings(env, 1, `{"settings":{"a":null}}`)
	if code != 400 || ec != "user_settings.unknown_key" || msg != "unknown setting key" {
		t.Fatalf("null unknown key: %d %s %s", code, ec, msg)
	}
	_, code, ec, _ = patchSettings(env, 1, `{"settings":{"chat.file_mode":null}}`)
	if code != 400 || ec != "user_settings.invalid_value" {
		t.Fatalf("null known key: %d %s", code, ec)
	}
}

func TestPatchSettingsEmptyMapIsANoOp(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}})
	got, code, _, _ := patchSettings(env, 1, `{"settings":{}}`)
	if code != 200 || got["chat.file_mode"] != "rag" || len(got) != 23 {
		t.Fatalf("%d %v", code, got)
	}
	var n int64
	env.DB.Model(&Setting{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestPatchSettingsTrimsKeysButNotValues(t *testing.T) {
	env := newEnv(t, Ports{})
	got, code, _, _ := patchSettings(env, 1, map[string]any{"settings": map[string]string{
		" chat.show_latency ": "false", "chat.default_model": "  my model  "}})
	if code != 200 || got["chat.show_latency"] != "false" || got["chat.default_model"] != "  my model  " {
		t.Fatalf("%d %v", code, got)
	}
	var row Setting
	if err := env.DB.Where("user_id = 1 AND "+"`key`"+" = ?", "chat.show_latency").Take(&row).Error; err != nil {
		t.Fatalf("row stored under the trimmed key: %v", err)
	}
}

func TestPatchSettingsCollapsedKeysDoNotFail(t *testing.T) {
	env := newEnv(t, Ports{})
	got, code, _, _ := patchSettings(env, 1, map[string]any{"settings": map[string]string{
		"chat.file_mode": "auto", " chat.file_mode ": "rag"}})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if v := got["chat.file_mode"]; v != "auto" && v != "rag" {
		t.Fatalf("value %q", v)
	}
	var n int64
	env.DB.Model(&Setting{}).Where("user_id = 1").Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestPatchSettingsUpdatesInPlace(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "rag", "chat.input_height": "loose"}})
	var first Setting
	env.DB.Where(map[string]any{"user_id": 1, "key": "chat.file_mode"}).Take(&first)
	env.Clock.Advance(time.Hour)
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "full_context"}})
	var second Setting
	env.DB.Where(map[string]any{"user_id": 1, "key": "chat.file_mode"}).Take(&second)
	if second.ID != first.ID || second.Value != "full_context" || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("not updated in place: %+v -> %+v", first, second)
	}
	var other Setting
	env.DB.Where(map[string]any{"user_id": 1, "key": "chat.input_height"}).Take(&other)
	if !other.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("untouched key must keep its timestamp")
	}
	var n int64
	env.DB.Model(&Setting{}).Count(&n)
	if n != 2 {
		t.Fatalf("rows: %d", n)
	}
}

func TestPatchSettingsStampsOneTimestampPerRequest(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "rag", "chat.input_height": "loose", "chat.default_model": "m"}})
	var rows []Setting
	env.DB.Find(&rows)
	for _, r := range rows[1:] {
		if !r.UpdatedAt.Equal(rows[0].UpdatedAt) {
			t.Fatalf("timestamps differ: %v vs %v", r.UpdatedAt, rows[0].UpdatedAt)
		}
	}
}

func TestSettingsArePerUser(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}})
	patchSettings(env, 2, map[string]any{"settings": map[string]string{"chat.file_mode": "full_context"}})
	if getSettings(t, env, 1)["chat.file_mode"] != "rag" || getSettings(t, env, 2)["chat.file_mode"] != "full_context" {
		t.Fatal("settings leaked across users")
	}
	if getSettings(t, env, 3)["chat.file_mode"] != "auto" {
		t.Fatal("third user must see defaults")
	}
}

func TestUserIDInBodyIsIgnored(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, `{"user_id":2,"userId":2,"settings":{"chat.file_mode":"rag"}}`)
	if getSettings(t, env, 2)["chat.file_mode"] != "auto" {
		t.Fatal("request must not choose the user")
	}
}

func TestStoredLegacyKeysAreNotReturned(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Create(&Setting{UserID: 1, Key: "chat.legacy_thing", Value: "x", UpdatedAt: env.Clock.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.DB.Create(&Setting{UserID: 1, Key: "chat.file_mode", Value: "rag", UpdatedAt: env.Clock.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	got := getSettings(t, env, 1)
	if _, leaked := got["chat.legacy_thing"]; leaked || len(got) != 23 || got["chat.file_mode"] != "rag" {
		t.Fatalf("unexpected: %v", got)
	}
	var n int64
	env.DB.Model(&Setting{}).Where("user_id = 1").Count(&n)
	if n != 2 {
		t.Fatalf("legacy row must not be deleted, rows=%d", n)
	}
}

func TestUniqueSettingRowPerUserAndKey(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Create(&Setting{UserID: 1, Key: "chat.file_mode", Value: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.DB.Create(&Setting{UserID: 1, Key: "chat.file_mode", Value: "b"}).Error; err == nil {
		t.Fatal("(user, key) must be unique")
	}
	if err := env.DB.Create(&Setting{UserID: 2, Key: "chat.file_mode", Value: "b"}).Error; err != nil {
		t.Fatalf("other user may reuse the key: %v", err)
	}
}
