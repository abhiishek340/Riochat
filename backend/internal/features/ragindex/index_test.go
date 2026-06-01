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

func TestPublishRejectsBadVectors(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "bad")
	sig := te.sig()
	te.DB.Model(&File{}).Where("id = ?", f.ID).UpdateColumns(map[string]any{"embed_status": StateProcessing, "embed_signature": sig})
	chunk := func(s string) Chunk { return Chunk{FileObjID: f.ID, UserID: 1, EmbeddingSignature: s, Content: "x"} }
	if _, err := te.svc.publish(context.Background(), f, sig, builtIndex{chunks: []Chunk{chunk(sig)}, vecs: [][]float32{{}}}); err == nil {
		t.Fatal("empty vector must abort")
	}
	if _, err := te.svc.publish(context.Background(), f, sig, builtIndex{chunks: []Chunk{chunk("other")}, vecs: [][]float32{{1}}}); err == nil {
		t.Fatal("foreign chunk signature must abort")
	}
	if _, err := te.svc.publish(context.Background(), f, sig, builtIndex{chunks: []Chunk{chunk(sig)}, vecs: [][]float32{make([]float32, maxVectorDims+1)}}); err == nil {
		t.Fatal("oversized vector must abort")
	}
	if _, err := te.svc.publish(context.Background(), f, sig, builtIndex{chunks: []Chunk{chunk(sig)}, vecs: nil}); err == nil {
		t.Fatal("count mismatch must abort")
	}
	if te.chunkCount(f) != 0 {
		t.Fatal("aborted publishes must leave nothing behind")
	}
}

func TestSweepFailsStalledProcessing(t *testing.T) {
	te := newEnv(t)
	sig := te.sig()
	stuck := te.addFile(1, "stuck", func(f *File) { f.EmbedStatus = StateProcessing; f.EmbedSignature = sig })
	embedding := te.addFile(1, "emb", func(f *File) {
		f.EmbedStatus = StateProcessing
		f.EmbedSignature = sig
		f.ProcessingStatus = procEmbedding
		f.ProcessingReady = false
	})
	fresh := te.addFile(1, "fresh", func(f *File) { f.EmbedStatus = StateProcessing; f.EmbedSignature = sig })
	te.Clock.Advance(7 * time.Minute)
	te.DB.Model(&File{}).Where("id = ?", fresh.ID).UpdateColumns(map[string]any{"updated_at": te.Clock.Now()})
	n, err := te.svc.Sweep(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("swept %d: %v", n, err)
	}
	if g := te.reload(stuck); g.EmbedStatus != StateFailed || g.EmbedError != errStalled {
		t.Fatalf("stuck: %q %q", g.EmbedStatus, g.EmbedError)
	}
	g := te.reload(embedding)
	if g.EmbedStatus != StateFailed || g.ProcessingStatus != procReady || !g.ProcessingReady || g.ProcessingErrCod != "embed_failed" {
		t.Fatalf("embedding: %+v", g)
	}
	if g := te.reload(fresh); g.EmbedStatus != StateProcessing {
		t.Fatalf("fresh file must stay processing, got %q", g.EmbedStatus)
	}
}

func TestReconcileMarksOldSpaceStale(t *testing.T) {
	te := newEnv(t)
	f := te.indexNow(te.addFile(1, "r1"))
	other := te.addFile(1, "r2") // never indexed: untouched
	queuedNow := te.addFile(1, "r3")
	if _, err := te.svc.Submit(context.Background(), 1, []string{"r3"}); err != nil {
		t.Fatal(err)
	}
	if err := te.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := te.reload(f); g.EmbedStatus != StateReady {
		t.Fatal("same space must stay ready")
	}
	te.cfg.update(func(c *Config) {
		c.Host = "https://moved.example.test/v1"
		c.StoredSignature = SpaceSignature(c.Model, c.Dimensions, c.Host)
	})
	for i := 0; i < 2; i++ { // idempotent
		if err := te.svc.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if g := te.reload(f); g.EmbedStatus != StateStale || g.EmbedError != errReindexRequired {
			t.Fatalf("round %d: %q %q", i, g.EmbedStatus, g.EmbedError)
		}
	}
	if g := te.reload(other); g.EmbedStatus != StateNone {
		t.Fatalf("never indexed file changed to %q", g.EmbedStatus)
	}
	if g := te.reload(queuedNow); g.EmbedStatus != StateStale {
		t.Fatalf("queued under the old space must be stale, got %q", g.EmbedStatus)
	}
}

func TestMigrationMarksUnsignedReadyStale(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "u1", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = "x" })
	g := te.addFile(1, "u2", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = "x" })
	te.DB.Create(&Chunk{FileObjID: f.ID, UserID: 1, Content: "a", EmbeddingSignature: ""})
	te.DB.Create(&Chunk{FileObjID: g.ID, UserID: 1, Content: "a", EmbeddingSignature: "x"})
	if err := markUnsignedStale(context.Background(), te.DB); err != nil {
		t.Fatal(err)
	}
	if te.reload(f).EmbedStatus != StateStale || te.reload(g).EmbedStatus != StateReady {
		t.Fatal("only the file with an unsigned chunk becomes stale")
	}
}

func TestBatchInputs(t *testing.T) {
	var in []EmbedInput
	for i := 0; i < 45; i++ {
		in = append(in, EmbedInput{Text: "t"})
	}
	b := batchInputs(in, 20)
	if len(b) != 3 || len(b[0]) != 20 || len(b[2]) != 5 {
		t.Fatalf("batches: %d", len(b))
	}
	if len(batchInputs(in, 0)) != 3 {
		t.Fatal("non-positive size means 20")
	}
	img := func(n int) EmbedInput { return EmbedInput{ImageMIME: "image/png", ImageData: make([]byte, n)} }
	b = batchInputs([]EmbedInput{img(8 << 20), img(8 << 20), img(1 << 20)}, 20)
	if len(b) != 2 || len(b[0]) != 1 || len(b[1]) != 2 {
		t.Fatalf("image batches: %v", b)
	}
	b = batchInputs([]EmbedInput{img(13 << 20)}, 20)
	if len(b) != 1 {
		t.Fatal("a single oversized image forms its own batch")
	}
}

func pngBytes(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if noise {
				img.SetRGBA(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{10, 20, 30, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPrepareImage(t *testing.T) {
	small := pngBytes(t, 10, 10, false)
	data, mime, err := prepareImage(small, "image/png")
	if err != nil || mime != "image/png" || !bytes.Equal(data, small) {
		t.Fatalf("small image must pass unchanged: %v %q", err, mime)
	}
	if _, _, err := prepareImage(small, "image/bmp"); !errors.Is(err, errImageFormat) {
		t.Fatalf("unsupported format: %v", err)
	}
	big := pngBytes(t, 1800, 1800, true)
	if len(big) <= imageSendLimit {
		t.Skipf("noise image only %d bytes", len(big))
	}
	data, mime, err = prepareImage(big, "image/png")
	if err != nil || mime != "image/jpeg" || len(data) > imageSendLimit {
		t.Fatalf("downscale: %v %q %d", err, mime, len(data))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || max(cfg.Width, cfg.Height) > 1536 {
		t.Fatalf("resized dimensions %+v %v", cfg, err)
	}
	if _, _, err := prepareImage(append(bytes.Repeat([]byte{0}, imageSendLimit+1), 1), "image/webp"); !errors.Is(err, errImageTooLarge) {
		t.Fatalf("oversized webp: %v", err)
	}
	if _, _, err := prepareImage(append([]byte("not an image"), make([]byte, imageSendLimit)...), "image/png"); !errors.Is(err, errImageFormat) {
		t.Fatalf("undecodable oversized image: %v", err)
	}
}

func TestImageIndexedOnPixelPath(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.Protocol = "jina" })
	te.emb.set(func(e *fakeEmbedder) { e.images = true })
	img := te.addFile(3, "pic", func(f *File) {
		f.FileName, f.MimeType, f.DetectedMIME, f.FileCategory = "pic.png", "image/png", "image/png", "image"
		f.ExtractText = "  caption text  "
	})
	te.content.original[img.ID] = pngBytes(t, 8, 8, false)
	got := te.indexNow(img)
	if got.EmbedStatus != StateReady || got.ChunkCount != 1 {
		t.Fatalf("got %q chunks %d", got.EmbedStatus, got.ChunkCount)
	}
	var c Chunk
	te.DB.Where("file_obj_id = ?", img.ID).First(&c)
	if c.Modality != ModalityImage || c.Content != "caption text" || c.ChunkIndex != 0 {
		t.Fatalf("chunk %+v", c)
	}
	reqs := te.emb.requests()
	if len(reqs[len(reqs)-1].Inputs) != 1 || !reqs[len(reqs)-1].Inputs[0].IsImage() {
		t.Fatal("the picture itself must be sent")
	}
}

func TestImageUnsupportedFormatFails(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.Protocol = "jina"; c.ImageOCREnabled = false })
	te.emb.set(func(e *fakeEmbedder) { e.images = true })
	// A GIF is allowed by the pixel rule; undecodable oversized data fails.
	img := te.addFile(3, "gif", func(f *File) {
		f.FileName, f.MimeType, f.DetectedMIME, f.FileCategory = "a.gif", "image/gif", "image/gif", "image"
	})
	te.content.original[img.ID] = append([]byte("GIF89a"), make([]byte, imageSendLimit+10)...)
	te.svc.Submit(context.Background(), 3, []string{"gif"})
	te.svc.DrainQueue(context.Background())
	if got := te.reload(img); got.EmbedStatus != StateFailed || got.EmbedError != errImageBad {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
}

func TestSubmitEnqueueFailure(t *testing.T) {
	te := newEnv(t)
	te.svc.enqueueFn = func(context.Context, *Job) error { return errors.New("queue down") }
	f := te.addFile(1, "qf")
	res, err := te.svc.Submit(context.Background(), 1, []string{"qf"})
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0].Reason != SkipQueueBusy {
		t.Fatalf("%+v %v", res, err)
	}
	if got := te.reload(f); got.EmbedStatus != StateFailed || got.EmbedError != errQueueBusy {
		t.Fatalf("got %q %q", got.EmbedStatus, got.EmbedError)
	}
}

func TestCloneIndexCopiesChunksAndVectors(t *testing.T) {
	te := newEnv(t)
	src := te.indexNow(te.addFile(1, "src", func(f *File) { f.ExtractText = strings.Repeat("some words to index\n", 50) }))
	dst := te.addFile(2, "dst")
	if err := te.svc.CloneIndex(context.Background(), nil, src.ID, dst.ID); err != nil {
		t.Fatal(err)
	}
	got := te.reload(dst)
	if got.EmbedStatus != StateReady || got.EmbedSignature != src.EmbedSignature || got.ChunkCount != src.ChunkCount {
		t.Fatalf("target %+v", got)
	}
	if te.chunkCount(dst) != te.chunkCount(src) || te.vectorCount(dst) != te.vectorCount(src) || te.vectorCount(dst) == 0 {
		t.Fatal("chunk and vector rows must be copied")
	}
	var c Chunk
	te.DB.Where("file_obj_id = ?", dst.ID).First(&c)
	if c.UserID != 2 {
		t.Fatalf("copied chunk owner %d", c.UserID)
	}
	// A source without a ready index cannot be cloned.
	other := te.addFile(1, "none")
	if err := te.svc.CloneIndex(context.Background(), nil, other.ID, dst.ID); !errors.Is(err, ErrCloneUnavailable) {
		t.Fatalf("got %v", err)
	}
	// A vector row missing from the source rolls the clone back.
	te.DB.Where("file_obj_id = ?", src.ID).Limit(1).Delete(&ChunkVector{})
	dst2 := te.addFile(2, "dst2")
	if err := te.svc.CloneIndex(context.Background(), nil, src.ID, dst2.ID); err == nil {
		t.Fatal("clone with a vector count mismatch must fail")
	}
	if te.chunkCount(dst2) != 0 || te.reload(dst2).EmbedStatus == StateReady {
		t.Fatal("failed clone must leave no trace")
	}
}

func TestForgetRemovesIndex(t *testing.T) {
	te := newEnv(t)
	f := te.indexNow(te.addFile(1, "gone"))
	if err := te.svc.Forget(context.Background(), nil, f.ID); err != nil {
		t.Fatal(err)
	}
	if te.chunkCount(f) != 0 || te.vectorCount(f) != 0 {
		t.Fatal("index data must be removed")
	}
}

func TestAfterProcessingAutoIndexes(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "auto")
	te.svc.AfterProcessing(context.Background(), f.ID, true)
	eventually(t, "auto index to finish", func() bool { return te.reload(f).EmbedStatus == StateReady })
	if got := te.reload(f); got.ProcessingStatus != procReady || got.ChunkCount == 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestAfterProcessingRespectsSwitches(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.TriggerOnUpload = false })
	f := te.addFile(1, "off")
	te.svc.AfterProcessing(context.Background(), f.ID, true)
	time.Sleep(50 * time.Millisecond)
	if te.reload(f).EmbedStatus != StateNone {
		t.Fatal("auto-trigger off must not index")
	}
	// An explicit request left on the file is honoured anyway.
	sig := te.sig()
	te.DB.Model(&File{}).Where("id = ?", f.ID).UpdateColumns(map[string]any{"embed_status": StateQueued, "embed_signature": sig})
	te.svc.AfterProcessing(context.Background(), f.ID, true)
	eventually(t, "requested index", func() bool { return te.reload(f).EmbedStatus == StateReady })
}

func TestAfterProcessingFailureAndStaleRequests(t *testing.T) {
	te := newEnv(t)
	sig := te.sig()
	failed := te.addFile(1, "pf", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig })
	te.svc.AfterProcessing(context.Background(), failed.ID, false)
	if g := te.reload(failed); g.EmbedStatus != StateNone {
		t.Fatalf("processing failure must revert the request, got %q", g.EmbedStatus)
	}
	old := te.addFile(1, "old", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = "old@16" })
	te.svc.AfterProcessing(context.Background(), old.ID, true)
	if g := te.reload(old); g.EmbedStatus != StateStale {
		t.Fatalf("outdated request must become stale, got %q", g.EmbedStatus)
	}
	te.cfg.update(func(c *Config) { c.Enabled = false })
	req := te.addFile(1, "req", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig })
	te.svc.AfterProcessing(context.Background(), req.ID, true)
	if g := te.reload(req); g.EmbedStatus != StateFailed || g.EmbedError != errServiceConf {
		t.Fatalf("unavailable service: %q %q", g.EmbedStatus, g.EmbedError)
	}
}

func TestUploadRunFailureKeepsProcessingReady(t *testing.T) {
	te := newEnv(t)
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	f := te.addFile(1, "upfail")
	te.svc.AfterProcessing(context.Background(), f.ID, true)
	eventually(t, "failure", func() bool { return te.reload(f).EmbedStatus == StateFailed })
	eventually(t, "processing restored", func() bool { return te.reload(f).ProcessingStatus == procReady })
	got := te.reload(f)
	if !got.ProcessingReady || got.ProcessingErrCod != "embed_failed" || got.ProcessingErrMsg != errServiceDownMsg {
		t.Fatalf("got %+v", got)
	}
}
