package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

func TestNormalizeKey(t *testing.T) {
	for in, want := range map[string]string{" a/b.txt ": "a/b.txt", `\a\b`: "a/b", "//x//": "x"} {
		got, err := NormalizeKey(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q,%v", in, got, err)
		}
	}
	for _, in := range []string{"", "  ", "///", `\`} {
		if _, err := NormalizeKey(in); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%q should be invalid", in)
		}
	}
}

func TestLocalRoundTripAndTraversal(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := l.Put(ctx, "a/b.txt", strings.NewReader("hello"), 5, "text/plain")
	if err != nil || info.Size != 5 || info.Key != "a/b.txt" || info.ModTime.IsZero() {
		t.Fatalf("put: %+v %v", info, err)
	}
	rc, oi, err := l.Open(ctx, "/a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" || oi.Size != 5 || oi.ContentType != "" {
		t.Fatalf("open: %q %+v", b, oi)
	}
	for _, bad := range []string{"../x", "a/../../x", "..", "a/../.."} {
		if _, err := l.Put(ctx, bad, strings.NewReader("x"), 1, ""); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("put %q: %v", bad, err)
		}
		if _, _, err := l.Open(ctx, bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("open %q: %v", bad, err)
		}
	}
	if err := l.Delete(ctx, "../x"); err != nil {
		t.Fatalf("delete of an invalid key is a no-op: %v", err)
	}
	if err := l.Delete(ctx, "missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if _, _, err := l.Open(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	path, cleanup, err := l.Materialize(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "hello" {
		t.Fatal("materialized content")
	}
	cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("local materialize cleanup must be a no-op")
	}
	if err := l.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Open(ctx, "a/b.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted object still readable")
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n > 0 {
		f.n--
		copy(p, "partial")
		return 7, nil
	}
	return 0, errors.New("boom")
}

func TestLocalFailedWriteLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	if _, err := l.Put(ctx, "d/x.bin", &failingReader{n: 1}, -1, ""); err == nil {
		t.Fatal("expected failure")
	}
	if _, _, err := l.Open(ctx, "d/x.bin"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial object visible")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "d"))
	if len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	// An overwrite that fails keeps the previous content.
	if _, err := l.Put(ctx, "d/x.bin", strings.NewReader("old"), 3, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "d/x.bin", &failingReader{n: 1}, -1, ""); err == nil {
		t.Fatal("expected failure")
	}
	rc, _, _ := l.Open(ctx, "d/x.bin")
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "old" {
		t.Fatalf("content after failed overwrite: %q", b)
	}
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Put(ctx2, "c", strings.NewReader("x"), 1, ""); err == nil {
		t.Fatal("canceled context must abort the write")
	}
}

// ---- S3 -------------------------------------------------------------------

// The worked example from the SigV4 documentation (GET object with Range).
func TestSigV4DocumentationVector(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	req.Header.Set("x-amz-content-sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	c := &credentials{id: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	signV4(req, c, "us-east-1", "s3", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	got := req.Header.Get("Authorization")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
}

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	reqs    []*http.Request
	lengths []int64
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string][]byte{}, types: map[string]string{}}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	key := r.URL.Path
	switch r.Method {
	case "PUT":
		b, _ := io.ReadAll(r.Body)
		f.objects[key] = b
		f.types[key] = r.Header.Get("Content-Type")
		f.lengths = append(f.lengths, r.ContentLength)
		w.WriteHeader(200)
	case "GET":
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`)
			return
		}
		w.Header().Set("Content-Type", f.types[key])
		w.Header().Set("Last-Modified", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(http.TimeFormat))
		w.Write(b)
	case "DELETE":
		delete(f.objects, key)
		w.WriteHeader(204)
	}
}

func newS3(t *testing.T, f *fakeS3, mut func(*S3Options)) *S3 {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	o := S3Options{Endpoint: srv.URL, Bucket: "bkt", Prefix: "p", PathStyle: true, AccessKeyID: "AK", SecretAccessKey: "SK",
		Now: func() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) }}
	if mut != nil {
		mut(&o)
	}
	s, err := NewS3(o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestS3PrefixAndRoundTrip(t *testing.T) {
	f := newFakeS3()
	s := newS3(t, f, nil)
	ctx := context.Background()
	info, err := s.Put(ctx, "/a", strings.NewReader("data"), 4, "text/plain")
	if err != nil || info.Size != 4 || info.Key != "a" {
		t.Fatalf("put: %+v %v", info, err)
	}
	if _, ok := f.objects["/bkt/p/a"]; !ok {
		t.Fatalf("object should be p/a in bucket bkt, have %v", f.objects)
	}
	if f.lengths[0] != 4 {
		t.Fatalf("content length: %v", f.lengths)
	}
	r := f.reqs[0]
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/20260301/us-east-1/s3/aws4_request") ||
		r.Header.Get("x-amz-date") != "20260301T090000Z" {
		t.Fatalf("signature headers: %v", r.Header)
	}
	rc, oi, err := s.Open(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "data" || oi.ContentType != "text/plain" || oi.Size != 4 || oi.ModTime.Year() != 2026 {
		t.Fatalf("open: %q %+v", b, oi)
	}
	if _, _, err := s.Open(ctx, "zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	p, cleanup, err := s.Materialize(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(p), "riochat-object-") {
		t.Fatalf("temp name %s", p)
	}
	if d, _ := os.ReadFile(p); string(d) != "data" {
		t.Fatal("materialized content")
	}
	cleanup()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the temp file")
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	if err := s.Delete(ctx, "  "); err != nil {
		t.Fatalf("invalid key delete: %v", err)
	}
}

func TestS3UnknownSizeAndEscaping(t *testing.T) {
	f := newFakeS3()
	s := newS3(t, f, func(o *S3Options) { o.Prefix = "" })
	if _, err := s.Put(context.Background(), "dir/my file+1.txt", bytes.NewReader([]byte("12345")), -1, ""); err != nil {
		t.Fatal(err)
	}
	if f.lengths[0] != 5 {
		t.Fatalf("spooled length: %v", f.lengths)
	}
	if _, ok := f.objects["/bkt/dir/my file+1.txt"]; !ok {
		t.Fatalf("keys: %v", f.objects)
	}
	if !strings.Contains(f.reqs[0].URL.EscapedPath(), "my%20file%2B1.txt") {
		t.Fatalf("path not canonically escaped: %s", f.reqs[0].URL.EscapedPath())
	}
}

func TestS3RequiresBucketAndVirtualHost(t *testing.T) {
	if _, err := NewS3(S3Options{}); err == nil || err.Error() != "s3 bucket is required" {
		t.Fatalf("got %v", err)
	}
	s, _ := NewS3(S3Options{Endpoint: "https://r2.example.com", Bucket: "b", Region: ""})
	u, _ := s.objectURL("k/x")
	if u.String() != "https://b.r2.example.com/k/x" || s.o.Region != "auto" {
		t.Fatalf("virtual host url %s region %s", u, s.o.Region)
	}
	aws, _ := NewS3(S3Options{Bucket: "b", Region: "eu-west-1"})
	u, _ = aws.objectURL("k")
	if u.String() != "https://b.s3.eu-west-1.amazonaws.com/k" {
		t.Fatalf("aws url %s", u)
	}
}

func TestS3AnonymousAndEnvCredentials(t *testing.T) {
	f := newFakeS3()
	s := newS3(t, f, func(o *S3Options) {
		o.AccessKeyID, o.SecretAccessKey = "", ""
		o.Getenv = func(string) string { return "" }
	})
	if _, err := s.Put(context.Background(), "a", strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	if f.reqs[0].Header.Get("Authorization") != "" {
		t.Fatal("no credentials: request must be unsigned")
	}
	env := map[string]string{"AWS_ACCESS_KEY_ID": "EK", "AWS_SECRET_ACCESS_KEY": "ES", "AWS_SESSION_TOKEN": "tok"}
	s2 := newS3(t, f, func(o *S3Options) {
		o.AccessKeyID, o.SecretAccessKey = "", ""
		o.Getenv = func(k string) string { return env[k] }
	})
	if _, err := s2.Put(context.Background(), "b", strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	last := f.reqs[len(f.reqs)-1]
	if !strings.Contains(last.Header.Get("Authorization"), "Credential=EK/") || last.Header.Get("x-amz-security-token") != "tok" {
		t.Fatalf("env credential chain: %v", last.Header)
	}
}

func TestS3ServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
	}))
	defer srv.Close()
	s, _ := NewS3(S3Options{Endpoint: srv.URL, Bucket: "b", PathStyle: true})
	_, err := s.Put(context.Background(), "a", strings.NewReader("x"), 1, "")
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

type spanSink struct {
	mu    sync.Mutex
	spans []*trace.SpanData
}

func (s *spanSink) Export(_ context.Context, sp []*trace.SpanData) error {
	s.mu.Lock()
	s.spans = append(s.spans, sp...)
	s.mu.Unlock()
	return nil
}
func (s *spanSink) Shutdown(context.Context) error { return nil }

func TestS3Tracing(t *testing.T) {
	sink := &spanSink{}
	tr := trace.New(trace.Options{Exporter: sink, SamplingRate: 1, FlushEvery: time.Hour})
	f := newFakeS3()
	s := newS3(t, f, func(o *S3Options) { o.Tracer = tr })
	ctx := context.Background()
	s.Put(ctx, "a", strings.NewReader("x"), 1, "text/plain")
	rc, _, _ := s.Open(ctx, "a")
	// The open span is still running until the stream closes.
	tr.Shutdown(ctx)
	sink.mu.Lock()
	names := map[string]bool{}
	for _, sp := range sink.spans {
		names[sp.Name] = true
	}
	sink.mu.Unlock()
	if !names["objectstore.s3.put"] || names["objectstore.s3.open"] {
		t.Fatalf("spans before close: %v", names)
	}
	rc.Close()
}

func TestSelectionAndLazyReload(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	cur := config.StorageConfig{Backend: "weird", Local: config.LocalStorageConfig{RootDir: dir1}}
	lazy := NewLazy(func() Settings { return SettingsFrom(cur) }, nil)
	ctx := context.Background()
	if _, err := lazy.Put(ctx, "k", strings.NewReader("v"), 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir1, "k")); err != nil {
		t.Fatal("unknown backend values select local")
	}
	cur.Local.RootDir = dir2
	if _, _, err := lazy.Open(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("config change must apply to the next operation: %v", err)
	}
	cur.Backend = "s3"
	if _, err := lazy.Current(); err == nil {
		t.Fatal("s3 without a bucket must fail")
	}
}
