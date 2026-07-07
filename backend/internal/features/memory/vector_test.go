package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const testSig = "test-model@3"

func seedTwo(t *testing.T) (*testkit.Env, *Service, Entry, Entry) {
	t.Helper()
	env, svc := newEnv(t, Options{})
	ctx := t.Context()
	a, err := svc.Upsert(ctx, 1, "a", "alpha", ScopeProfile, "user")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Upsert(ctx, 1, "b", "beta", ScopeProfile, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StoreVector(ctx, 1, "a", "alpha", []float32{1, 0, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	if err := svc.StoreVector(ctx, 1, "b", "beta", []float32{0, 1, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	return env, svc, a, b
}

func keys(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.MemoryKey
	}
	return out
}

func TestSearchRanksByCosineAndHonoursSignature(t *testing.T) {
	_, svc, a, _ := seedTwo(t)
	ctx := t.Context()
	got, err := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 2, 0)
	if err != nil || len(got) != 2 || got[0].MemoryKey != "a" || got[1].MemoryKey != "b" {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].ID != a.ID || got[0].UserID != 1 || got[0].Value != "alpha" || got[0].Scope != ScopeProfile || got[0].UpdatedBy != "user" {
		t.Fatalf("hit fields: %+v", got[0])
	}
	if !got[0].CreatedAt.IsZero() || !got[0].UpdatedAt.IsZero() {
		t.Fatalf("timestamps must not be populated: %+v", got[0])
	}
	// A different signature sees nothing.
	if got, err = svc.Search(ctx, 1, []float32{1, 0, 0}, "other-model@3", 2, 0); err != nil || len(got) != 0 {
		t.Fatalf("other signature: %v %+v", err, got)
	}
	// topK limits the result.
	if got, _ = svc.Search(ctx, 1, []float32{1, 0.1, 0}, testSig, 1, 0); len(got) != 1 || got[0].MemoryKey != "a" {
		t.Fatalf("topK: %+v", got)
	}
	// Ordering follows similarity, not insertion.
	if got, _ = svc.Search(ctx, 1, []float32{0.1, 1, 0}, testSig, 2, 0); keys(got)[0] != "b" {
		t.Fatalf("order: %v", keys(got))
	}
}

func TestSearchScalesAreIrrelevantAndDimensionsMustMatch(t *testing.T) {
	_, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	got, _ := svc.Search(ctx, 1, []float32{5, 0, 0}, testSig, 2, 0.99)
	if len(got) != 1 || got[0].MemoryKey != "a" {
		t.Fatalf("cosine must ignore magnitude: %+v", got)
	}
	if got, err := svc.Search(ctx, 1, []float32{1, 0}, testSig, 2, 0); err != nil || len(got) != 0 {
		t.Fatalf("dimension mismatch: %v %+v", err, got)
	}
	if got, _ := svc.Search(ctx, 1, []float32{0, 0, 0}, testSig, 2, -1); len(got) != 0 {
		t.Fatalf("zero query: %+v", got)
	}
}

func TestSearchIsPerUser(t *testing.T) {
	_, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	if _, err := svc.Upsert(ctx, 2, "a", "mine", ScopeProfile, "user"); err != nil {
		t.Fatal(err)
	}
	if err := svc.StoreVector(ctx, 2, "a", "mine", []float32{1, 0, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Search(ctx, 2, []float32{1, 0, 0}, testSig, 5, 0)
	if len(got) != 1 || got[0].Value != "mine" || got[0].UserID != 2 {
		t.Fatalf("user 2: %+v", got)
	}
	if got, _ = svc.Search(ctx, 3, []float32{1, 0, 0}, testSig, 5, 0); len(got) != 0 {
		t.Fatalf("user 3: %+v", got)
	}
}

func TestUpdateClearsVectorUntilRewritten(t *testing.T) {
	env, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	if _, err := svc.Upsert(ctx, 1, "a", "alpha two", ScopeProfile, "user"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 5, 0.5); len(got) != 0 {
		t.Fatalf("stale vector returned: %+v", got)
	}
	var m Memory
	env.DB.Where("memory_key = 'a'").Take(&m)
	if m.EmbeddingSignature != "" {
		t.Fatalf("signature not cleared: %q", m.EmbeddingSignature)
	}
	// Rewriting with the old expected value is dropped; with the new one it lands.
	if err := svc.StoreVector(ctx, 1, "a", "alpha", []float32{1, 0, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 5, 0.5); len(got) != 0 {
		t.Fatalf("stale write landed: %+v", got)
	}
	if err := svc.StoreVector(ctx, 1, "a", "alpha two", []float32{1, 0, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 5, 0.5)
	if len(got) != 1 || got[0].Value != "alpha two" {
		t.Fatalf("after rewrite: %+v", got)
	}
	// Even an unchanged value clears the vector.
	if _, err := svc.Upsert(ctx, 1, "a", "alpha two", ScopeProfile, "user"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 5, 0.5); len(got) != 0 {
		t.Fatalf("unchanged update kept vector: %+v", got)
	}
}

func TestStoreVectorWithoutExpectedValueMatchesByKey(t *testing.T) {
	_, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	if err := svc.StoreVector(ctx, 1, "a", "", []float32{0, 0, 1}, testSig); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Search(ctx, 1, []float32{0, 0, 1}, testSig, 5, 0.9)
	if len(got) != 1 || got[0].MemoryKey != "a" {
		t.Fatalf("%+v", got)
	}
}

func TestStoreVectorNoOps(t *testing.T) {
	env, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	var before int64
	env.DB.Model(&vectorRow{}).Count(&before)
	for name, fn := range map[string]func() error{
		"empty vector":    func() error { return svc.StoreVector(ctx, 1, "a", "alpha", nil, testSig) },
		"blank signature": func() error { return svc.StoreVector(ctx, 1, "a", "alpha", []float32{1}, "  ") },
		"missing entry":   func() error { return svc.StoreVector(ctx, 1, "nope", "x", []float32{1}, testSig) },
		"other user":      func() error { return svc.StoreVector(ctx, 9, "a", "alpha", []float32{1}, testSig) },
		"stale value":     func() error { return svc.StoreVector(ctx, 1, "a", "old", []float32{1}, testSig) },
	} {
		if err := fn(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	var after int64
	env.DB.Model(&vectorRow{}).Count(&after)
	if before != after {
		t.Fatalf("vector rows changed: %d -> %d", before, after)
	}
	// The original vector is intact.
	if got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 1, 0.99); len(got) != 1 {
		t.Fatalf("original vector lost: %+v", got)
	}
}

func TestDeleteRemovesVector(t *testing.T) {
	env, svc, a, _ := seedTwo(t)
	ctx := t.Context()
	if err := svc.Delete(ctx, 1, "a"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 5, -1)
	if len(got) != 1 || got[0].MemoryKey != "b" {
		t.Fatalf("%+v", got)
	}
	var n int64
	env.DB.Model(&vectorRow{}).Where("memory_id = ?", a.ID).Count(&n)
	if n != 0 {
		t.Fatalf("vector row left behind: %d", n)
	}
}

func TestSearchDegenerateInputsReturnEmptyList(t *testing.T) {
	_, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	cases := map[string]func() ([]Entry, error){
		"empty query":     func() ([]Entry, error) { return svc.Search(ctx, 1, nil, testSig, 2, 0) },
		"blank signature": func() ([]Entry, error) { return svc.Search(ctx, 1, []float32{1, 0, 0}, " ", 2, 0) },
		"topK zero":       func() ([]Entry, error) { return svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 0, 0) },
		"topK negative":   func() ([]Entry, error) { return svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, -3, 0) },
		"min above all":   func() ([]Entry, error) { return svc.Search(ctx, 1, []float32{1, 0, 0}, testSig, 2, 1.01) },
	}
	for name, fn := range cases {
		got, err := fn()
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("%s: %v %#v", name, err, got)
		}
	}
}

func TestSearchWithNoEmbeddedEntries(t *testing.T) {
	_, svc := newEnv(t, Options{})
	if _, err := svc.Upsert(t.Context(), 1, "a", "v", ScopeProfile, "user"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Search(t.Context(), 1, []float32{1, 0, 0}, testSig, 3, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestStoreVectorRejectsNonFiniteSilently(t *testing.T) {
	_, svc, _, _ := seedTwo(t)
	nan := float32(0)
	nan = nan / nan
	if err := svc.StoreVector(t.Context(), 1, "a", "alpha", []float32{nan, 1, 0}, testSig); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Search(t.Context(), 1, []float32{1, 0, 0}, testSig, 1, 0.99); len(got) != 1 {
		t.Fatalf("existing vector should be untouched: %+v", got)
	}
}

func TestPurgeUserRemovesEverythingIncludingSoftDeleted(t *testing.T) {
	env, svc, _, _ := seedTwo(t)
	ctx := t.Context()
	if err := svc.Delete(ctx, 1, "a"); err != nil { // leaves a soft-deleted row
		t.Fatal(err)
	}
	if _, err := svc.Upsert(ctx, 2, "keep", "me", ScopeCustom, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListCached(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeUser(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var mem, vec int64
	env.DB.Unscoped().Model(&Memory{}).Where("user_id = 1").Count(&mem)
	env.DB.Model(&vectorRow{}).Where("user_id = 1").Count(&vec)
	if mem != 0 || vec != 0 {
		t.Fatalf("rows left: memories=%d vectors=%d", mem, vec)
	}
	env.DB.Unscoped().Model(&Memory{}).Where("user_id = 2").Count(&mem)
	if mem != 1 {
		t.Fatalf("other user's data touched: %d", mem)
	}
	if got, _ := svc.ListCached(ctx, 1); len(got) != 0 {
		t.Fatalf("cache survived the purge: %+v", got)
	}
}

func TestServiceUpsertLabelsAndLimits(t *testing.T) {
	env, svc := newEnv(t, Options{})
	ctx := t.Context()
	e, err := svc.Upsert(ctx, 1, " k ", " v ", " weird-scope ", " auto-writer ")
	if err != nil {
		t.Fatal(err)
	}
	if e.MemoryKey != "k" || e.Value != "v" || e.Scope != "weird-scope" || e.UpdatedBy != "auto-writer" || e.ID == 0 {
		t.Fatalf("%+v", e)
	}
	// Out-of-range scopes show up in the list but never reach chat.
	list, _ := svc.List(ctx, 1)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	pc, err := svc.BuildPromptContext(ctx, 1, "hello", nil)
	if err != nil || pc.Mems != "" || pc.Prefs != "" || len(pc.Injected) != 0 {
		t.Fatalf("%v %+v", err, pc)
	}
	// Overlong labels are cut to the column size rather than failing.
	long := "0123456789012345678901234567890123456789"
	e, err = svc.Upsert(ctx, 1, "k2", "v", long, long)
	if err != nil || len(e.UpdatedBy) != 32 || len(e.Scope) != 32 {
		t.Fatalf("%v %+v", err, e)
	}
	// An empty or over-long key is refused and stores nothing.
	for _, k := range []string{"", "   ", repeat("x", 129)} {
		if _, err := svc.Upsert(ctx, 1, k, "v", ScopeCustom, "user"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("key %q: %v", k, err)
		}
	}
	var n int64
	env.DB.Model(&Memory{}).Count(&n)
	if n != 2 {
		t.Fatalf("rows: %d", n)
	}
	if err := svc.Delete(ctx, 1, "  "); !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("blank delete: %v", err)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestOnChangeHook(t *testing.T) {
	var seen []uint
	_, svc := newEnv(t, Options{OnChange: func(id uint) { seen = append(seen, id) }})
	svc.Upsert(t.Context(), 4, "a", "v", ScopeCustom, "user")
	svc.Delete(t.Context(), 4, "a")
	if len(seen) != 2 || seen[0] != 4 || seen[1] != 4 {
		t.Fatalf("seen: %v", seen)
	}
}

// ---- background embedding through the HTTP path ----

func TestPutGeneratesVectorInBackground(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig}
	env, svc := newEnv(t, Options{Embedder: emb})
	if c := put(env, 1, "lang", "Chinese", "profile"); c != 200 {
		t.Fatal(c)
	}
	eventually(t, "vector to be stored", func() bool {
		got, _ := svc.Search(context.Background(), 1, []float32{1, 0, 0}, testSig, 1, 0.9)
		return len(got) == 1
	})
	var m Memory
	env.DB.Where("memory_key = 'lang'").Take(&m)
	if m.EmbeddingSignature != testSig {
		t.Fatalf("signature: %q", m.EmbeddingSignature)
	}
	if emb.calls[0] != "Chinese" {
		t.Fatalf("embedded text: %q", emb.calls[0])
	}
}

func TestEmbeddingFailureLeavesTextEntry(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, err: errors.New("provider down")}
	env, svc := newEnv(t, Options{Embedder: emb})
	if c := put(env, 1, "lang", "Chinese", "profile"); c != 200 {
		t.Fatal(c)
	}
	eventually(t, "embedder call", func() bool { return emb.callCount() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(list(t, env, 1)); n != 1 {
		t.Fatalf("entries: %d", n)
	}
	if got, _ := svc.Search(t.Context(), 1, []float32{1, 0, 0}, testSig, 1, 0); len(got) != 0 {
		t.Fatalf("unexpected hit: %+v", got)
	}
	// An empty result is treated the same way.
	emb.mu.Lock()
	emb.err, emb.vec = nil, nil
	emb.mu.Unlock()
	put(env, 1, "lang2", "x", "profile")
	eventually(t, "second embedder call", func() bool { return emb.callCount() == 2 })
}

func TestNoEmbeddingWhenDisabledOrAbsent(t *testing.T) {
	emb := &fakeEmbedder{enabled: false, vec: []float32{1}, sig: testSig}
	env, _ := newEnv(t, Options{Embedder: emb})
	put(env, 1, "lang", "x", "profile")
	time.Sleep(50 * time.Millisecond)
	if emb.callCount() != 0 {
		t.Fatal("disabled embedder was called")
	}
	env2, _ := newEnv(t, Options{})
	if c := put(env2, 1, "lang", "x", "profile"); c != 200 {
		t.Fatal(c)
	}
}

func TestEditDuringEmbeddingDropsStaleVector(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig, delay: 150 * time.Millisecond}
	env, svc := newEnv(t, Options{Embedder: emb})
	put(env, 1, "lang", "first", "profile")
	put(env, 1, "lang", "second", "profile") // edit while the first embed is still running
	eventually(t, "both embeds", func() bool { return emb.callCount() == 2 })
	time.Sleep(400 * time.Millisecond)
	got, _ := svc.Search(t.Context(), 1, []float32{1, 0, 0}, testSig, 5, 0.9)
	if len(got) != 1 || got[0].Value != "second" {
		t.Fatalf("hits: %+v", got)
	}
}

func TestDeleteDuringEmbeddingIsNoOp(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig, delay: 150 * time.Millisecond}
	env, _ := newEnv(t, Options{Embedder: emb})
	put(env, 1, "lang", "first", "profile")
	env.Do("DELETE", "/api/v1/memories/profile/lang", env.Bearer(testkit.User(1)), nil)
	time.Sleep(400 * time.Millisecond)
	var n int64
	env.DB.Model(&vectorRow{}).Count(&n)
	if n != 0 {
		t.Fatalf("vector rows: %d", n)
	}
}
