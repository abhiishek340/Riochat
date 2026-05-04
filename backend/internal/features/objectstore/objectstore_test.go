package objectstore

import (
	"bytes"
	"context"
	"encoding/hex"
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

	"github.com/riochat/riochat/backend/internal/platform/config"
)

func TestNormalizeKey(t *testing.T) {
	good := map[string]string{
		"  a/b.txt ":    "a/b.txt",
		"\\a\\b\\c":     "a/b/c",
		"/lead/trail/":  "lead/trail",
		"a//b":          "a//b",
		"model-icons/x": "model-icons/x",
	}
	for in, want := range good {
		got, err := NormalizeKey(in)
		if err != nil || got != want {
			t.Errorf("NormalizeKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "/", "../x", "a/../b", "..", "C:/x", "a\\..\\b"} {
		if _, err := NormalizeKey(in); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("NormalizeKey(%q) should be invalid, got %v", in, err)
		}
	}
}

func readKey(t *testing.T, s Store, key string) string {
	t.Helper()
	obj, err := s.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("open %q: %v", key, err)
	}
	defer obj.Body.Close()
	b, _ := io.ReadAll(obj.Body)
	return string(b)
}

// Scenario 23: traversal is rejected, delete is silent, overwrite is atomic,
// an interrupted write leaves nothing visible.
func TestLocalContract(t *testing.T) {
	root := t.TempDir()
	l := NewLocal(root)
	ctx := context.Background()

	if _, err := l.Write(ctx, "../x", strings.NewReader("evil"), -1, ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("write ../x: %v", err)
	}
	if _, err := l.Open(ctx, "../x"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("open ../x: %v", err)
	}
	if err := l.Delete(ctx, "../x"); err != nil {
		t.Fatalf("delete invalid key must succeed: %v", err)
	}
	if err := l.Delete(ctx, ""); err != nil {
		t.Fatalf("delete empty key must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x")); err == nil {
		t.Fatal("traversal escaped the root")
	}

	info, err := l.Write(ctx, "u/2026/05/f_a.txt", strings.NewReader("hello"), 5, "text/plain")
	if err != nil || info.Size != 5 || info.Key != "u/2026/05/f_a.txt" {
		t.Fatalf("write: %+v %v", info, err)
	}
	obj, err := l.Open(ctx, "/u/2026/05/f_a.txt/")
	if err != nil {
		t.Fatal(err)
	}
	if obj.ContentType != "" || obj.Size != 5 {
		t.Fatalf("local open metadata: %+v", obj)
	}
	obj.Body.Close()

	if _, err := l.Open(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing open: %v", err)
	}
	if err := l.Delete(ctx, "missing"); err != nil {
		t.Fatalf("missing delete: %v", err)
	}

	// An interrupted write keeps the old content and leaves no temp file.
	_, err = l.Write(ctx, "u/2026/05/f_a.txt", io.MultiReader(strings.NewReader("par"), errReader{}), -1, "")
	if err == nil {
		t.Fatal("expected failed write")
	}
	if got := readKey(t, l, "u/2026/05/f_a.txt"); got != "hello" {
		t.Fatalf("partial write became visible: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "u/2026/05"))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	// A declared size that does not match fails and changes nothing.
	if _, err := l.Write(ctx, "u/2026/05/f_a.txt", strings.NewReader("abc"), 10, ""); err == nil {
		t.Fatal("size mismatch must fail")
	}
	// Overwrite replaces content.
	if _, err := l.Write(ctx, "u/2026/05/f_a.txt", strings.NewReader("bye"), 3, ""); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, l, "u/2026/05/f_a.txt"); got != "bye" {
		t.Fatalf("overwrite: %q", got)
	}

	p, cleanup, err := l.Materialize(ctx, "u/2026/05/f_a.txt")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if b, _ := os.ReadFile(p); string(b) != "bye" {
		t.Fatal("local materialize must keep the real file")
	}
	if err := l.Delete(ctx, "u/2026/05/f_a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(ctx, "u/2026/05/f_a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted open: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestLocalDefaultRoot(t *testing.T) {
	if NewLocal("  ").Root() != DefaultRoot {
		t.Fatal("blank root must default to ./storage")
	}
}

// fakeS3 is a path-style in-memory S3 that records what it received.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	auth    []string
	paths   []string
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string][]byte{}, types: map[string]string{}}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.paths = append(f.paths, r.URL.Path)
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = b
		f.types[r.URL.Path] = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	case http.MethodGet:
		b, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>secret detail</Message></Error>`))
			return
		}
		w.Header().Set("Content-Type", f.types[r.URL.Path])
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, r.URL.Path)
		w.WriteHeader(204)
	}
}

func newS3(t *testing.T, srv *httptest.Server, prefix string) *S3 {
	t.Helper()
	s, err := NewS3(S3Options{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "bkt", Prefix: prefix,
		AccessKeyID: "AKID", SecretAccessKey: "secret", PathStyle: true,
		Now: func() time.Time { return time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
