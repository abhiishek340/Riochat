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

func TestSettingsStoreDeleteAll(t *testing.T) {
	env := newEnv(t, Ports{})
	patchSettings(env, 1, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}})
	patchSettings(env, 2, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}})
	store := NewSettingsStore(env.DB, env.Clock, nil, nil, nil)
	if err := store.DeleteAll(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var n int64
	env.DB.Model(&Setting{}).Where("user_id = 1").Count(&n)
	if n != 0 {
		t.Fatal("rows remain")
	}
	env.DB.Model(&Setting{}).Where("user_id = 2").Count(&n)
	if n != 1 {
		t.Fatal("other user's rows must stay")
	}
}

func TestWriteHookReceivesCommittedKeys(t *testing.T) {
	var mu sync.Mutex
	var gotUser uint
	var gotKeys []string
	env := newEnv(t, Ports{OnSettingsWritten: func(_ context.Context, uid uint, keys []string) {
		mu.Lock()
		defer mu.Unlock()
		gotUser, gotKeys = uid, append([]string(nil), keys...)
	}})
	patchSettings(env, 7, map[string]any{"settings": map[string]string{"chat.file_mode": "rag", " chat.markdown_render": "false"}})
	mu.Lock()
	defer mu.Unlock()
	if gotUser != 7 || len(gotKeys) != 2 || gotKeys[0] != "chat.file_mode" || gotKeys[1] != "chat.markdown_render" {
		t.Fatalf("hook got %d %v", gotUser, gotKeys)
	}
	// Failed requests never reach the hook.
	gotUser = 0
	mu.Unlock()
	patchSettings(env, 8, map[string]any{"settings": map[string]string{"chat.file_mode": "bogus"}})
	mu.Lock()
	if gotUser != 0 {
		t.Fatal("hook ran for a rejected request")
	}
}

// ---- cache --------------------------------------------------------------------

// flakyKV wraps a cache and fails on demand.
type flakyKV struct {
	cache.KV
	mu       sync.Mutex
	failGet  bool
	failSet  bool
	failAll  bool
	getCalls int
}

var errCacheDown = errors.New("cache down")

func (f *flakyKV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	f.mu.Lock()
	f.getCalls++
	fail := f.failGet || f.failAll
	f.mu.Unlock()
	if fail {
		return nil, false, errCacheDown
	}
	return f.KV.Get(ctx, key)
}

func (f *flakyKV) Set(ctx context.Context, key string, v []byte, ttl time.Duration) error {
	f.mu.Lock()
	fail := f.failSet || f.failAll
	f.mu.Unlock()
	if fail {
		return errCacheDown
	}
	return f.KV.Set(ctx, key, v, ttl)
}

func (f *flakyKV) SetNX(ctx context.Context, key string, v []byte, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	fail := f.failAll
	f.mu.Unlock()
	if fail {
		return false, errCacheDown
	}
	return f.KV.SetNX(ctx, key, v, ttl)
}

func cacheFixture(t *testing.T) (*testkit.Env, *flakyKV, *SettingsCache, *SettingsStore) {
	t.Helper()
	env := newEnv(t, Ports{})
	kv := &flakyKV{KV: env.Deps.Cache}
	c := NewSettingsCache(env.DB, kv, nil)
	return env, kv, c, NewSettingsStore(env.DB, env.Clock, c, nil, nil)
}

func TestCacheUnsetKeyReadsAsEmptyString(t *testing.T) {
	env, _, c, _ := cacheFixture(t)
	_ = env
	v, err := c.Get(context.Background(), 1, SettingContextCompactAuto)
	if err != nil || v != "" {
		t.Fatalf("got %q, %v; want empty (not the catalog default)", v, err)
	}
	v2, ok, err := c.UserSetting(context.Background(), 1, SettingContextCompactAuto)
	if err != nil || ok || v2 != "" {
		t.Fatalf("UserSetting = %q %v %v", v2, ok, err)
	}
}

func TestCacheServesWrittenValueImmediately(t *testing.T) {
	_, _, c, store := cacheFixture(t)
	ctx := context.Background()
	// Warm the cache with the "unset" answer, as an older read would.
	for _, k := range []string{SettingContextCompactAuto, SettingFileMode, SettingReasoningContentPassing} {
		if v, _ := c.Get(ctx, 1, k); v != "" {
			t.Fatalf("%s warm value %q", k, v)
		}
	}
	if _, err := store.Patch(ctx, 1, map[string]string{
		SettingContextCompactAuto: "false", SettingFileMode: "rag", SettingReasoningContentPassing: "false"}); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{SettingContextCompactAuto: "false", SettingFileMode: "rag", SettingReasoningContentPassing: "false"} {
		if v, err := c.Get(ctx, 1, k); err != nil || v != want {
			t.Errorf("%s = %q, %v; want %q", k, v, err, want)
		}
	}
}

func TestCacheSecondInstanceSharingTheCacheSeesWrites(t *testing.T) {
	env, _, c, store := cacheFixture(t)
	other := NewSettingsCache(env.DB, env.Deps.Cache, nil)
	ctx := context.Background()
	other.Get(ctx, 1, SettingFileMode)
	if _, err := store.Patch(ctx, 1, map[string]string{SettingFileMode: "rag"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := other.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("second instance read %q", v)
	}
	_ = c
}

func TestCacheStaleInFlightReadCannotOverwriteNewValue(t *testing.T) {
	env, kv, c, store := cacheFixture(t)
	ctx := context.Background()
	// An old reader resolved the version and loaded the old DB value, but has not
	// stored it yet.
	oldToken, err := c.version(ctx, 1, SettingFileMode)
	if err != nil {
		t.Fatal(err)
	}
	staleValue := "auto-from-old-read"
	if _, err := store.Patch(ctx, 1, map[string]string{SettingFileMode: "rag"}); err != nil {
		t.Fatal(err)
	}
	// The slow reader finally writes under the version it had.
	if err := kv.Set(ctx, valueKey(1, SettingFileMode, oldToken), []byte(staleValue), settingsCacheTTL); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("stale write won: %q", v)
	}
	// And a concurrent second update still wins over the first.
	if _, err := store.Patch(ctx, 1, map[string]string{SettingFileMode: "full_context"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "full_context" {
		t.Fatalf("got %q", v)
	}
	_ = env
}

func TestCacheFillsFromDatabaseAndThenServesFromCache(t *testing.T) {
	env, _, c, _ := cacheFixture(t)
	ctx := context.Background()
	env.DB.Create(&Setting{UserID: 1, Key: SettingFileMode, Value: "rag", UpdatedAt: env.Clock.Now()})
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("got %q", v)
	}
	// Change the row behind the cache's back: the cached value is served.
	env.DB.Model(&Setting{}).Where(map[string]any{"user_id": 1, "key": SettingFileMode}).Update("value", "full_context")
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("expected cached value, got %q", v)
	}
}

func TestCacheEntriesExpireAfterTenMinutes(t *testing.T) {
	// The memory cache uses wall time, so check the configured lifetime instead.
	if settingsCacheTTL != 10*time.Minute {
		t.Fatalf("ttl = %v", settingsCacheTTL)
	}
}

func TestCacheBackendDownFallsBackToDatabase(t *testing.T) {
	env, kv, c, store := cacheFixture(t)
	ctx := context.Background()
	env.DB.Create(&Setting{UserID: 1, Key: SettingFileMode, Value: "rag", UpdatedAt: env.Clock.Now()})
	kv.mu.Lock()
	kv.failAll = true
	kv.mu.Unlock()
	if v, err := c.Get(ctx, 1, SettingFileMode); err != nil || v != "rag" {
		t.Fatalf("read with cache down: %q, %v", v, err)
	}
	if v, err := c.Get(ctx, 1, SettingContextCompactAuto); err != nil || v != "" {
		t.Fatalf("unset read with cache down: %q, %v", v, err)
	}
	got, err := store.Patch(ctx, 1, map[string]string{SettingFileMode: "full_context"})
	if err != nil || got[SettingFileMode] != "full_context" {
		t.Fatalf("patch with cache down: %v, %v", got, err)
	}
}

func TestCacheValueReadFailureStillReturnsDatabaseValue(t *testing.T) {
	env, kv, c, _ := cacheFixture(t)
	env.DB.Create(&Setting{UserID: 1, Key: SettingFileMode, Value: "rag", UpdatedAt: env.Clock.Now()})
	c.Get(context.Background(), 1, SettingFileMode) // create the version
	kv.mu.Lock()
	kv.failGet = true
	kv.mu.Unlock()
	if v, err := c.Get(context.Background(), 1, SettingFileMode); err != nil || v != "rag" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestPatchWithClosedCacheStillSucceedsOverHTTP(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.Deps.Cache.Close(); err != nil {
		t.Fatal(err)
	}
	got, code, _, _ := patchSettings(env, 1, map[string]any{"settings": map[string]string{SettingFileMode: "rag"}})
	if code != 200 || got[SettingFileMode] != "rag" {
		t.Fatalf("%d %v", code, got)
	}
	if getSettings(t, env, 1)[SettingFileMode] != "rag" {
		t.Fatal("not stored")
	}
}

func TestNilCacheReadsDatabaseDirectly(t *testing.T) {
	env := newEnv(t, Ports{})
	c := NewSettingsCache(env.DB, nil, nil)
	env.DB.Create(&Setting{UserID: 1, Key: SettingFileMode, Value: "rag", UpdatedAt: env.Clock.Now()})
	if v, err := c.Get(context.Background(), 1, SettingFileMode); err != nil || v != "rag" {
		t.Fatalf("got %q, %v", v, err)
	}
	c.Refresh(context.Background(), 1, []string{SettingFileMode}) // must not panic
	var nilCache *SettingsCache
	nilCache.Refresh(context.Background(), 1, []string{"x"})
}

func TestFailedWriteLeavesCacheUntouched(t *testing.T) {
	env, _, c, store := cacheFixture(t)
	ctx := context.Background()
	env.DB.Create(&Setting{UserID: 1, Key: SettingFileMode, Value: "rag", UpdatedAt: env.Clock.Now()})
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("warm: %q", v)
	}
	// Break the table so the upsert fails.
	if err := env.DB.Exec("ALTER TABLE user_settings RENAME TO user_settings_gone").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Patch(ctx, 1, map[string]string{SettingFileMode: "full_context"}); err == nil {
		t.Fatal("write should have failed")
	}
	env.DB.Exec("ALTER TABLE user_settings_gone RENAME TO user_settings")
	if v, _ := c.Get(ctx, 1, SettingFileMode); v != "rag" {
		t.Fatalf("cached value changed: %q", v)
	}
}

func TestStorageFailureIs500WithoutLeakingDetails(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Exec("DROP TABLE user_settings").Error; err != nil {
		t.Fatal(err)
	}
	w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(w.Body.String(), "user_settings") {
		t.Fatalf("leaked detail: %s", w.Body)
	}
	w = env.Do("PATCH", "/api/v1/user/settings", env.Bearer(testkit.User(1)), map[string]any{"settings": map[string]string{SettingFileMode: "rag"}})
	testkit.Expect(t, w, 500, "internal.error")
}

func TestConcurrentPatchesDoNotCorruptRows(t *testing.T) {
	env := newEnv(t, Ports{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mode := []string{"auto", "rag", "full_context"}[i%3]
			patchSettings(env, 1, map[string]any{"settings": map[string]string{SettingFileMode: mode, "chat.default_model": fmt.Sprint(i)}})
		}(i)
	}
	wg.Wait()
	var n int64
	env.DB.Model(&Setting{}).Where("user_id = 1").Count(&n)
	if n != 2 {
		t.Fatalf("rows: %d", n)
	}
}
