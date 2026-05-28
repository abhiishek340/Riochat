package ragindex

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestSignatures(t *testing.T) {
	if ModelSignature(" m ", 1536) != ModelSignature("m", 1536) {
		t.Fatal("model whitespace must not change the legacy signature")
	}
	if SpaceSignature(" m ", 1536, "https://x/v1") != SpaceSignature("m", 1536, "https://x/v1") {
		t.Fatal("model whitespace must not change the space signature")
	}
	if ModelSignature("m", 1536) == ModelSignature("m", 768) {
		t.Fatal("dimensions must change the signature")
	}
	base := SpaceSignature("m", 1536, "https://x/v1")
	if base != SpaceSignature("m", 1536, "  https://x/v1/// ") {
		t.Fatal("endpoint slashes and spaces must not matter")
	}
	if base == SpaceSignature("m", 1536, "https://y/v1") {
		t.Fatal("a different endpoint is a different space")
	}
	if SpaceSignature("m", 768, "https://x/v1") == base || SpaceSignature("m", 1536, "https://x/v1") != base {
		t.Fatal("switching dimension and back must reproduce the signature")
	}
	if !strings.HasSuffix(base, "@1536") || len(strings.Split(base, "@")[0]) != 16 {
		t.Fatalf("unexpected current format %q", base)
	}
	if legacy := ModelSignature("m", 1536); len(strings.Split(legacy, "@")[0]) != 8 {
		t.Fatalf("unexpected legacy format %q", legacy)
	}
}

func TestEffectiveSignature(t *testing.T) {
	c := DefaultConfig()
	if c.Signature() != "" {
		t.Fatal("no model gives an empty signature")
	}
	c.Model, c.Dimensions = "m", 1536
	if c.Signature() != ModelSignature("m", 1536) {
		t.Fatal("without a stored signature the legacy value applies")
	}
	c.StoredSignature = "abc@1536"
	if c.Signature() != "abc@1536" {
		t.Fatal("the stored signature wins")
	}
}

func TestIndexTextFileBecomesReady(t *testing.T) {
	te := newEnv(t)
	long := strings.Repeat("alpha beta gamma delta\n", 60)
	f := te.addFile(7, "f1", func(f *File) { f.ExtractText = long })
	got := te.indexNow(f)
	if got.EmbedStatus != StateReady || got.EmbedSignature != te.sig() {
		t.Fatalf("state %q signature %q", got.EmbedStatus, got.EmbedSignature)
	}
	n := te.chunkCount(f)
	if n < 2 || int64(got.ChunkCount) != n || te.vectorCount(f) != n {
		t.Fatalf("chunk_count %d chunks %d vectors %d", got.ChunkCount, n, te.vectorCount(f))
	}
	var chunks []Chunk
	te.DB.Where("file_obj_id = ?", f.ID).Order("chunk_index").Find(&chunks)
	for i, c := range chunks {
		if c.ChunkIndex != i || c.EmbeddingSignature != te.sig() || c.Modality != ModalityText || c.UserID != 7 || c.TokenCount == 0 {
			t.Fatalf("chunk %d: %+v", i, c)
		}
	}
	reqs := te.emb.requests()
	if len(reqs) == 0 || reqs[0].Purpose != PurposeDocument || reqs[0].Model != "test-embed" || reqs[0].Dimensions != testDims {
		t.Fatalf("provider request: %+v", reqs)
	}
}

func TestIndexNormalizesVectors(t *testing.T) {
	te := newEnv(t)
	f := te.indexNow(te.addFile(1, "n1"))
	var v ChunkVector
	if err := te.DB.Where("file_obj_id = ?", f.ID).First(&v).Error; err != nil {
		t.Fatal(err)
	}
	vec := decodeVector(v.Embedding)
	var sum float64
	for _, x := range vec {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Fatalf("vector is not unit length: %f", sum)
	}
	te.cfg.update(func(c *Config) { c.Normalize = false })
	g := te.indexNow(te.addFile(1, "n2"))
	v = ChunkVector{}
	te.DB.Where("file_obj_id = ?", g.ID).First(&v)
	sum = 0
	for _, x := range decodeVector(v.Embedding) {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) < 1e-3 {
		t.Fatal("normalization was applied although switched off")
	}
}

func TestWhitespaceTextEndsEmptyNotFailed(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "blank", func(f *File) { f.ExtractText = "   \n\t" })
	got := te.indexNow(f)
	if got.EmbedStatus != StateEmpty || got.ChunkCount != 0 || got.EmbedError == "" {
		t.Fatalf("got %+v", got)
	}
	st, err := te.svc.Status(context.Background())
	if err != nil || st.EmptyCount != 1 || st.FailedCount != 0 {
		t.Fatalf("status %+v %v", st, err)
	}
}

func TestExtractStatusEmptySkipsExtraction(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "e", func(f *File) { f.ExtractText = ""; f.ExtractStatus = extractEmpty })
	te.content.text[f.ID] = "would be found"
	if got := te.indexNow(f); got.EmbedStatus != StateEmpty || te.content.calls != 0 {
		t.Fatalf("state %q, extractor calls %d", got.EmbedStatus, te.content.calls)
	}
}

func TestMissingTextIsExtractedAgain(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "x", func(f *File) { f.ExtractText = ""; f.ExtractStatus = "unsupported" })
	te.content.text[f.ID] = "fresh extracted words here"
	got := te.indexNow(f)
	if got.EmbedStatus != StateReady || te.content.calls != 1 {
		t.Fatalf("state %q calls %d", got.EmbedStatus, te.content.calls)
	}
}

func TestProviderDownRetriesThenFails(t *testing.T) {
	te := newEnv(t)
	te.emb.set(func(e *fakeEmbedder) { e.err = errors.New("secret provider text https://internal.example/key=abc") })
	f := te.addFile(1, "down")
	if _, err := te.svc.Submit(context.Background(), 1, []string{"down"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for attempt := 1; attempt <= 2; attempt++ {
		te.svc.DrainQueue(ctx)
		got := te.reload(f)
		if got.EmbedStatus != StateQueued || got.EmbedError != errGeneric {
			t.Fatalf("attempt %d: state %q error %q", attempt, got.EmbedStatus, got.EmbedError)
		}
		te.Clock.Advance(time.Minute)
	}
	te.svc.DrainQueue(ctx)
	got := te.reload(f)
	if got.EmbedStatus != StateFailed {
		t.Fatalf("third failure must fail the file, got %q", got.EmbedStatus)
	}
	if strings.Contains(got.EmbedError, "secret") || strings.Contains(got.EmbedError, "internal") {
		t.Fatalf("provider text leaked: %q", got.EmbedError)
	}
	var jobs int64
	te.DB.Model(&Job{}).Count(&jobs)
	if jobs != 0 {
		t.Fatalf("job must be gone after the final failure, %d left", jobs)
	}
}

func TestProviderUnavailableSummary(t *testing.T) {
	te := newEnv(t)
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	f := te.addFile(1, "unavail")
	te.svc.Submit(context.Background(), 1, []string{"unavail"})
	for i := 0; i < 3; i++ {
		te.svc.DrainQueue(context.Background())
		te.Clock.Advance(time.Minute)
	}
	if got := te.reload(f); got.EmbedStatus != StateFailed || got.EmbedError != errServiceDownMsg {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
}

func TestPermanentFailureDoesNotRetry(t *testing.T) {
	te := newEnv(t)
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrModalityUnsupported })
	f := te.addFile(1, "perm")
	te.svc.Submit(context.Background(), 1, []string{"perm"})
	te.svc.DrainQueue(context.Background())
	if got := te.reload(f); got.EmbedStatus != StateFailed || got.EmbedError != errProtocolInput {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
}

func TestBatchSizeMismatchFails(t *testing.T) {
	te := newEnv(t)
	te.emb.set(func(e *fakeEmbedder) { e.short = true })
	f := te.addFile(1, "mm")
	te.svc.Submit(context.Background(), 1, []string{"mm"})
	for i := 0; i < 3; i++ {
		te.svc.DrainQueue(context.Background())
		te.Clock.Advance(time.Minute)
	}
	got := te.reload(f)
	if got.EmbedStatus != StateFailed || got.EmbedError != errGeneric || te.chunkCount(f) != 0 {
		t.Fatalf("got %q %q chunks %d", got.EmbedStatus, got.EmbedError, te.chunkCount(f))
	}
}

func TestConfigChangeDuringRunEndsStale(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "mid")
	te.emb.set(func(e *fakeEmbedder) {
		e.hook = func(EmbedRequest) { te.cfg.update(func(c *Config) { c.Host = "https://other.example.test/v1" }) }
	})
	te.svc.Submit(context.Background(), 1, []string{"mid"})
	te.svc.DrainQueue(context.Background())
	got := te.reload(f)
	if got.EmbedStatus != StateStale || got.EmbedError != errConfigDuring {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
}

func TestDisablingEmbeddingStalesQueuedFiles(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "q")
	te.svc.Submit(context.Background(), 1, []string{"q"})
	te.cfg.update(func(c *Config) { c.Enabled = false })
	te.svc.DrainQueue(context.Background())
	if got := te.reload(f); got.EmbedStatus != StateStale || got.EmbedError != errConfigResubmit {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
	var jobs int64
	te.DB.Model(&Job{}).Count(&jobs)
	if jobs != 0 {
		t.Fatal("message must be settled")
	}
}

func TestDuplicateDeliveryIgnored(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "dup")
	sig := te.sig()
	// Another consumer already processes the file under the same signature.
	te.DB.Model(&File{}).Where("id = ?", f.ID).UpdateColumns(map[string]any{"embed_status": StateProcessing, "embed_signature": sig})
	out, err := te.svc.runIndex(context.Background(), f.ID, sig, "https://embed.example.test/v1", runOpts{})
	if err != nil || out != outIgnored {
		t.Fatalf("duplicate run: %v %v", out, err)
	}
	// A takeover (expired lease) proceeds.
	out, err = te.svc.runIndex(context.Background(), f.ID, sig, "https://embed.example.test/v1", runOpts{takeover: true})
	if err != nil || out != outReady {
		t.Fatalf("takeover run: %v %v", out, err)
	}
}

func TestReindexReplacesChunksAndVectors(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "re", func(f *File) { f.ExtractText = strings.Repeat("one two three four five six\n", 40) })
	te.indexNow(f)
	first := te.chunkCount(f)
	var oldIDs []uint
	te.DB.Model(&Chunk{}).Where("file_obj_id = ?", f.ID).Pluck("id", &oldIDs)
	te.DB.Model(&File{}).Where("id = ?", f.ID).UpdateColumns(map[string]any{"embed_status": StateStale, "extract_text": strings.Repeat("seven eight\n", 20)})
	te.indexNow(f)
	second := te.chunkCount(f)
	if second == 0 || te.vectorCount(f) != second {
		t.Fatalf("chunks %d vectors %d", second, te.vectorCount(f))
	}
	var left int64
	te.DB.Model(&ChunkVector{}).Where("chunk_id IN ?", oldIDs).Count(&left)
	if left != 0 || first == second && false {
		t.Fatalf("%d old vector rows remain", left)
	}
	var old int64
	te.DB.Model(&Chunk{}).Where("id IN ?", oldIDs).Count(&old)
	if old != 0 {
		t.Fatal("old chunk rows remain")
	}
}

func TestSupersededTaskWritesNothing(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "sup")
	te.indexNow(f)
	before := te.chunkCount(f)
	// A newer task owns the file under another signature.
	te.DB.Model(&File{}).Where("id = ?", f.ID).UpdateColumns(map[string]any{"embed_status": StateProcessing, "embed_signature": "newer@16"})
	applied, err := te.svc.publish(context.Background(), f, te.sig(), builtIndex{
		chunks: []Chunk{{FileObjID: f.ID, UserID: 1, EmbeddingSignature: te.sig(), Content: "x"}}, vecs: [][]float32{{1, 0}},
	})
	if err != nil || applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if te.chunkCount(f) != before {
		t.Fatal("a superseded task must not write")
	}
}
