package ragindex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// docs indexes one single-chunk file per entry and returns the search files.
func (te *tenv) docs(owner uint, texts map[string]string) []SearchFile {
	te.t.Helper()
	var out []SearchFile
	ids := make([]string, 0, len(texts))
	for id := range texts {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		f := te.addFile(owner, id, func(f *File) { f.ExtractText = texts[id] })
		g := te.indexNow(f)
		out = append(out, SearchFile{ObjectID: g.ID, FileID: g.FileID, Name: g.FileName, ChunkCount: g.ChunkCount, State: g.EmbedStatus, UpdatedAt: g.UpdatedAt})
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestRetrieveGate(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango banana"})
	ctx := context.Background()
	for name, mod := range map[string]func(*Config){
		"rag off": func(c *Config) { c.RAGEnabled = false }, "embedding off": func(c *Config) { c.Enabled = false },
		"no model": func(c *Config) { c.Model = "" },
	} {
		te.cfg.update(mod)
		if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi", Files: files}); r.Status != StatusUnavailable {
			t.Fatalf("%s: %q", name, r.Status)
		}
		te.cfg.update(func(c *Config) { *c = onConfig() })
	}
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "  ", Files: files}); r.Status != StatusEmpty {
		t.Fatalf("blank query: %q", r.Status)
	}
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi"}); r.Status != StatusEmpty {
		t.Fatalf("no files: %q", r.Status)
	}
}

func TestRetrieveThreshold(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.MinSimilarity = 0.45 })
	files := te.docs(1, map[string]string{
		"hit":  "kiwi mango banana",
		"weak": "kiwi pear plum cherry grape melon lime",
		"miss": "carrot potato onion",
	})
	ctx := context.Background()
	r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi mango banana", Files: files})
	if r.Status != StatusHit || r.Reason != ReasonMatched || len(r.Hits) == 0 {
		t.Fatalf("%+v", r)
	}
	for _, h := range r.Hits {
		if h.Score < 0.45 || h.FileID == "miss" {
			t.Fatalf("below-threshold passage returned: %+v", h)
		}
	}
	if r.Hits[0].Modality != ModalityText || r.Hits[0].FileName == "" || r.Hits[0].Content == "" {
		t.Fatalf("hit fields: %+v", r.Hits[0])
	}
	if r.Candidates != 3 || r.Filtered < 1 || r.Filtered > 2 || r.MaxScore < 0.99 {
		t.Fatalf("diagnostics: %+v", r)
	}
	low := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "zucchini", Files: files})
	if low.Status != StatusLowScore || low.Reason != ReasonBelowMin || low.Candidates != 3 || low.Filtered != 0 {
		t.Fatalf("low score: %+v", low)
	}
	none := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi", Files: []SearchFile{{ObjectID: 99999, FileID: "ghost"}}})
	if none.Status != StatusEmpty || none.Reason != ReasonNoCandidates {
		t.Fatalf("no candidates: %+v", none)
	}
}

func TestRetrieveEmbeddingFailures(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango"})
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files})
	if r.Status != StatusError || r.Reason != ReasonRetrievalError || r.Err == nil {
		t.Fatalf("%+v", r)
	}
	te.emb.set(func(e *fakeEmbedder) { e.err = nil; e.short = true })
	if r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files}); r.Status != StatusError {
		t.Fatalf("empty embedding: %+v", r)
	}
	te.emb.set(func(e *fakeEmbedder) { e.short = false; e.err = context.DeadlineExceeded })
	if r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files}); r.Status != StatusTimeout || r.Reason != ReasonDeadline {
		t.Fatalf("timeout: %+v", r)
	}
	te.emb.set(func(e *fakeEmbedder) { e.err = nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi", Files: files}); r.Status != StatusTimeout {
		t.Fatalf("expired context: %+v", r)
	}
}

func TestRetrieveUsesQueryPurposeAndNoNormalizationFlag(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.BatchSize = 1 })
	files := te.docs(1, map[string]string{"a": "kiwi mango"})
	te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files})
	reqs := te.emb.requests()
	if last := reqs[len(reqs)-1]; last.Purpose != PurposeQuery || len(last.Inputs) != 1 {
		t.Fatalf("%+v", last)
	}
}

func TestSelectHits(t *testing.T) {
	hit := func(chunk uint, file uint, idx int, score float64, tokens int) Hit {
		return Hit{ChunkID: chunk, FileObjectID: file, FileID: fmt.Sprint("f", file), ChunkIndex: idx, Score: score, TokenCount: tokens, Content: "c"}
	}
	order := map[uint]int{1: 0, 2: 1, 3: 2}
	t.Run("top-k and document order", func(t *testing.T) {
		cands := []Hit{hit(1, 2, 3, 0.9, 10), hit(2, 1, 2, 0.85, 10), hit(3, 1, 1, 0.8, 10), hit(4, 3, 0, 0.79, 10)}
		got := selectHits(cands, 3, 1000, order)
		if len(got) != 3 {
			t.Fatalf("%d", len(got))
		}
		// file order 1, 2: (f1,1) (f1,2) (f2,3)
		if got[0].ChunkID != 3 || got[1].ChunkID != 2 || got[2].ChunkID != 1 {
			t.Fatalf("order: %v", got)
		}
	})
	t.Run("two per file when scores are close", func(t *testing.T) {
		cands := []Hit{hit(1, 1, 0, 0.9, 10), hit(2, 1, 1, 0.89, 10), hit(3, 1, 2, 0.88, 10), hit(4, 2, 0, 0.87, 10)}
		got := selectHits(cands, 3, 1000, order)
		count := map[uint]int{}
		for _, h := range got {
			count[h.FileObjectID]++
		}
		if count[1] != 2 || count[2] != 1 {
			t.Fatalf("%v", count)
		}
	})
	t.Run("pass two fills by rank when scores are far apart", func(t *testing.T) {
		cands := []Hit{hit(1, 1, 0, 0.9, 10), hit(2, 1, 1, 0.5, 10), hit(3, 1, 2, 0.4, 10)}
		got := selectHits(cands, 3, 1000, order)
		if len(got) != 3 {
			t.Fatalf("pass two must fill the free slots without the cap or ratio: %d", len(got))
		}
	})
	t.Run("budget skips overflowing chunks but later smaller ones fit", func(t *testing.T) {
		cands := []Hit{hit(1, 1, 0, 0.9, 60), hit(2, 2, 0, 0.89, 60), hit(3, 3, 0, 0.88, 30)}
		got := selectHits(cands, 3, 100, order)
		ids := []uint{}
		for _, h := range got {
			ids = append(ids, h.ChunkID)
		}
		if len(got) != 2 || ids[0] != 1 || ids[1] != 3 {
			t.Fatalf("%v", ids)
		}
	})
	t.Run("stored token count of zero is estimated", func(t *testing.T) {
		big := Hit{ChunkID: 1, FileObjectID: 1, Score: 1, Content: strings.Repeat("x", 4000)}
		if got := selectHits([]Hit{big}, 5, 100, order); len(got) != 0 {
			t.Fatal("an estimated 1000-token chunk must not fit a 100-token budget")
		}
	})
	t.Run("defaults for non-positive limits", func(t *testing.T) {
		var cands []Hit
		for i := 0; i < 8; i++ {
			cands = append(cands, hit(uint(i+1), uint(i+1), 0, 0.9, 10))
		}
		if got := selectHits(cands, 0, 0, map[uint]int{}); len(got) != 5 {
			t.Fatalf("default top-k is 5, got %d", len(got))
		}
	})
}

func TestRetrieveSelectionRespectsBudget(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.TopK = 5; c.TokenBudget = 128 })
	files := te.docs(1, map[string]string{
		"a": "kiwi mango banana", "b": "kiwi mango plum", "c": "kiwi mango lime",
	})
	te.DB.Model(&Chunk{}).Where("1 = 1").UpdateColumn("token_count", 60)
	r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi mango", Files: files, Ephemeral: true})
	if r.Status != StatusHit || len(r.Hits) != 2 {
		t.Fatalf("only two 60-token chunks fit a 128 budget: %+v", r)
	}
	te.DB.Model(&Chunk{}).Where("1 = 1").UpdateColumn("token_count", 500)
	te.cfg.update(func(c *Config) { c.TokenBudget = 128 })
	r = te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi mango", Files: files, Ephemeral: true})
	if r.Status != StatusEmpty || r.Reason != ReasonBudget {
		t.Fatalf("%+v", r)
	}
}

func TestRetrieveHybrid(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.Hybrid = true; c.MinSimilarity = 0.9 })
	files := te.docs(1, map[string]string{
		"exact": "kiwi mango banana",
		"near":  "kiwi mango pear plum cherry grape",
		"kwonly": "the word kiwi appears with filler words that move the vector far from the query nothing " +
			"else sharing meaning today",
	})
	te.cfg.update(func(c *Config) { c.TokenBudget = 100000; c.TopK = 10 })
	r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files, Ephemeral: true})
	if r.Status != StatusHit {
		t.Fatalf("%+v", r)
	}
	got := map[string]float64{}
	for _, h := range r.Hits {
		got[h.FileID] = h.Score
		if h.Score <= 0 || h.Score > 1 {
			t.Fatalf("hybrid score outside (0,1]: %+v", h)
		}
	}
	if _, ok := got["kwonly"]; !ok {
		t.Fatalf("keyword-only hits must survive the vector threshold: %v", got)
	}
	// Keyword failure degrades to the vector side only.
	te.svc.keywords = func(context.Context, []uint, string, string, int) ([]scored, error) {
		return nil, errors.New("fts down")
	}
	te.cfg.update(func(c *Config) { c.MinSimilarity = 0.1 })
	r = te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi mango banana", Files: files, Ephemeral: true})
	if r.Status != StatusHit {
		t.Fatalf("keyword failure must not fail the retrieval: %+v", r)
	}
}

func TestKeywordSearchSQLite(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "Kiwi Mango banana", "b": "kiwi only", "c": "100% sure_thing"})
	var ids []uint
	for _, f := range files {
		ids = append(ids, f.ObjectID)
	}
	ctx := context.Background()
	hits, err := te.svc.keywordSearch(ctx, ids, te.sig(), "KIWI mango", 10)
	if err != nil || len(hits) != 1 || hits[0].Score != 0.5 {
		t.Fatalf("every term must match: %v %v", hits, err)
	}
	hits, _ = te.svc.keywordSearch(ctx, ids, te.sig(), "kiwi", 10)
	if len(hits) != 2 || hits[0].ChunkID > hits[1].ChunkID {
		t.Fatalf("chunk id order: %v", hits)
	}
	hits, _ = te.svc.keywordSearch(ctx, ids, te.sig(), "100%", 10)
	if len(hits) != 1 {
		t.Fatalf("percent must be literal: %v", hits)
	}
	hits, _ = te.svc.keywordSearch(ctx, ids, te.sig(), "_", 10)
	if len(hits) != 1 {
		t.Fatalf("underscore must be literal: %v", hits)
	}
}

func TestKeywordTokens(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	got := keywordTokens("Hello, world42 \u4f60\u597d! x-y")
	want := []string{"hello", "world42", "\u4f60", "\u597d", "x", "y"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
	if len(keywordTokens("!!! ---")) != 0 {
		t.Fatal("separators only")
	}
}

func TestFuse(t *testing.T) {
	vec := []scored{{1, 0.9}, {2, 0.8}}
	kw := []scored{{1, 0.5}, {3, 0.5}}
	got := fuse(vec, kw, 10)
	if len(got) != 3 || got[0].ChunkID != 1 || math.Abs(got[0].Score-1) > 1e-9 {
		t.Fatalf("first in both lists scores 1: %v", got)
	}
	want2 := (1.0 / 62) / (2.0 / 61)
	want3 := (1.0 / 62) / (2.0 / 61)
	if math.Abs(got[1].Score-want2) > 1e-9 || math.Abs(got[2].Score-want3) > 1e-9 {
		t.Fatalf("%v", got)
	}
	if got[1].ChunkID != 2 || got[2].ChunkID != 3 {
		t.Fatalf("ties break by chunk id: %v", got)
	}
	if len(fuse(vec, kw, 2)) != 2 {
		t.Fatal("truncate to the limit")
	}
	if len(fuse(nil, nil, 5)) != 0 {
		t.Fatal("nothing in, nothing out")
	}
	for _, s := range fuse(vec, kw, 10) {
		if s.Score <= 0 || s.Score > 1 {
			t.Fatalf("%v", s)
		}
	}
}

func TestRetrieveCache(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.CacheTTLSeconds = 120 })
	files := te.docs(1, map[string]string{"a": "kiwi mango banana"})
	ctx := context.Background()
	req := SearchRequest{UserID: 1, Query: "kiwi   mango", Files: files}
	first := te.svc.Retrieve(ctx, req)
	if first.Status != StatusHit || first.Cached {
		t.Fatalf("%+v", first)
	}
	calls := len(te.emb.requests())
	again := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi mango", Files: files})
	if !again.Cached || again.Reason != ReasonCacheHit || again.Status != StatusHit || len(again.Hits) != len(first.Hits) {
		t.Fatalf("whitespace-collapsed repeat must hit: %+v", again)
	}
	if len(te.emb.requests()) != calls {
		t.Fatal("a cache hit must not call the provider")
	}
	// Changing file state or any retrieval setting changes the key.
	changed := append([]SearchFile(nil), files...)
	changed[0].UpdatedAt = changed[0].UpdatedAt.Add(time.Second)
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi mango", Files: changed}); r.Cached {
		t.Fatal("file state is part of the key")
	}
	for name, mod := range map[string]func(*Config){
		"top-k": func(c *Config) { c.TopK = 7 }, "min": func(c *Config) { c.MinSimilarity = 0.31 }, "budget": func(c *Config) { c.TokenBudget = 999 },
		"fetch": func(c *Config) { c.FetchMultiplier = 9 }, "hybrid": func(c *Config) { c.Hybrid = true }, "model": func(c *Config) { c.Model = "other" },
		"host": func(c *Config) { c.Host = "https://elsewhere.test" }, "dims": func(c *Config) { c.Dimensions = 99 },
	} {
		before := onConfig()
		after := before
		mod(&after)
		k1 := cacheKey(before, 1, "q", files)
		k2 := cacheKey(after, 1, "q", files)
		if k1 == k2 {
			t.Fatalf("%s must change the cache key", name)
		}
	}
	if cacheKey(onConfig(), 1, "q", files) == cacheKey(onConfig(), 2, "q", files) {
		t.Fatal("the user is part of the key")
	}
	// TTL 0 disables the cache; ephemeral searches never use it.
	te.cfg.update(func(c *Config) { c.CacheTTLSeconds = 0 })
	if r := te.svc.Retrieve(ctx, req); r.Cached {
		t.Fatal("ttl 0 disables caching")
	}
	te.cfg.update(func(c *Config) { c.CacheTTLSeconds = 120 })
	te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "banana", Files: files, Ephemeral: true})
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "banana", Files: files, Ephemeral: true}); r.Cached {
		t.Fatal("temporary chats never cache")
	}
	te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "banana", Files: files})
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "banana", Files: files, Ephemeral: true}); r.Cached {
		t.Fatal("ephemeral searches do not read entries written by others")
	}
}

func TestRetrieveUserIsolation(t *testing.T) {
	te := newEnv(t)
	mine := te.docs(1, map[string]string{"mine": "kiwi mango banana"})
	theirs := te.docs(2, map[string]string{"theirs": "kiwi mango banana secret"})
	ctx := context.Background()
	r := te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi mango", Files: append(append([]SearchFile{}, mine...), theirs...)})
	if r.Status != StatusHit {
		t.Fatalf("%+v", r)
	}
	for _, h := range r.Hits {
		if h.FileID == "theirs" {
			t.Fatal("another user's file leaked")
		}
	}
	r = te.svc.Retrieve(ctx, SearchRequest{UserID: 1, Query: "kiwi mango", Files: theirs})
	if r.Status != StatusEmpty || r.Reason != ReasonNoCandidates {
		t.Fatalf("only foreign files: %+v", r)
	}
	if r := te.svc.Retrieve(ctx, SearchRequest{UserID: 0, Query: "kiwi mango", Files: mine}); r.Status != StatusEmpty {
		t.Fatalf("user 0 never reads: %+v", r)
	}
}

func TestRetrieveBuiltinKnowledgeBaseFiles(t *testing.T) {
	te := newEnv(t)
	pool := te.docs(0, map[string]string{"pool": "kiwi mango banana reference"})
	ctx := context.Background()
	req := SearchRequest{UserID: 9, Query: "kiwi mango", Files: pool, Ephemeral: true}
	if r := te.svc.Retrieve(ctx, req); r.Status != StatusEmpty {
		t.Fatalf("a pool file outside an enabled builtin base is invisible: %+v", r)
	}
	te.shared.set(pool[0].ObjectID, true)
	r := te.svc.Retrieve(ctx, req)
	if r.Status != StatusHit || r.Hits[0].FileID != "pool" {
		t.Fatalf("builtin base files are searchable by everyone: %+v", r)
	}
	te.shared.set(pool[0].ObjectID, false)
	if r := te.svc.Retrieve(ctx, req); r.Status != StatusEmpty {
		t.Fatalf("disabled base stops matching: %+v", r)
	}
}

func TestRetrieveOnlyCurrentSignature(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango banana"})
	te.cfg.update(func(c *Config) {
		c.Host = "https://new-endpoint.test"
		c.StoredSignature = SpaceSignature(c.Model, c.Dimensions, c.Host)
	})
	r := te.svc.Retrieve(context.Background(), SearchRequest{UserID: 1, Query: "kiwi", Files: files})
	if r.Status != StatusEmpty || r.Reason != ReasonNoCandidates {
		t.Fatalf("vectors from another space must be invisible: %+v", r)
	}
}

func TestBuildQuery(t *testing.T) {
	prior := []string{"first question", "", "second question", "third " + strings.Repeat("x", 400), "fourth"}
	if got := BuildQuery("  now ", prior, 0); got != "now" {
		t.Fatalf("%q", got)
	}
	got := BuildQuery("now", prior, 2)
	want := "now\n\nRecent user context:\n- third " + strings.Repeat("x", 240-len("third ")) + "\n- fourth"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	got = BuildQuery("now", prior, 20)
	if !strings.Contains(got, "- first question\n- second question\n- third") || strings.Count(got, "\n- ") != 4 {
		t.Fatalf("blank messages are skipped and order is oldest first: %q", got)
	}
	if got := BuildQuery("now", []string{" ", ""}, 3); got != "now" {
		t.Fatalf("no usable history: %q", got)
	}
}

func TestDedupHits(t *testing.T) {
	seen := map[string]bool{}
	a := []Hit{{FileID: "f", ChunkIndex: 1}, {FileID: "f", ChunkIndex: 2}}
	if got := DedupHits(seen, a); len(got) != 2 {
		t.Fatal(got)
	}
	b := []Hit{{FileID: "f", ChunkIndex: 2}, {FileID: "g", ChunkIndex: 2}}
	got := DedupHits(seen, b)
	if len(got) != 1 || got[0].FileID != "g" {
		t.Fatalf("%v", got)
	}
}

func TestPreview(t *testing.T) {
	long := strings.Repeat("word  \n ", 100)
	p := Preview(long)
	if len([]rune(p)) > 100 || strings.ContainsAny(p, "\n\t") || strings.Contains(p, "  ") {
		t.Fatalf("%q", p)
	}
}
