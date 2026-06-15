package ragindex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func turnFile(sf SearchFile, mods ...func(*TurnFile)) TurnFile {
	f := TurnFile{
		ObjectID: sf.ObjectID, FileID: sf.FileID, Name: sf.Name, EmbedStatus: sf.State, ChunkCount: sf.ChunkCount, UpdatedAt: sf.UpdatedAt,
		ProcessingReady: true, Text: "full text of " + sf.Name, TextTokens: 10, FullContextOK: true, CurrentTurn: true,
		ExtractStatus: extractReady, SHA256: "sha-" + sf.FileID,
	}
	for _, m := range mods {
		m(&f)
	}
	return f
}

func kbFile(sf SearchFile) KBFile {
	return KBFile{ObjectID: sf.ObjectID, FileID: sf.FileID, Name: sf.Name, ChunkCount: sf.ChunkCount, State: sf.State, UpdatedAt: sf.UpdatedAt}
}

func TestPlanFileModes(t *testing.T) {
	ready := TurnFile{FileID: "f", EmbedStatus: StateReady, FullContextOK: true, TextTokens: 100}
	cases := []struct {
		name  string
		f     TurnFile
		ragOn bool
		in    TurnInput
		want  fileDecision
	}{
		{"rag mode retrieves", ready, true, TurnInput{Mode: ModeRAG}, fileDecision{retrieve: true}},
		{"full context mode never retrieves", ready, true, TurnInput{Mode: ModeFullContext}, fileDecision{inline: true}},
		{"auto keeps small files inline", ready, true, TurnInput{}, fileDecision{inline: true}},
		{"auto retrieves files that cannot be inlined", TurnFile{FileID: "f", EmbedStatus: StateReady}, true, TurnInput{}, fileDecision{retrieve: true}},
		{"auto retrieves above 40 percent of the budget", TurnFile{FileID: "f", EmbedStatus: StateReady, FullContextOK: true, TextTokens: 401},
			true, TurnInput{ContextBudgetFeature: true, ContextBudgetTokens: 1000}, fileDecision{retrieve: true}},
		{"auto stays inline at exactly 40 percent", TurnFile{FileID: "f", EmbedStatus: StateReady, FullContextOK: true, TextTokens: 400},
			true, TurnInput{ContextBudgetFeature: true, ContextBudgetTokens: 1000}, fileDecision{inline: true}},
		{"the budget rule needs the feature", TurnFile{FileID: "f", EmbedStatus: StateReady, FullContextOK: true, TextTokens: 900},
			true, TurnInput{ContextBudgetTokens: 1000}, fileDecision{inline: true}},
		{"opted out files are not retrieved", TurnFile{FileID: "f", EmbedStatus: StateReady, RAGOptOut: true, FullContextOK: true}, true,
			TurnInput{Mode: ModeRAG}, fileDecision{inline: true}},
		{"rag mode falls back to full context", TurnFile{FileID: "f", EmbedStatus: StateStale, FullContextOK: true}, true,
			TurnInput{Mode: ModeRAG}, fileDecision{inline: true}},
		{"rag mode skips what cannot be inlined", TurnFile{FileID: "f", EmbedStatus: StateStale}, true,
			TurnInput{Mode: ModeRAG}, fileDecision{skip: true}},
		{"rag unavailable", ready, false, TurnInput{Mode: ModeRAG}, fileDecision{inline: true}},
		{"no public id", TurnFile{EmbedStatus: StateReady}, true, TurnInput{Mode: ModeRAG}, fileDecision{skip: true}},
	}
	for _, c := range cases {
		if got := planFile(c.f, c.ragOn, c.in); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestPrepareTurnSkips(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango banana"})
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi", Files: []TurnFile{turnFile(files[0])}, // auto mode, small file: inline
	})
	if err != nil || out.Ran || out.Context != "" || !out.Inline["a"] {
		t.Fatalf("%+v %v", out, err)
	}
	out, err = te.svc.PrepareTurn(context.Background(), TurnInput{UserID: 1, Message: "kiwi"})
	if err != nil || out.Ran {
		t.Fatalf("nothing to retrieve: %+v %v", out, err)
	}
}

func TestPrepareTurnHit(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{
		"a": "kiwi mango <banana> & friends", "b": "carrot potato onion",
	})
	searching := 0
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi mango", Mode: ModeRAG,
		Files:     []TurnFile{turnFile(files[0]), turnFile(files[1])},
		Searching: func() { searching++ },
	})
	if err != nil || !out.Ran || searching != 1 {
		t.Fatalf("%+v %v searching=%d", out, err, searching)
	}
	if out.Status != StatusHit || len(out.Sources) != 1 {
		t.Fatalf("%+v", out)
	}
	src := out.Sources[0]
	if src.FileID != "a" || src.FileName != "a.txt" || src.Preview != "kiwi mango <banana> & friends" || src.Modality != "" || src.Score <= 0 {
		t.Fatalf("source %+v", src)
	}
	if !out.Retrieved["a"] || !out.Retrieved["b"] || len(out.Inline) != 0 {
		t.Fatalf("retrieved/inline: %+v %+v", out.Retrieved, out.Inline)
	}
	if !strings.Contains(out.Context, `<passage source="a.txt" position="1">kiwi mango &lt;banana&gt; &amp; friends</passage>`) {
		t.Fatalf("context:\n%s", out.Context)
	}
	// File b (this turn's upload) got no passage: reported, not inlined.
	if !strings.Contains(out.Context, `<file name="b.txt">content not provided</file>`) {
		t.Fatalf("missing unretrieved note:\n%s", out.Context)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts %+v", out.Artifacts)
	}
	a := out.Artifacts[0]
	if a.Kind != KindChunk || a.SourceType != SourceTypeChunk || a.SourceID != "a:0" || a.SourceTitle != "a.txt" || a.ContentHash == "" || a.Score != src.Score {
		t.Fatalf("artifact %+v", a)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(a.MetadataJSON), &meta); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"query", "file_id", "chunk_index", "modality", "score"} {
		if _, ok := meta[k]; !ok {
			t.Fatalf("metadata lacks %q: %v", k, meta)
		}
	}
	if out.Trace == nil || out.Trace.Summary != "retrieved 1 relevant passages" || out.Trace.Payload["hitCount"] != 1 {
		t.Fatalf("trace %+v", out.Trace)
	}
}

func TestPrepareTurnEarlierAttachmentsNote(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango banana", "b": "plum cherry"})
	out, _ := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi mango", Mode: ModeRAG,
		Files: []TurnFile{turnFile(files[0], func(f *TurnFile) { f.CurrentTurn = false }), turnFile(files[1], func(f *TurnFile) { f.CurrentTurn = false })},
	})
	if !strings.Contains(out.Context, "<retrieval_note>") {
		t.Fatalf("earlier documents are announced:\n%s", out.Context)
	}
	if strings.Contains(out.Context, "content not provided") {
		t.Fatalf("only this turn's uploads are reported on a relevance miss:\n%s", out.Context)
	}
}

func TestPrepareTurnFailureFallsBackToFullText(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"a": "kiwi mango banana", "big": "plum cherry"})
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi", Mode: ModeRAG,
		Files: []TurnFile{
			turnFile(files[0]),
			turnFile(files[1], func(f *TurnFile) { f.FullContextOK = false; f.CurrentTurn = false }),
		},
	})
	if err != nil || out.Status != StatusError {
		t.Fatalf("%+v %v", out, err)
	}
	if !out.Inline["a"] || out.Inline["big"] {
		t.Fatalf("inline %+v", out.Inline)
	}
	if !strings.Contains(out.Context, `<file name="a.txt">full text of a.txt</file>`) {
		t.Fatalf("fallback text missing:\n%s", out.Context)
	}
	// A system fault lists every attachment that got nothing, even earlier ones.
	if !strings.Contains(out.Context, `<file name="b.txt">content not provided</file>`) && !strings.Contains(out.Context, `<file name="big.txt">content not provided</file>`) {
		t.Fatalf("unretrieved attachment missing:\n%s", out.Context)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != KindFallback {
		t.Fatalf("%+v", out.Artifacts)
	}
	a := out.Artifacts[0]
	if a.SourceType != SourceTypeFile || a.SourceID != "a" || a.Content != "full text of a.txt" {
		t.Fatalf("%+v", a)
	}
	var meta map[string]any
	json.Unmarshal([]byte(a.MetadataJSON), &meta)
	if meta["reason"] != StatusError || meta["error"] != "failed" || meta["file_id"] != "a" || meta["embed_status"] != StateReady || meta["sha256"] != "sha-a" {
		t.Fatalf("metadata %v", meta)
	}
	if out.Trace == nil || !strings.Contains(out.Trace.Detail, "Full text was substituted for 1 files") || out.Trace.Payload["error"] != "failed" {
		t.Fatalf("trace %+v", out.Trace)
	}
}

func TestPrepareTurnKnowledgeBaseFaultAborts(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"kb": "kiwi mango banana"})
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	_, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi", KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[0])}, Ephemeral: true,
	})
	if !errors.Is(err, ErrKnowledgeBaseUnavailable) {
		t.Fatalf("got %v", err)
	}
	te.emb.set(func(e *fakeEmbedder) { e.err = nil })
	te.cfg.update(func(c *Config) { c.RAGEnabled = false })
	_, err = te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi", KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[0])},
	})
	if !errors.Is(err, ErrKnowledgeBaseUnavailable) {
		t.Fatalf("unavailable service with a selected base: %v", err)
	}
}

func TestPrepareTurnKnowledgeBaseNoEvidence(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.MinSimilarity = 0.9 })
	files := te.docs(1, map[string]string{"kb": "carrot potato onion"})
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi mango", KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[0])}, Ephemeral: true,
	})
	if err != nil || out.Status != StatusLowScore {
		t.Fatalf("%+v %v", out, err)
	}
	if !strings.Contains(out.Context, "<retrieval_status>") || !strings.Contains(out.Context, "Do not claim") || len(out.Sources) != 0 {
		t.Fatalf("context:\n%s", out.Context)
	}
	if out.Trace == nil || !strings.Contains(out.Trace.Summary, "similarity threshold") {
		t.Fatalf("trace %+v", out.Trace)
	}
}

func TestPrepareTurnKnowledgeBaseHit(t *testing.T) {
	te := newEnv(t)
	files := te.docs(1, map[string]string{"kb": "kiwi mango banana", "att": "carrot potato"})
	// Attachment hits, base does not: the notice is added.
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "carrot potato", Mode: ModeRAG, Files: []TurnFile{turnFile(files[0])},
		KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[1])},
	})
	if err != nil || out.Status != StatusHit {
		t.Fatalf("%+v %v", out, err)
	}
	for _, s := range out.Sources {
		if s.FileID == "kb" {
			t.Fatal("no source from the base was found")
		}
	}
	// Both found: no notice.
	out, _ = te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi mango banana", KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[1])}, Ephemeral: true,
	})
	if out.Status != StatusHit || strings.Contains(out.Context, "retrieval_status") {
		t.Fatalf("%+v", out)
	}
	// The status element follows the retrieval section.
	out, _ = te.svc.PrepareTurn(context.Background(), TurnInput{
		UserID: 1, Message: "kiwi mango banana", Mode: ModeRAG, Files: []TurnFile{turnFile(files[1])},
		KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(files[0])},
	})
	if i, j := strings.Index(out.Context, "</retrieval>"), strings.Index(out.Context, "<retrieval_status>"); i < 0 || j < i {
		t.Fatalf("status must come after the retrieval section:\n%s", out.Context)
	}
}

func TestPrepareTurnRetrievalTimeout(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.WaitReadyMS = 30 })
	files := te.docs(1, map[string]string{"a": "kiwi mango"})
	te.emb.set(func(e *fakeEmbedder) { e.delay = 5 * time.Second })
	start := time.Now()
	out, err := te.svc.PrepareTurn(context.Background(), TurnInput{UserID: 1, Message: "kiwi", Mode: ModeRAG, Files: []TurnFile{turnFile(files[0])}})
	if err != nil || out.Status != StatusTimeout || out.Reason != ReasonDeadline {
		t.Fatalf("%+v %v", out, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the wait limit was not applied")
	}
	if !out.Inline["a"] || out.Trace == nil || out.Trace.Payload["error"] != "timed out" {
		t.Fatalf("full text must replace the failed retrieval: %+v", out)
	}
}

func TestPrepareTurnImageChunk(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.Protocol = "jina" })
	te.emb.set(func(e *fakeEmbedder) { e.images = true })
	img := te.addFile(1, "pic", func(f *File) {
		f.FileName, f.MimeType, f.DetectedMIME, f.FileCategory = "pic.png", "image/png", "image/png", "image"
		f.ExtractText = "kiwi mango photo"
	})
	te.content.original[img.ID] = pngBytes(t, 4, 4, false)
	g := te.indexNow(img)
	sf := SearchFile{ObjectID: g.ID, FileID: g.FileID, Name: g.FileName, ChunkCount: 1, State: g.EmbedStatus, UpdatedAt: g.UpdatedAt}
	// The pixel vector is ("image image/png"), so ask for that text.
	run := func(attached bool) TurnOutput {
		out, err := te.svc.PrepareTurn(context.Background(), TurnInput{
			UserID: 1, Message: "image png", KnowledgeBaseSelected: true, KBFiles: []KBFile{kbFile(sf)}, Ephemeral: true,
			Files: []TurnFile{{FileID: "pic", Attached: attached}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := run(false)
	if out.Status != StatusHit || len(out.Sources) != 1 || out.Sources[0].Modality != "image" {
		t.Fatalf("%+v", out)
	}
	if len(out.Attach) != 1 || out.Attach[0].FileID != "pic" || out.Attach[0].Name != "pic.png" || !strings.Contains(out.Context, `kind="image"`) {
		t.Fatalf("attach %v\n%s", out.Attach, out.Context)
	}
	if out = run(true); len(out.Attach) != 0 {
		t.Fatalf("an image already attached is not attached twice: %v", out.Attach)
	}
}

func TestRenderContext(t *testing.T) {
	got := renderContext(contextParts{
		hits: []Hit{
			{FileName: `a"b.txt`, ChunkIndex: 0, Content: "zero <x>"},
			{FileName: "blank.txt", ChunkIndex: 4, Content: "   "},
			{FileName: "c.txt", ChunkIndex: 7, Content: "seven"},
		},
	})
	want := "<retrieval>\n<passage source=\"a&#34;b.txt\" position=\"1\">zero &lt;x&gt;</passage>\n<passage source=\"c.txt\" position=\"7\">seven</passage>\n</retrieval>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if renderContext(contextParts{}) != "" {
		t.Fatal("an empty plan renders nothing")
	}
}

func TestArtifacts(t *testing.T) {
	long := strings.Repeat("x", 5000)
	a, ok := chunkArtifact("q", Hit{FileID: "", FileName: "n.txt", ChunkIndex: 3, Content: long, Score: 0.5, Modality: ModalityText})
	if !ok || a.SourceID != "n.txt:3" || len([]rune(a.Content)) != 2000 || a.TokenEstimate != EstimateTokens(long) {
		t.Fatalf("%+v", a)
	}
	if a.ContentHash != contentHash(KindChunk, "n.txt:3", long) {
		t.Fatal("the hash covers kind, source id and the full content")
	}
	if _, ok := chunkArtifact("q", Hit{FileID: "f", Content: "  "}); ok {
		t.Fatal("blank chunks are not stored")
	}
	f := TurnFile{Name: "n", Text: "t", SHA256: "abc"}
	b, _ := fallbackArtifact("q", SearchResult{Status: StatusTimeout}, "timed out", f)
	if b.SourceID != "abc" {
		t.Fatalf("source id falls back to the sha256: %q", b.SourceID)
	}
	f.SHA256 = ""
	b, _ = fallbackArtifact("q", SearchResult{Status: StatusError}, "failed", f)
	if b.SourceID != "n" {
		t.Fatalf("then to the name: %q", b.SourceID)
	}
	f.ObjectID = 12
	b, _ = fallbackArtifact("q", SearchResult{Status: StatusError}, "failed", f)
	if b.SourceID != "12" {
		t.Fatalf("internal id before sha256: %q", b.SourceID)
	}
	if _, ok := fallbackArtifact("q", SearchResult{}, "", TurnFile{Name: "n"}); ok {
		t.Fatal("files without text are not stored")
	}
}

func TestRAGAvailable(t *testing.T) {
	te := newEnv(t)
	ctx := context.Background()
	if ok, why := te.svc.RAGAvailable(ctx); !ok || why != "" {
		t.Fatalf("%v %q", ok, why)
	}
	te.cfg.update(func(c *Config) { c.RAGEnabled = false })
	if ok, why := te.svc.RAGAvailable(ctx); ok || why != ReasonRAGDisabled {
		t.Fatalf("%v %q", ok, why)
	}
	te.cfg.update(func(c *Config) { c.RAGEnabled = true; c.Host = "" })
	if ok, why := te.svc.RAGAvailable(ctx); ok || why != ReasonHostMissing {
		t.Fatalf("%v %q", ok, why)
	}
}
