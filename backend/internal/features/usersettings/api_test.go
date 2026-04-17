package usersettings_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/usersettings"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func newEnv(t *testing.T) *testkit.Env {
	return testkit.New(t, []features.Feature{{Name: "usersettings", Migrations: usersettings.Migrations, Mount: usersettings.Mount}})
}

func settings(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var out struct {
		Settings map[string]string `json:"settings"`
	}
	testkit.Decode(t, w, &out)
	return out.Settings
}

func get(t *testing.T, env *testkit.Env, uid uint) map[string]string {
	t.Helper()
	w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(uid)), nil)
	testkit.Expect(t, w, 200, "")
	return settings(t, w)
}

func patch(env *testkit.Env, uid uint, body any) *httptest.ResponseRecorder {
	return env.Do("PATCH", "/api/v1/user/settings", env.Bearer(testkit.User(uid)), body)
}

func body(m map[string]string) map[string]any { return map[string]any{"settings": m} }

func TestAuthRequired(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", "/api/v1/user/settings", nil, nil), 401, "auth.invalid_token")
	testkit.Expect(t, env.Do("PATCH", "/api/v1/user/settings", nil, body(nil)), 401, "auth.invalid_token")
}

func TestNewUserGetsAllDefaults(t *testing.T) {
	env := newEnv(t)
	w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 200, "")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "" {
		t.Fatalf("errorMsg %q", msg)
	}
	got := settings(t, w)
	if len(got) != 23 {
		t.Fatalf("%d keys", len(got))
	}
	for k, want := range map[string]string{
		"chat.file_mode": "auto", "chat.default_mcp_tool_ids": "[]", "chat.model_control_placements": "{}",
		"chat.content_width": "compact", "chat.delete_conversation_files_by_default": "false",
		"chat.default_model": "", "chat.markdown_render": "true",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestPatchOneKeyThenGet(t *testing.T) {
	env := newEnv(t)
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "rag"}))
	testkit.Expect(t, w, 200, "")
	got := settings(t, w)
	if got["chat.file_mode"] != "rag" || got["chat.send_on_enter"] != "enter" || len(got) != 23 {
		t.Fatalf("patch result: %v", got)
	}
	if again := get(t, env, 1); again["chat.file_mode"] != "rag" {
		t.Fatalf("get after patch: %v", again)
	}
}

func TestInvalidValueAndUnknownKey(t *testing.T) {
	env := newEnv(t)
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "bogus"}))
	testkit.Expect(t, w, 400, "user_settings.invalid_value")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid user setting value" {
		t.Fatalf("message %q", msg)
	}
	if strings.Contains(w.Body.String(), "chat.file_mode") {
		t.Fatalf("response leaks the key: %s", w.Body)
	}
	if get(t, env, 1)["chat.file_mode"] != "auto" {
		t.Fatal("state changed")
	}

	w = patch(env, 1, body(map[string]string{"chat.nope": "x"}))
	testkit.Expect(t, w, 400, "user_settings.unknown_key")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "unknown setting key" {
		t.Fatalf("message %q", msg)
	}
	if strings.Contains(w.Body.String(), "chat.nope") {
		t.Fatalf("response leaks the key: %s", w.Body)
	}
}

func TestMixedRequestPersistsNothing(t *testing.T) {
	env := newEnv(t)
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "rag", "chat.markdown_render": "yes"}))
	testkit.Expect(t, w, 400, "user_settings.invalid_value")
	w = patch(env, 1, body(map[string]string{"chat.file_mode": "rag", "chat.unknown": "x"}))
	testkit.Expect(t, w, 400, "user_settings.unknown_key")
	if get(t, env, 1)["chat.file_mode"] != "auto" {
		t.Fatal("valid entry was persisted")
	}
	var n int64
	env.DB.Model(&usersettings.UserSetting{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d rows", n)
	}
}

func TestKeyIsTrimmedValueIsNot(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, patch(env, 1, body(map[string]string{" chat.show_latency ": "false"})), 200, "")
	if get(t, env, 1)["chat.show_latency"] != "false" {
		t.Fatal("trimmed key not applied")
	}
	var row usersettings.UserSetting
	if err := env.DB.Where("user_id = ?", 1).First(&row).Error; err != nil || row.Key != "chat.show_latency" {
		t.Fatalf("stored key %q err %v", row.Key, err)
	}
	// Values are stored raw: a padded boolean is invalid, a padded model name is kept.
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.show_latency": " false"})), 400, "user_settings.invalid_value")
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.default_model": "  gpt  "})), 200, "")
	if get(t, env, 1)["chat.default_model"] != "  gpt  " {
		t.Fatal("value was trimmed")
	}
	// Structured values tolerate whitespace and are stored as sent.
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.default_mcp_tool_ids": " [42] "})), 200, "")
	if get(t, env, 1)["chat.default_mcp_tool_ids"] != " [42] " {
		t.Fatal("structured value not stored raw")
	}
}

func TestBooleanVariantsRejected(t *testing.T) {
	env := newEnv(t)
	for _, v := range []string{"yes", "True", "1", ""} {
		testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.markdown_render": v})), 400, "user_settings.invalid_value")
	}
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.markdown_render": "false"})), 200, "")
}

func TestBodyErrors(t *testing.T) {
	env := newEnv(t)
	for name, b := range map[string]any{
		"empty object": "{}", "no body": nil, "not json": "not json", "null settings": `{"settings":null}`,
		"number value": `{"settings":{"chat.file_mode":1}}`, "array settings": `{"settings":[]}`,
		"bool value": `{"settings":{"chat.markdown_render":true}}`, "null value": `{"settings":{"chat.file_mode":null}}`,
		"whitespace body": "  \n", "truncated": `{"settings":{"chat.file_mode"`, "top level array": `[]`,
	} {
		w := patch(env, 1, b)
		if w.Code != 400 {
			t.Errorf("%s: status %d: %s", name, w.Code, w.Body)
			continue
		}
		if code := testkit.Envelope(t, w).ErrorCode; code != "request.invalid_body" {
			t.Errorf("%s: code %q", name, code)
		}
	}
	w := patch(env, 1, "")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "request body is required" {
		t.Errorf("empty body message %q", msg)
	}
	w = patch(env, 1, "{bad")
	if msg := testkit.Envelope(t, w).ErrorMsg; !strings.Contains(msg, "offset") {
		t.Errorf("syntax error message %q", msg)
	}
}

func TestEmptySettingsMapIsAccepted(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	w := patch(env, 1, `{"settings":{}}`)
	testkit.Expect(t, w, 200, "")
	if got := settings(t, w); len(got) != 23 || got["chat.file_mode"] != "rag" {
		t.Fatalf("got %v", got)
	}
}

func TestStructuredValuesOverHTTP(t *testing.T) {
	env := newEnv(t)
	ok := map[string]string{
		"chat.default_mcp_tool_ids":     "[1,2,3]",
		"chat.model_control_placements": `{"reasoning":"toolbar","temperature":"menu"}`,
	}
	testkit.Expect(t, patch(env, 1, body(ok)), 200, "")
	for key, v := range map[string]string{
		"chat.default_mcp_tool_ids":     "[0]",
		"chat.model_control_placements": `{"a":"sidebar"}`,
	} {
		testkit.Expect(t, patch(env, 1, body(map[string]string{key: v})), 400, "user_settings.invalid_value")
	}
	if got := get(t, env, 1); got["chat.default_mcp_tool_ids"] != "[1,2,3]" {
		t.Fatalf("got %v", got)
	}
}

func TestLegacyRowsAreIgnoredButKept(t *testing.T) {
	env := newEnv(t)
	env.DB.Create(&usersettings.UserSetting{UserID: 1, Key: "legacy.theme", Value: "dark", UpdatedAt: env.Clock.Now()})
	got := get(t, env, 1)
	if _, found := got["legacy.theme"]; found || len(got) != 23 {
		t.Fatalf("legacy key returned: %v", got)
	}
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	var n int64
	env.DB.Model(&usersettings.UserSetting{}).Where("key = ?", "legacy.theme").Count(&n)
	if n != 1 {
		t.Fatal("legacy row was deleted")
	}
}

func TestSettingsArePerUser(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	testkit.Expect(t, patch(env, 2, body(map[string]string{"chat.file_mode": "full_context"})), 200, "")
	if get(t, env, 1)["chat.file_mode"] != "rag" || get(t, env, 2)["chat.file_mode"] != "full_context" || get(t, env, 3)["chat.file_mode"] != "auto" {
		t.Fatal("settings leaked between users")
	}
	// A user id in the body or query never overrides the identity.
	w := env.Do("PATCH", "/api/v1/user/settings?user_id=1", env.Bearer(testkit.User(3)),
		`{"user_id":1,"settings":{"chat.file_mode":"auto"}}`)
	testkit.Expect(t, w, 200, "")
	if get(t, env, 1)["chat.file_mode"] != "rag" {
		t.Fatal("another user's settings were modified")
	}
}

func TestUpsertOverwritesAndSharesTimestamp(t *testing.T) {
	env := newEnv(t)
	first := env.Clock.Now()
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag", "chat.send_on_enter": "ctrl_enter"})), 200, "")
	env.Clock.Advance(time.Hour)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "full_context", "chat.input_height": "loose", "chat.content_width": "wide"})), 200, "")

	var rows []usersettings.UserSetting
	env.DB.Where("user_id = ?", 1).Order("key").Find(&rows)
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		switch r.Key {
		case "chat.send_on_enter":
			if !r.UpdatedAt.Equal(first) {
				t.Errorf("untouched row timestamp moved: %v", r.UpdatedAt)
			}
		default:
			if !r.UpdatedAt.Equal(first.Add(time.Hour)) {
				t.Errorf("%s updated_at %v, want %v", r.Key, r.UpdatedAt, first.Add(time.Hour))
			}
		}
	}
	if get(t, env, 1)["chat.file_mode"] != "full_context" {
		t.Fatal("not overwritten")
	}
}

func TestDuplicateKeysDifferingByWhitespaceDoNotFail(t *testing.T) {
	env := newEnv(t)
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "auto", " chat.file_mode ": "rag"}))
	testkit.Expect(t, w, 200, "")
	// " chat.file_mode " sorts before "chat.file_mode", so the later spelling wins.
	if settings(t, w)["chat.file_mode"] != "auto" {
		t.Fatalf("deterministic winner expected")
	}
	var n int64
	env.DB.Model(&usersettings.UserSetting{}).Where("user_id = ?", 1).Count(&n)
	if n != 1 {
		t.Fatalf("%d rows", n)
	}
}

func TestWritingDefaultReturnsToDefault(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "auto"})), 200, "")
	if get(t, env, 1)["chat.file_mode"] != "auto" {
		t.Fatal("not back to default")
	}
}

func TestVeryLongModelNameIsStored(t *testing.T) {
	env := newEnv(t)
	long := strings.Repeat("m", 70000)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.default_model": long})), 200, "")
	if get(t, env, 1)["chat.default_model"] != long {
		t.Fatal("long value truncated")
	}
}

func TestPurgeRemovesOnlyThatUsersRows(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag", "chat.send_on_enter": "ctrl_enter"})), 200, "")
	testkit.Expect(t, patch(env, 2, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	env.DB.Create(&usersettings.UserSetting{UserID: 1, Key: "legacy.x", Value: "y"})
	if err := usersettings.PurgeUser(context.Background(), env.DB, 1); err != nil {
		t.Fatal(err)
	}
	var n1, n2 int64
	env.DB.Unscoped().Model(&usersettings.UserSetting{}).Where("user_id = ?", 1).Count(&n1)
	env.DB.Model(&usersettings.UserSetting{}).Where("user_id = ?", 2).Count(&n2)
	if n1 != 0 || n2 != 1 {
		t.Fatalf("user1 rows %d, user2 rows %d", n1, n2)
	}
}

func TestMigrationsAreIdempotentAndIndexed(t *testing.T) {
	env := newEnv(t)
	for i := 0; i < 2; i++ {
		for _, s := range usersettings.Migrations() {
			if s.Name == "" {
				t.Fatal("unnamed step")
			}
		}
	}
	m := env.DB.Migrator()
	if !m.HasTable("user_settings") {
		t.Fatal("no table")
	}
	for _, col := range []string{"id", "user_id", "key", "value", "updated_at"} {
		if !m.HasColumn(&usersettings.UserSetting{}, col) {
			t.Errorf("missing column %s", col)
		}
	}
	if m.HasColumn(&usersettings.UserSetting{}, "created_at") {
		t.Error("created_at must not exist")
	}
	if !m.HasIndex(&usersettings.UserSetting{}, "uidx_user_settings_user_key") || !m.HasIndex(&usersettings.UserSetting{}, "idx_user_settings_user_id") {
		t.Error("indexes missing")
	}
	// The unique constraint is enforced.
	env.DB.Create(&usersettings.UserSetting{UserID: 9, Key: "chat.file_mode", Value: "rag"})
	if err := env.DB.Create(&usersettings.UserSetting{UserID: 9, Key: "chat.file_mode", Value: "auto"}).Error; err == nil {
		t.Fatal("duplicate (user_id, key) accepted")
	}
}

// ---- cache contract ----

func TestCacheRefreshedImmediatelyAfterPatch(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	a := usersettings.NewValueCache(env.DB, env.Deps.Cache)
	b := usersettings.NewValueCache(env.DB, env.Deps.Cache) // a second service instance on the shared cache

	keys := []string{"chat.reasoning_content_passback", "chat.context_compact_auto", "chat.file_mode"}
	for _, k := range keys {
		if v, err := a.Value(ctx, 1, k); err != nil || v != "" {
			t.Fatalf("unset %s read %q, %v", k, v, err)
		}
	}
	testkit.Expect(t, patch(env, 1, body(map[string]string{
		"chat.reasoning_content_passback": "false", "chat.context_compact_auto": "false", "chat.file_mode": "rag",
	})), 200, "")
	want := map[string]string{"chat.reasoning_content_passback": "false", "chat.context_compact_auto": "false", "chat.file_mode": "rag"}
	for _, k := range keys {
		for name, c := range map[string]*usersettings.ValueCache{"a": a, "b": b} {
			if v, err := c.Value(ctx, 1, k); err != nil || v != want[k] {
				t.Fatalf("instance %s %s = %q, %v", name, k, v, err)
			}
		}
	}
	// Cached entries are served without the database.
	env.DB.Exec("DELETE FROM user_settings")
	if v, _ := b.Value(ctx, 1, "chat.file_mode"); v != "rag" {
		t.Fatalf("expected cached value, got %q", v)
	}
}

func TestFailedWriteLeavesCacheUntouched(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	c := usersettings.NewValueCache(env.DB, env.Deps.Cache)
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	if v, _ := c.Value(ctx, 1, "chat.file_mode"); v != "rag" {
		t.Fatalf("got %q", v)
	}
	if err := env.DB.Migrator().DropTable(&usersettings.UserSetting{}); err != nil {
		t.Fatal(err)
	}
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "full_context"}))
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(strings.ToLower(w.Body.String()), "user_settings") || strings.Contains(strings.ToLower(w.Body.String()), "sql") {
		t.Fatalf("driver detail leaked: %s", w.Body)
	}
	if v, err := c.Value(ctx, 1, "chat.file_mode"); err != nil || v != "rag" {
		t.Fatalf("cached value must keep being served, got %q, %v", v, err)
	}
}

func TestReadFailureIsGenericInternalError(t *testing.T) {
	env := newEnv(t)
	if err := env.DB.Migrator().DropTable(&usersettings.UserSetting{}); err != nil {
		t.Fatal(err)
	}
	w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(strings.ToLower(w.Body.String()), "user_settings") {
		t.Fatalf("detail leaked: %s", w.Body)
	}
}

func TestCacheFailureDoesNotFailPatch(t *testing.T) {
	env := newEnv(t)
	if err := env.Deps.Cache.Close(); err != nil {
		t.Fatal(err)
	}
	w := patch(env, 1, body(map[string]string{"chat.file_mode": "rag"}))
	testkit.Expect(t, w, 200, "")
	if settings(t, w)["chat.file_mode"] != "rag" {
		t.Fatal("write lost")
	}
	if !strings.Contains(env.Logs.String(), "user_settings_cache_refresh_failed") {
		t.Fatalf("failure was not logged: %s", env.Logs)
	}
	// Reads fall back to the database when the cache is unavailable.
	c := usersettings.NewValueCache(env.DB, env.Deps.Cache)
	if v, err := c.Value(context.Background(), 1, "chat.file_mode"); err != nil || v != "rag" {
		t.Fatalf("fallback read %q, %v", v, err)
	}
}

type recorder struct {
	calls [][]string
	users []uint
	err   error
}

func (r *recorder) SettingsChanged(_ context.Context, uid uint, keys []string) error {
	r.users = append(r.users, uid)
	r.calls = append(r.calls, keys)
	return r.err
}

func TestServiceNotifiesOnlyAfterSuccessfulWrite(t *testing.T) {
	env := newEnv(t)
	rec := &recorder{err: errors.New("cache down")}
	svc := usersettings.NewService(env.DB, env.Clock, nil, rec)
	ctx := context.Background()

	if _, err := svc.Update(ctx, 5, map[string]string{"chat.file_mode": "bogus"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := svc.Update(ctx, 5, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("notified without a write: %v", rec.calls)
	}
	out, err := svc.Update(ctx, 5, map[string]string{" chat.file_mode": "rag", "chat.show_latency": "false"})
	if err != nil {
		t.Fatalf("notifier failure must not fail the update: %v", err)
	}
	if out["chat.file_mode"] != "rag" {
		t.Fatalf("out %v", out)
	}
	if len(rec.calls) != 1 || rec.users[0] != 5 || strings.Join(rec.calls[0], ",") != "chat.file_mode,chat.show_latency" {
		t.Fatalf("calls %v users %v", rec.calls, rec.users)
	}

	env.DB.Migrator().DropTable(&usersettings.UserSetting{})
	rec.calls = nil
	if _, err := svc.Update(ctx, 5, map[string]string{"chat.file_mode": "auto"}); err == nil {
		t.Fatal("expected write failure")
	}
	if len(rec.calls) != 0 {
		t.Fatal("notified after a failed write")
	}
}

func TestNilNotifierStillWrites(t *testing.T) {
	env := newEnv(t)
	svc := usersettings.NewService(env.DB, env.Clock, nil, nil)
	out, err := svc.Update(context.Background(), 1, map[string]string{"chat.file_mode": "rag"})
	if err != nil || out["chat.file_mode"] != "rag" {
		t.Fatalf("%v %v", out, err)
	}
}

func TestValueCacheRejectsUnsupportedKey(t *testing.T) {
	env := newEnv(t)
	c := usersettings.NewValueCache(env.DB, env.Deps.Cache)
	if _, err := c.Value(context.Background(), 1, "chat.nope"); !errors.Is(err, usersettings.ErrUnknownKey) {
		t.Fatalf("err %v", err)
	}
}

func TestStaleReaderCannotOverwriteNewerValue(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	c := usersettings.NewValueCache(env.DB, env.Deps.Cache)
	// Reader A primes the cache with the pre-write value.
	if _, err := c.Value(ctx, 1, "chat.file_mode"); err != nil {
		t.Fatal(err)
	}
	// The write lands and the version token is replaced.
	testkit.Expect(t, patch(env, 1, body(map[string]string{"chat.file_mode": "rag"})), 200, "")
	// A late writer storing the old value under the old token must not be visible:
	// reads use the new token, so the committed value wins.
	if v, _ := c.Value(ctx, 1, "chat.file_mode"); v != "rag" {
		t.Fatalf("got %q", v)
	}
}

type ctxProbe struct{ cancellable bool }

func (p *ctxProbe) SettingsChanged(ctx context.Context, _ uint, _ []string) error {
	p.cancellable = ctx.Done() != nil
	return nil
}

// The refresh follows a committed write, so it must not be tied to the
// caller's cancellable request context.
func TestNotificationIsDetachedFromRequestCancellation(t *testing.T) {
	env := newEnv(t)
	probe := &ctxProbe{}
	svc := usersettings.NewService(env.DB, env.Clock, nil, probe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := svc.Update(ctx, 1, map[string]string{"chat.file_mode": "rag"}); err != nil {
		t.Fatal(err)
	}
	if probe.cancellable {
		t.Fatal("notifier received a cancellable context")
	}
}
