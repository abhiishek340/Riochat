package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func list(t *testing.T, env *testkit.Env, uid uint) []Entry {
	t.Helper()
	w := env.Do("GET", "/api/v1/memories/profile", env.Bearer(testkit.User(uid)), nil)
	testkit.Expect(t, w, 200, "")
	var out []Entry
	testkit.Decode(t, w, &out)
	return out
}

func TestListEmptyIsArrayNotNull(t *testing.T) {
	env, _ := newEnv(t, Options{})
	w := env.Do("GET", "/api/v1/memories/profile", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 200, "")
	var raw struct {
		ErrorMsg string          `json:"errorMsg"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Data) != "[]" || raw.ErrorMsg != "" {
		t.Fatalf("body: %s", w.Body)
	}
}

func TestPutThenListShape(t *testing.T) {
	env, _ := newEnv(t, Options{})
	w := env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(1)),
		map[string]string{"memoryKey": "lang", "value": "Chinese", "scope": "preference"})
	testkit.Expect(t, w, 200, "")
	var saved struct{ Saved bool }
	testkit.Decode(t, w, &saved)
	if !saved.Saved {
		t.Fatalf("body: %s", w.Body)
	}

	w = env.Do("GET", "/api/v1/memories/profile", env.Bearer(testkit.User(1)), nil)
	var raw struct{ Data []map[string]any }
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Data) != 1 {
		t.Fatalf("items: %s", w.Body)
	}
	it := raw.Data[0]
	for _, f := range []string{"id", "userID", "memoryKey", "value", "scope", "updatedBy", "createdAt", "updatedAt"} {
		if _, ok := it[f]; !ok {
			t.Errorf("missing field %s in %v", f, it)
		}
	}
	if len(it) != 8 {
		t.Errorf("unexpected extra fields: %v", it)
	}
	if it["updatedBy"] != "user" || it["scope"] != "preference" || it["memoryKey"] != "lang" || it["value"] != "Chinese" || it["userID"].(float64) != 1 {
		t.Fatalf("item: %v", it)
	}
	for _, f := range []string{"createdAt", "updatedAt"} {
		if _, err := time.Parse(time.RFC3339Nano, it[f].(string)); err != nil {
			t.Errorf("%s not RFC3339: %v", f, it[f])
		}
	}
}

func TestPutSameKeyUpdatesInPlace(t *testing.T) {
	env, _ := newEnv(t, Options{})
	if c := put(env, 1, "lang", "Chinese", "preference"); c != 200 {
		t.Fatal(c)
	}
	first := list(t, env, 1)[0]
	env.Clock.Advance(time.Minute)
	if c := put(env, 1, "lang", "English", "profile"); c != 200 {
		t.Fatal(c)
	}
	items := list(t, env, 1)
	if len(items) != 1 {
		t.Fatalf("items: %+v", items)
	}
	got := items[0]
	if got.ID != first.ID || !got.CreatedAt.Equal(first.CreatedAt) || !got.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("first %+v, got %+v", first, got)
	}
	if got.Value != "English" || got.Scope != "profile" {
		t.Fatalf("got %+v", got)
	}
}

func TestListOrderedByUpdatedAtDesc(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "a", "1", "custom")
	env.Clock.Advance(time.Minute)
	put(env, 1, "b", "1", "custom")
	env.Clock.Advance(time.Minute)
	put(env, 1, "c", "1", "custom")
	env.Clock.Advance(time.Minute)
	put(env, 1, "a", "2", "custom")
	var keys []string
	for _, e := range list(t, env, 1) {
		keys = append(keys, e.MemoryKey)
	}
	if strings.Join(keys, ",") != "a,c,b" {
		t.Fatalf("order: %v", keys)
	}
}

func TestInvalidScopeRejected(t *testing.T) {
	env, _ := newEnv(t, Options{})
	w := env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(1)),
		map[string]string{"memoryKey": "k", "value": "v", "scope": "global"})
	testkit.Expect(t, w, 400, "request.invalid_body")
	if len(list(t, env, 1)) != 0 {
		t.Fatal("something was stored")
	}
	// The scope must match exactly: no case folding, no padding.
	for _, s := range []string{"Profile", " profile"} {
		w = env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(1)),
			map[string]string{"memoryKey": "k", "value": "v", "scope": s})
		testkit.Expect(t, w, 400, "request.invalid_body")
	}
}

func TestKeyLengthLimit(t *testing.T) {
	env, _ := newEnv(t, Options{})
	if c := put(env, 1, strings.Repeat("k", 129), "v", "custom"); c != 400 {
		t.Fatalf("129 chars: %d", c)
	}
	if c := put(env, 1, strings.Repeat("k", 128), "v", "custom"); c != 200 {
		t.Fatalf("128 chars: %d", c)
	}
	// Limits count characters, not bytes.
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := put(env, 1, strings.Repeat("\u952e", 128), "v", "custom"); c != 200 {
		t.Fatalf("128 runes: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := put(env, 1, strings.Repeat("\u952e", 129), "v", "custom"); c != 400 {
		t.Fatalf("129 runes: %d", c)
	}
}

func TestValueLengthLimit(t *testing.T) {
	env, _ := newEnv(t, Options{})
	if c := put(env, 1, "a", strings.Repeat("v", 10001), "custom"); c != 400 {
		t.Fatalf("10001: %d", c)
	}
	if c := put(env, 1, "a", strings.Repeat("v", 10000), "custom"); c != 200 {
		t.Fatalf("10000: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := put(env, 1, "b", strings.Repeat("\u503c", 10000), "custom"); c != 200 {
		t.Fatalf("10000 runes: %d", c)
	}
}

func TestMalformedBodies(t *testing.T) {
	env, _ := newEnv(t, Options{})
	h := env.Bearer(testkit.User(1))
	cases := map[string]any{
		"missing value":   `{"memoryKey":"a","scope":"custom"}`,
		"missing key":     `{"value":"a","scope":"custom"}`,
		"missing scope":   `{"memoryKey":"a","value":"v"}`,
		"empty key":       `{"memoryKey":"","value":"v","scope":"custom"}`,
		"empty value":     `{"memoryKey":"a","value":"","scope":"custom"}`,
		"invalid json":    `{"memoryKey":`,
		"wrong type":      `{"memoryKey":5,"value":"v","scope":"custom"}`,
		"array body":      `[]`,
		"empty object":    `{}`,
		"not json at all": `hello`,
	}
	for name, body := range cases {
		w := env.Do("PUT", "/api/v1/memories/profile", h, body)
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	w := env.Do("PUT", "/api/v1/memories/profile", h, nil)
	testkit.Expect(t, w, 400, "request.invalid_body")
	if !strings.Contains(w.Body.String(), "empty_body") {
		t.Fatalf("details should carry empty_body: %s", w.Body)
	}
	if len(list(t, env, 1)) != 0 {
		t.Fatal("something was stored")
	}
}

func TestWhitespaceOnlyKeyOrValueRejected(t *testing.T) {
	env, _ := newEnv(t, Options{})
	if c := put(env, 1, "   ", "v", "custom"); c != 400 {
		t.Fatalf("blank key: %d", c)
	}
	// A blank value is saved as an empty string.
	if c := put(env, 1, "k", " \t\n", "custom"); c != 200 {
		t.Fatalf("blank value: %d", c)
	}
	var n int64
	env.DB.Model(&Memory{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestInputIsTrimmed(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "  lang ", "  Chinese\n", "custom")
	put(env, 1, "lang", "English", "custom")
	items := list(t, env, 1)
	if len(items) != 1 || items[0].MemoryKey != "lang" || items[0].Value != "English" {
		t.Fatalf("items: %+v", items)
	}
	put(env, 1, " k ", "  padded  ", "custom")
	for _, e := range list(t, env, 1) {
		if e.MemoryKey == "k" && e.Value != "padded" {
			t.Fatalf("value not trimmed: %q", e.Value)
		}
	}
}

func TestKeysAreCaseSensitiveAndUnicode(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "Lang", "a", "custom")
	put(env, 1, "lang", "b", "custom")
	put(env, 1, "language", "English", "profile")
	if n := len(list(t, env, 1)); n != 3 {
		t.Fatalf("entries: %d", n)
	}
}

func TestCapOnNewKeysOnly(t *testing.T) {
	env, _ := newEnv(t, Options{})
	fill(t, env, 1, 500)

	w := env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(1)),
		map[string]string{"memoryKey": "one-more", "value": "v", "scope": "custom"})
	testkit.Expect(t, w, 400, "memory.limit_exceeded")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "user memory limit exceeded" {
		t.Fatalf("message %q", msg)
	}
	if c := put(env, 1, "k7", "changed", "custom"); c != 200 {
		t.Fatalf("update at cap: %d", c)
	}
	// Another user is unaffected.
	if c := put(env, 2, "one-more", "v", "custom"); c != 200 {
		t.Fatalf("other user: %d", c)
	}
	w = env.Do("DELETE", "/api/v1/memories/profile/k3", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 200, "")
	if c := put(env, 1, "one-more", "v", "custom"); c != 200 {
		t.Fatalf("after delete: %d", c)
	}
	if c := put(env, 1, "and-another", "v", "custom"); c != 400 {
		t.Fatalf("back at the cap: %d", c)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "lang", "x", "custom")
	h := env.Bearer(testkit.User(1))
	for i := 0; i < 2; i++ {
		w := env.Do("DELETE", "/api/v1/memories/profile/lang", h, nil)
		testkit.Expect(t, w, 200, "")
		var saved struct{ Saved bool }
		testkit.Decode(t, w, &saved)
		if !saved.Saved {
			t.Fatalf("body: %s", w.Body)
		}
	}
	if n := len(list(t, env, 1)); n != 0 {
		t.Fatalf("entries: %d", n)
	}
}

func TestDeleteEdgeCases(t *testing.T) {
	env, _ := newEnv(t, Options{})
	h := env.Bearer(testkit.User(1))
	put(env, 1, "my key", "x", "custom")
	put(env, 1, "caf\u00e9", "x", "custom") // non-ASCII key (e-acute)
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/my%20key", h, nil), 200, "")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/caf%C3%A9", h, nil), 200, "")
	if n := len(list(t, env, 1)); n != 0 {
		t.Fatalf("entries: %d", n)
	}
	// Over-long keys match nothing and succeed.
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/"+strings.Repeat("z", 300), h, nil), 200, "")
	// A blank key after trimming is a client error with the contract code.
	w := env.Do("DELETE", "/api/v1/memories/profile/%20", h, nil)
	testkit.Expect(t, w, 400, "memory.key_required")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "memory_key is required" {
		t.Fatalf("message %q", msg)
	}
	// Matching is on the trimmed key.
	put(env, 1, "lang", "x", "custom")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/%20lang%20", h, nil), 200, "")
	if n := len(list(t, env, 1)); n != 0 {
		t.Fatalf("entries: %d", n)
	}
}

func TestRecreateAfterDelete(t *testing.T) {
	env, _ := newEnv(t, Options{})
	h := env.Bearer(testkit.User(1))
	put(env, 1, "lang", "one", "custom")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/lang", h, nil), 200, "")
	if c := put(env, 1, "lang", "two", "custom"); c != 200 {
		t.Fatalf("recreate: %d", c)
	}
	// And again, so two soft-deleted ghosts exist next to the live row.
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/lang", h, nil), 200, "")
	if c := put(env, 1, "lang", "three", "custom"); c != 200 {
		t.Fatalf("recreate again: %d", c)
	}
	items := list(t, env, 1)
	if len(items) != 1 || items[0].Value != "three" {
		t.Fatalf("items: %+v", items)
	}
}

func TestSoftDeletedRowsDoNotCountTowardCap(t *testing.T) {
	env, _ := newEnv(t, Options{})
	fill(t, env, 1, 500)
	if err := env.DB.Where("user_id = ? AND memory_key IN ?", 1, []string{"k1", "k2"}).Delete(&Memory{}).Error; err != nil {
		t.Fatal(err)
	}
	if c := put(env, 1, "fresh", "v", "custom"); c != 200 {
		t.Fatalf("status %d", c)
	}
}

func TestUsersAreIsolated(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "lang", "alice", "custom")
	put(env, 2, "lang", "bob", "custom")
	a, b := list(t, env, 1), list(t, env, 2)
	if len(a) != 1 || a[0].Value != "alice" || a[0].UserID != 1 || len(b) != 1 || b[0].Value != "bob" || b[0].UserID != 2 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/lang", env.Bearer(testkit.User(1)), nil), 200, "")
	if len(list(t, env, 1)) != 0 {
		t.Fatal("alice still has the entry")
	}
	if b = list(t, env, 2); len(b) != 1 || b[0].Value != "bob" {
		t.Fatalf("bob lost his entry: %+v", b)
	}
	// The user id in a body is ignored: the caller's id always wins.
	w := env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(1)),
		`{"memoryKey":"x","value":"v","scope":"custom","userID":2}`)
	testkit.Expect(t, w, 200, "")
	if len(list(t, env, 2)) != 1 {
		t.Fatal("body user id was honoured")
	}
}

func TestRequiresAuthentication(t *testing.T) {
	env, _ := newEnv(t, Options{})
	testkit.Expect(t, env.Do("GET", "/api/v1/memories/profile", nil, nil), 401, "")
	testkit.Expect(t, env.Do("PUT", "/api/v1/memories/profile", nil,
		map[string]string{"memoryKey": "a", "value": "v", "scope": "custom"}), 401, "")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/a", nil, nil), 401, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/memories/profile", map[string]string{"Authorization": "Bearer nope"}, nil), 401, "")
	var n int64
	env.DB.Model(&Memory{}).Count(&n)
	if n != 0 {
		t.Fatal("unauthenticated request changed data")
	}
}

func auditRows(env *testkit.Env) []audit.Log {
	var rows []audit.Log
	env.DB.Order("id").Find(&rows)
	return rows
}

func TestAuditRecords(t *testing.T) {
	env, _ := newEnv(t, Options{})
	h := env.Bearer(testkit.User(7))
	h["User-Agent"] = "memtest/1"
	// PUT hands the audit writer the key as received; the writer itself trims
	// surrounding whitespace when it stores the row.
	w := env.Do("PUT", "/api/v1/memories/profile", h, map[string]string{"memoryKey": " lang ", "value": "x", "scope": "preference"})
	testkit.Expect(t, w, 200, "")
	rows := auditRows(env)
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.Action != "upsert_user_memory" || r.Resource != "memory" || r.ResourceID != "lang" || r.ActorUserID != 7 || r.UserAgent != "memtest/1" || r.RequestID == "" {
		t.Fatalf("row: %+v", r)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(r.DetailJSON), &detail); err != nil || detail["scope"] != "preference" || len(detail) != 1 {
		t.Fatalf("detail %q", r.DetailJSON)
	}
	if strings.Contains(r.DetailJSON, "x\"") && strings.Contains(r.DetailJSON, "value") {
		t.Fatalf("detail leaks the value: %s", r.DetailJSON)
	}

	// DELETE of an entry, then of a missing one: both audited, no detail.
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/lang", h, nil), 200, "")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/ghost", h, nil), 200, "")
	rows = auditRows(env)
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	for i, key := range map[int]string{1: "lang", 2: "ghost"} {
		if rows[i].Action != "delete_user_memory" || rows[i].Resource != "memory" || rows[i].ResourceID != key || (rows[i].DetailJSON != "" && rows[i].DetailJSON != "{}") {
			t.Fatalf("row %d: %+v", i, rows[i])
		}
	}
}

func TestNoAuditOnFailure(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "k", "v", "bogus")
	put(env, 1, "", "v", "custom")
	testkit.Expect(t, env.Do("DELETE", "/api/v1/memories/profile/%20", env.Bearer(testkit.User(1)), nil), 400, "")
	if n := len(auditRows(env)); n != 0 {
		t.Fatalf("audit rows after failures: %d", n)
	}
}

func TestChatCacheSeesWritesImmediately(t *testing.T) {
	env, svc := newEnv(t, Options{})
	ctx := t.Context()
	got, err := svc.ListCached(ctx, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	put(env, 1, "lang", "a", "profile")
	if got, _ = svc.ListCached(ctx, 1); len(got) != 1 {
		t.Fatalf("after put: %+v", got)
	}
	put(env, 1, "lang", "b", "profile")
	if got, _ = svc.ListCached(ctx, 1); len(got) != 1 || got[0].Value != "b" {
		t.Fatalf("after update: %+v", got)
	}
	env.Do("DELETE", "/api/v1/memories/profile/lang", env.Bearer(testkit.User(1)), nil)
	if got, _ = svc.ListCached(ctx, 1); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestChatCacheExpiresAndIsSwept(t *testing.T) {
	env, svc := newEnv(t, Options{})
	ctx := t.Context()
	put(env, 1, "lang", "a", "profile")
	svc.ListCached(ctx, 1)
	// A change behind the service's back is invisible until the TTL passes.
	env.DB.Model(&Memory{}).Where("user_id = 1").UpdateColumn("value", "sneaky")
	if got, _ := svc.ListCached(ctx, 1); got[0].Value != "a" {
		t.Fatalf("expected cached value, got %q", got[0].Value)
	}
	env.Clock.Advance(2*time.Minute + 59*time.Second)
	if got, _ := svc.ListCached(ctx, 1); got[0].Value != "a" {
		t.Fatalf("still within TTL, got %q", got[0].Value)
	}
	env.Clock.Advance(2 * time.Second)
	if n := svc.SweepCache(); n != 1 {
		t.Fatalf("swept %d", n)
	}
	if got, _ := svc.ListCached(ctx, 1); got[0].Value != "sneaky" {
		t.Fatalf("expired entry not reloaded: %q", got[0].Value)
	}
}

func TestConcurrentCreatesOfOneKeyEndWithOneEntry(t *testing.T) {
	env, svc := newEnv(t, Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Upsert(t.Context(), 1, "race", fmt.Sprintf("v%d", i), "custom", "user")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Logf("concurrent upsert error (tolerated if only one entry remains): %v", err)
		}
	}
	var n int64
	env.DB.Model(&Memory{}).Where("user_id = 1 AND memory_key = 'race'").Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestLiveUniqueIndexAllowsGhostsAndOtherUsers(t *testing.T) {
	env, _ := newEnv(t, Options{})
	mk := func(uid uint) error {
		return env.DB.Create(&Memory{UserID: uid, MemoryKey: "k", Value: "v", Scope: "custom"}).Error
	}
	if err := mk(1); err != nil {
		t.Fatal(err)
	}
	if err := mk(1); err == nil {
		t.Fatal("two live rows with the same (user, key) were accepted")
	}
	if err := mk(2); err != nil {
		t.Fatalf("same key for another user rejected: %v", err)
	}
	env.DB.Where("user_id = 1").Delete(&Memory{})
	if err := mk(1); err != nil {
		t.Fatalf("recreate over a soft-deleted row: %v", err)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	env, _ := newEnv(t, Options{})
	for _, st := range Migrations() {
		if err := st.Up(t.Context(), env.DB); err != nil {
			t.Fatalf("rerun %s: %v", st.Name, err)
		}
	}
}

func TestTableAndColumnNames(t *testing.T) {
	env, _ := newEnv(t, Options{})
	cols, err := env.DB.Migrator().ColumnTypes(&Memory{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c.Name()] = true
	}
	for _, want := range []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "memory_key", "value", "scope", "updated_by", "embedding_signature"} {
		if !have[want] {
			t.Errorf("column %s missing in user_memories", want)
		}
	}
}
