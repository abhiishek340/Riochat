package ragindex

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const testDims = 64

// fakeEmbedder embeds text as a hashed bag of words so that texts sharing words
// are close in cosine similarity.
type fakeEmbedder struct {
	mu       sync.Mutex
	err      error
	reqs     []EmbedRequest
	short    bool // return one vector too few
	dims     int
	images   bool
	failNext int
	hook     func(EmbedRequest)
	delay    time.Duration
}

func (f *fakeEmbedder) SupportsImages(string) bool { return f.images }

func (f *fakeEmbedder) Embed(ctx context.Context, r EmbedRequest) ([][]float32, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	err := f.err
	if f.failNext > 0 {
		f.failNext--
		err = ErrProviderUnavailable
	}
	short := f.short
	dims := f.dims
	hook := f.hook
	delay := f.delay
	f.mu.Unlock()
	if hook != nil {
		hook(r)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if dims == 0 {
		dims = testDims
	}
	out := make([][]float32, 0, len(r.Inputs))
	for _, in := range r.Inputs {
		if in.IsImage() {
			out = append(out, wordsVector("image "+in.ImageMIME, dims))
			continue
		}
		out = append(out, wordsVector(in.Text, dims))
	}
	if short && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (f *fakeEmbedder) requests() []EmbedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]EmbedRequest(nil), f.reqs...)
}

func (f *fakeEmbedder) set(fn func(*fakeEmbedder)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func wordsVector(text string, dims int) []float32 {
	v := make([]float32, dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%uint32(dims)]++
	}
	if len(words) == 0 {
		v[0] = 1
	}
	return v
}

// fakeConfig is a mutable ConfigSource.
type fakeConfig struct {
	mu sync.Mutex
	c  Config
}

func (f *fakeConfig) RAGConfig(context.Context) Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.c
}

func (f *fakeConfig) update(fn func(*Config)) {
	f.mu.Lock()
	fn(&f.c)
	f.mu.Unlock()
}

type fakeContent struct {
	mu       sync.Mutex
	text     map[uint]string
	textErr  error
	original map[uint][]byte
	calls    int
}

func (c *fakeContent) Text(_ context.Context, f File) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.textErr != nil {
		return "", c.textErr
	}
	return c.text[f.ID], nil
}

func (c *fakeContent) Original(_ context.Context, f File, limit int64) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.original[f.ID]
	if !ok {
		return nil, errors.New("not stored")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("too large")
	}
	return b, nil
}

type fakeShared struct {
	mu  sync.Mutex
	ids map[uint]bool
}

func (s *fakeShared) EnabledBuiltin(_ context.Context, ids []uint) (map[uint]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uint]bool{}
	for _, id := range ids {
		if s.ids[id] {
			out[id] = true
		}
	}
	return out, nil
}

func (s *fakeShared) set(id uint, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids == nil {
		s.ids = map[uint]bool{}
	}
	s.ids[id] = on
}

type fakeUsers struct{}

func (fakeUsers) Labels(_ context.Context, ids []uint) (map[uint]string, error) {
	out := map[uint]string{}
	for _, id := range ids {
		out[id] = "user-" + string(rune('a'+id%26))
	}
	return out, nil
}

// tenv is the test environment: one mounted area on the test kit.
type tenv struct {
	*testkit.Env
	t       *testing.T
	svc     *Service
	cfg     *fakeConfig
	emb     *fakeEmbedder
	content *fakeContent
	shared  *fakeShared
	next    uint
}

func onConfig() Config {
	c := DefaultConfig()
	c.Enabled = true
	c.Host = "https://embed.example.test/v1"
	c.Model = "test-embed"
	c.Dimensions = testDims
	c.RAGEnabled = true
	c.ChunkSizeTokens = 20
	c.ChunkOverlapTokens = 2
	c.MinSimilarity = 0.2
	return c
}

func newEnv(t *testing.T, mods ...func(*Config)) *tenv {
	t.Helper()
	te := &tenv{t: t, cfg: &fakeConfig{c: onConfig()}, emb: &fakeEmbedder{images: false}, content: &fakeContent{text: map[uint]string{}, original: map[uint][]byte{}}, shared: &fakeShared{}}
	for _, m := range mods {
		m(&te.cfg.c)
	}
	te.Env = testkit.New(t, []features.Feature{{
		Name: "ragindex",
		Migrations: func() []migrate.Step {
			return append([]migrate.Step{migrate.AutoMigrate("files", &File{})}, Migrations()...)
		},
		Mount: func(d deps.Deps) error {
			svc, err := MountWith(d, Options{
				Config: te.cfg, Embedder: te.emb, Content: te.content, Shared: te.shared, Users: fakeUsers{},
				DisableWorkers: true,
			})
			te.svc = svc
			return err
		},
	}})
	return te
}

// addFile inserts an active, text-ready file.
func (te *tenv) addFile(owner uint, id string, mods ...func(*File)) File {
	te.t.Helper()
	te.next++
	f := File{
		FileID: id, UserID: owner, FileName: id + ".txt", MimeType: "text/plain", DetectedMIME: "text/plain",
		FileCategory: "document", SizeBytes: 100, StoragePath: "chat-files/" + id, Status: fileActive,
		ProcessingStatus: procReady, ProcessingReady: true, ExtractStatus: extractReady,
		ExtractText: "alpha beta gamma delta epsilon zeta", EmbedStatus: StateNone,
		CreatedAt: te.Clock.Now(), UpdatedAt: te.Clock.Now(),
	}
	for _, m := range mods {
		m(&f)
	}
	if err := te.DB.Create(&f).Error; err != nil {
		te.t.Fatalf("addFile %s: %v", id, err)
	}
	return f
}

func (te *tenv) reload(f File) File {
	te.t.Helper()
	var out File
	if err := te.DB.Unscoped().First(&out, f.ID).Error; err != nil {
		te.t.Fatalf("reload: %v", err)
	}
	return out
}

func (te *tenv) sig() string { return te.cfg.RAGConfig(context.Background()).Signature() }

// indexNow submits the file as its owner and drains the queue.
func (te *tenv) indexNow(f File) File {
	te.t.Helper()
	res, err := te.svc.Submit(context.Background(), f.UserID, []string{f.FileID})
	if err != nil || len(res.SubmittedFileIDs) != 1 {
		te.t.Fatalf("submit %s: %+v %v", f.FileID, res, err)
	}
	te.svc.DrainQueue(context.Background())
	return te.reload(f)
}

func (te *tenv) chunkCount(f File) int64 {
	var n int64
	te.DB.Model(&Chunk{}).Where("file_obj_id = ?", f.ID).Count(&n)
	return n
}

func (te *tenv) vectorCount(f File) int64 {
	var n int64
	te.DB.Model(&ChunkVector{}).Where("file_obj_id = ?", f.ID).Count(&n)
	return n
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
