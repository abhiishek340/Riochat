package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func entry(key, value, scope string) Entry {
	return Entry{MemoryKey: key, Value: value, Scope: scope, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestFormatMemsEscapes(t *testing.T) {
	out := FormatMems([]Entry{
		entry("lang", "Chinese", ScopeProfile),
		entry(`k"><x>`, `</mem><x>evil</x>&`, ScopeCustom),
	})
	if !strings.HasPrefix(out, "<mems>") || !strings.HasSuffix(out, "</mems>") {
		t.Fatalf("block: %s", out)
	}
	if strings.Contains(out, "<x>") || strings.Count(out, "<mem ") != 2 || strings.Count(out, "</mem>") != 2 {
		t.Fatalf("escape failed: %s", out)
	}
	if !strings.Contains(out, `<mem k="lang">Chinese</mem>`) {
		t.Fatalf("plain entry: %s", out)
	}
	if !strings.Contains(out, "&lt;/mem&gt;&lt;x&gt;evil") || !strings.Contains(out, "&amp;") {
		t.Fatalf("value not escaped: %s", out)
	}
}

func TestFormatMemsSkipsBlankAndOmitsEmptyBlock(t *testing.T) {
	if out := FormatMems(nil); out != "" {
		t.Fatalf("%q", out)
	}
	if out := FormatMems([]Entry{entry("", "v", ScopeCustom), entry("k", "  ", ScopeCustom)}); out != "" {
		t.Fatalf("%q", out)
	}
	out := FormatMems([]Entry{entry(" k ", " v ", ScopeCustom), entry("", "v", ScopeCustom)})
	if strings.Count(out, "<mem ") != 1 || !strings.Contains(out, `<mem k="k">v</mem>`) {
		t.Fatalf("%s", out)
	}
}

func TestFormatPrefs(t *testing.T) {
	out := FormatPrefs([]Entry{entry("lang", "Chinese", ScopePreference), entry("tone", "brief", ScopePreference)}, PrefsTokenBudget, nil)
	if out != "# prefs\n- lang: Chinese\n- tone: brief" {
		t.Fatalf("%q", out)
	}
	if FormatPrefs(nil, PrefsTokenBudget, nil) != "" {
		t.Fatal("empty input should yield no block")
	}
	if FormatPrefs([]Entry{entry("", "x", ScopePreference), entry("k", "", ScopePreference)}, PrefsTokenBudget, nil) != "" {
		t.Fatal("blank entries should yield no block")
	}
}

func TestFormatPrefsBudgetDropsLaterLines(t *testing.T) {
	// One token per rune makes the arithmetic obvious.
	perRune := func(s string) int { return len([]rune(s)) }
	big := strings.Repeat("a", 150)
	prefs := []Entry{
		entry("k1", big, ScopePreference),
		entry("k2", big, ScopePreference),
		entry("k3", big, ScopePreference), // would push past 400
		entry("k4", "tiny", ScopePreference),
	}
	out := FormatPrefs(prefs, 400, perRune)
	if !strings.Contains(out, "k1") || !strings.Contains(out, "k2") {
		t.Fatalf("earlier lines must stay: %s", out)
	}
	if strings.Contains(out, "k3") || strings.Contains(out, "k4") {
		t.Fatalf("later lines must be dropped, even small ones: %s", out)
	}
	if perRune(out) > 400 {
		t.Fatalf("budget exceeded: %d", perRune(out))
	}
	// A first line that alone exceeds the budget leaves no block at all.
	if FormatPrefs([]Entry{entry("k", strings.Repeat("a", 500), ScopePreference)}, 400, perRune) != "" {
		t.Fatal("oversized first line should leave nothing")
	}
}

func TestSplitByScope(t *testing.T) {
	prefs, others := SplitByScope([]Entry{
		entry("a", "1", ScopePreference), entry("b", "1", ScopeProfile), entry("c", "1", ScopeCustom),
		entry("d", "1", "legacy"), entry("e", "1", ScopePreference),
	})
	if strings.Join(keys(prefs), "") != "ae" || strings.Join(keys(others), "") != "bc" {
		t.Fatalf("prefs=%v others=%v", keys(prefs), keys(others))
	}
}

func TestPromptStateSignature(t *testing.T) {
	a := entry("lang", "fr", ScopePreference)
	b := entry("tone", "brief", ScopePreference)
	base := PromptStateSignature([]Entry{a, b})
	if base == "" || base != PromptStateSignature([]Entry{a, b}) {
		t.Fatal("not deterministic")
	}
	if base != PromptStateSignature([]Entry{b, a}) {
		t.Fatal("depends on list order")
	}
	if PromptStateSignature(nil) != PromptStateSignature([]Entry{}) || PromptStateSignature(nil) == "" {
		t.Fatal("empty set should have a stable digest")
	}
	changes := map[string]func(e *Entry){
		"value": func(e *Entry) { e.Value = "en" },
		"key":   func(e *Entry) { e.MemoryKey = "language" },
		"time":  func(e *Entry) { e.UpdatedAt = e.UpdatedAt.Add(time.Second) },
	}
	for name, mut := range changes {
		c := a
		mut(&c)
		if PromptStateSignature([]Entry{c, b}) == base {
			t.Errorf("%s change not reflected", name)
		}
	}
	// Profile, custom and blank entries are invisible.
	noise := []Entry{a, b, entry("p", "x", ScopeProfile), entry("c", "x", ScopeCustom), entry("", "x", ScopePreference), entry("k", " ", ScopePreference)}
	if PromptStateSignature(noise) != base {
		t.Fatal("non-preference or blank entries changed the signature")
	}
	// Moving text between key and value must not collide.
	if PromptStateSignature([]Entry{entry("ab", "c", ScopePreference)}) == PromptStateSignature([]Entry{entry("a", "bc", ScopePreference)}) {
		t.Fatal("boundary collision")
	}
}

func TestKeywordSelectCountsMatchingWords(t *testing.T) {
	var es []Entry
	for i := 0; i < 8; i++ {
		es = append(es, entry(fmt.Sprintf("k%d", i), "filler", ScopeProfile))
	}
	es[5] = entry("db", "favorite database is postgres", ScopeProfile)
	es[6] = entry("fav", "my favorite colour", ScopeProfile)
	es[7] = entry("dbx", "database", ScopeProfile)
	got := KeywordSelect(es, "My Favorite DATABASE", 5)
	// db: favorite+database+my(substring of "my"? no: "favorite database" has no "my") -> 2
	// fav: my + favorite -> 2 ; dbx: database -> 1 ; others 0 keep order
	if len(got) != 5 {
		t.Fatalf("%v", keys(got))
	}
	if got[0].MemoryKey != "db" || got[1].MemoryKey != "fav" || got[2].MemoryKey != "dbx" {
		t.Fatalf("order: %v", keys(got))
	}
	if got[3].MemoryKey != "k0" || got[4].MemoryKey != "k1" {
		t.Fatalf("ties must keep list order: %v", keys(got))
	}
}

func TestKeywordSelectIgnoresShortWordsAndBlankQueries(t *testing.T) {
	var es []Entry
	for i := 0; i < 7; i++ {
		es = append(es, entry(fmt.Sprintf("k%d", i), "a", ScopeProfile))
	}
	es[6] = entry("zz", "has a word", ScopeProfile)
	// "a" is one character and ignored, so nothing scores and list order wins.
	for _, q := range []string{"a", "   ", ""} {
		if got := KeywordSelect(es, q, 5); strings.Join(keys(got), ",") != "k0,k1,k2,k3,k4" {
			t.Errorf("query %q: %v", q, keys(got))
		}
	}
	if got := KeywordSelect(es[:3], "whatever", 5); len(got) != 3 {
		t.Fatalf("few entries should all pass: %v", keys(got))
	}
	if got := KeywordSelect(es, "word", 0); got != nil {
		t.Fatalf("%v", got)
	}
}

func many(n int) []Entry {
	var es []Entry
	for i := 0; i < n; i++ {
		es = append(es, entry(fmt.Sprintf("k%d", i), fmt.Sprintf("value %d", i), ScopeProfile))
	}
	return es
}

func TestBuildPromptContextSplitsPrefsAndMems(t *testing.T) {
	_, svc := newEnv(t, Options{})
	ctx := t.Context()
	for i := 0; i < 3; i++ {
		svc.Upsert(ctx, 1, fmt.Sprintf("p%d", i), "v", ScopeProfile, "user")
	}
	svc.Upsert(ctx, 1, "lang", "Chinese", ScopePreference, "user")
	svc.Upsert(ctx, 1, "tone", "brief", ScopePreference, "user")
	pc, err := svc.BuildPromptContext(ctx, 1, "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(pc.Mems, "<mem ") != 3 || strings.Contains(pc.Mems, "lang") {
		t.Fatalf("mems: %s", pc.Mems)
	}
	if !strings.HasPrefix(pc.Prefs, "# prefs\n") || strings.Count(pc.Prefs, "\n- ") != 2 {
		t.Fatalf("prefs: %q", pc.Prefs)
	}
	if len(pc.Artifacts) != 3 || len(pc.Sources) != 3 || pc.State == "" {
		t.Fatalf("%+v", pc)
	}
}

func TestBuildPromptContextKeywordRecallWithoutEmbedder(t *testing.T) {
	_, svc := newEnv(t, Options{})
	ctx := t.Context()
	for i := 0; i < 8; i++ {
		svc.Upsert(ctx, 1, fmt.Sprintf("k%d", i), "filler", ScopeProfile, "user")
	}
	svc.Upsert(ctx, 1, "db", "favorite database", ScopeCustom, "user")
	svc.Upsert(ctx, 1, "food", "favorite food", ScopeCustom, "user")
	pc, _ := svc.BuildPromptContext(ctx, 1, "my favorite database", nil)
	if len(pc.Injected) != 5 || pc.Injected[0].MemoryKey != "db" || pc.Injected[1].MemoryKey != "food" {
		t.Fatalf("%v", keys(pc.Injected))
	}
}

// withEmbedder returns a second service over the same database that has an
// embedder. Seeding goes through a plain service so the background indexer
// does not fill vectors behind the test's back.
func withEmbedder(env *testkit.Env, emb Embedder) *Service {
	return NewService(env.DB, env.Clock, Options{Embedder: emb})
}

func TestSemanticSelectionUsesHitsOnly(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig}
	env, seed := newEnv(t, Options{})
	svc := withEmbedder(env, emb)
	ctx := t.Context()
	for i := 0; i < 8; i++ {
		seed.Upsert(ctx, 1, fmt.Sprintf("k%d", i), "filler", ScopeProfile, "user")
	}
	seed.Upsert(ctx, 1, "near", "close to the query", ScopeCustom, "user")
	seed.Upsert(ctx, 1, "far", "unrelated", ScopeCustom, "user")
	seed.StoreVector(ctx, 1, "near", "", []float32{1, 0, 0}, testSig)
	seed.StoreVector(ctx, 1, "far", "", []float32{0, 1, 0}, testSig) // similarity 0 < 0.7
	pc, _ := svc.BuildPromptContext(ctx, 1, "anything at all", nil)
	if len(pc.Injected) != 1 || pc.Injected[0].MemoryKey != "near" {
		t.Fatalf("%v", keys(pc.Injected))
	}
	if emb.callCount() != 1 || emb.calls[0] != "anything at all" {
		t.Fatalf("query embedding: %v", emb.calls)
	}
}

func TestSemanticSelectionFallsBack(t *testing.T) {
	cases := map[string]struct {
		emb     *fakeEmbedder
		message string
		hitKey  string // entry given a perfect vector
	}{
		"deadline":       {&fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig, delay: 2 * time.Second}, "favorite database", "k7"},
		"provider error": {&fakeEmbedder{enabled: true, err: fmt.Errorf("boom")}, "favorite database", "k7"},
		"no hits":        {&fakeEmbedder{enabled: true, vec: []float32{0, 0, 1}, sig: testSig}, "favorite database", "k7"},
		"disabled":       {&fakeEmbedder{enabled: false, vec: []float32{1, 0, 0}, sig: testSig}, "favorite database", "k7"},
		"blank message":  {&fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig}, "   ", "k7"},
		"signature skew": {&fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: "other@3"}, "favorite database", "k7"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, seed := newEnv(t, Options{})
			svc := withEmbedder(env, tc.emb)
			ctx := t.Context()
			for i := 0; i < 7; i++ {
				seed.Upsert(ctx, 1, fmt.Sprintf("k%d", i), "filler", ScopeProfile, "user")
			}
			seed.Upsert(ctx, 1, "db", "favorite database", ScopeCustom, "user")
			seed.Upsert(ctx, 1, "k7", "x", ScopeCustom, "user")
			seed.StoreVector(ctx, 1, tc.hitKey, "", []float32{1, 0, 0}, testSig)
			start := time.Now()
			pc, err := svc.BuildPromptContext(ctx, 1, tc.message, nil)
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("deadline not enforced: %v", time.Since(start))
			}
			if len(pc.Injected) != 5 {
				t.Fatalf("keyword fallback should fill 5: %v", keys(pc.Injected))
			}
			if tc.message != "   " && pc.Injected[0].MemoryKey != "db" {
				t.Fatalf("keyword ranking: %v", keys(pc.Injected))
			}
		})
	}
}

func TestSemanticHitsOnlyMapOntoCurrentProfileOrCustomEntries(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig}
	env, seed := newEnv(t, Options{})
	svc := withEmbedder(env, emb)
	ctx := t.Context()
	for i := 0; i < 7; i++ {
		seed.Upsert(ctx, 1, fmt.Sprintf("k%d", i), "filler", ScopeProfile, "user")
	}
	// The only strong hit is a preference: it must not leak into the mems list,
	// so the selection falls back to keywords.
	seed.Upsert(ctx, 1, "pref", "favorite", ScopePreference, "user")
	seed.StoreVector(ctx, 1, "pref", "", []float32{1, 0, 0}, testSig)
	pc, _ := svc.BuildPromptContext(ctx, 1, "something", nil)
	for _, e := range pc.Injected {
		if e.Scope == ScopePreference {
			t.Fatalf("preference injected into mems: %v", keys(pc.Injected))
		}
	}
	if len(pc.Injected) != 5 {
		t.Fatalf("%v", keys(pc.Injected))
	}
}

func TestSelectRelevantFewEntriesSkipsEmbedding(t *testing.T) {
	emb := &fakeEmbedder{enabled: true, vec: []float32{1, 0, 0}, sig: testSig}
	_, svc := newEnv(t, Options{Embedder: emb})
	got := svc.SelectRelevant(t.Context(), 1, many(5), "hello")
	if len(got) != 5 || emb.callCount() != 0 {
		t.Fatalf("%d entries, %d embeds", len(got), emb.callCount())
	}
}

func TestArtifacts(t *testing.T) {
	e := entry(" lang ", "Chinese", ScopeProfile)
	e.UpdatedBy = "auto"
	long := entry("long", strings.Repeat("x", 5000), "")
	arts, refs := Artifacts([]Entry{e, entry("", "v", ScopeCustom), entry("k", " ", ScopeCustom), long}, nil)
	if len(arts) != 2 || len(refs) != 2 {
		t.Fatalf("%d artifacts", len(arts))
	}
	a := arts[0]
	if a.Kind != "user_memory" || a.SourceType != "user_memory" || a.SourceID != "lang" || a.Title != ScopeProfile ||
		a.Content != "Chinese" || a.Score != 1 || a.TokenEstimate <= 0 || a.ContentHash == "" {
		t.Fatalf("%+v", a)
	}
	if a.Metadata["memory_key"] != "lang" || a.Metadata["scope"] != ScopeProfile || a.Metadata["updated_by"] != "auto" {
		t.Fatalf("metadata: %v", a.Metadata)
	}
	if refs[0].ID != "lang" || refs[0].Label != ScopeProfile {
		t.Fatalf("refs: %+v", refs)
	}
	if arts[1].Title != "long" || len([]rune(arts[1].Content)) >= 5000 {
		t.Fatalf("empty scope should title by key and long values be excerpted: %+v", arts[1].Title)
	}
	again, _ := Artifacts([]Entry{e}, nil)
	if again[0].ContentHash != a.ContentHash {
		t.Fatal("hash not deterministic")
	}
	changed := e
	changed.Value = "English"
	other, _ := Artifacts([]Entry{changed}, nil)
	if other[0].ContentHash == a.ContentHash {
		t.Fatal("hash ignores content")
	}
}

func TestDefaultEstimate(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if DefaultEstimate("") != 0 || DefaultEstimate("abcd") != 1 || DefaultEstimate("abcde") != 2 || DefaultEstimate("\u4e2d\u6587") != 2 {
		t.Fatal("estimator arithmetic changed")
	}
}

func TestPreferenceChangeMovesStateButProfileChangeDoesNot(t *testing.T) {
	env, svc := newEnv(t, Options{})
	ctx := t.Context()
	svc.Upsert(ctx, 1, "lang", "fr", ScopePreference, "user")
	svc.Upsert(ctx, 1, "job", "dev", ScopeProfile, "user")
	state := func() string {
		pc, err := svc.BuildPromptContext(ctx, 1, "hi", nil)
		if err != nil {
			t.Fatal(err)
		}
		return pc.State
	}
	s0 := state()
	env.Clock.Advance(time.Minute)
	svc.Upsert(ctx, 1, "job", "manager", ScopeProfile, "user")
	if state() != s0 {
		t.Fatal("profile change altered the signature")
	}
	env.Clock.Advance(time.Minute)
	svc.Upsert(ctx, 1, "lang", "en", ScopePreference, "user")
	if state() == s0 {
		t.Fatal("preference change did not alter the signature")
	}
}
