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
