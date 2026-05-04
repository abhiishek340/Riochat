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

// Scenario 22: prefix handling, not-found outcome, silent delete of a missing key.
func TestS3PrefixAndNotFound(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s := newS3(t, srv, "/app/prod/")
	ctx := context.Background()

	info, err := s.Write(ctx, "u/2026/05/f_a.txt", strings.NewReader("data"), 4, "text/plain")
	if err != nil || info.Key != "u/2026/05/f_a.txt" || info.Size != 4 {
		t.Fatalf("write: %+v %v", info, err)
	}
	if _, ok := fake.objects["/bkt/app/prod/u/2026/05/f_a.txt"]; !ok {
		t.Fatalf("object not stored at the prefixed key: %v", fake.paths)
	}
	if !strings.HasPrefix(fake.auth[0], "AWS4-HMAC-SHA256 Credential=AKID/20260501/us-east-1/s3/aws4_request") {
		t.Fatalf("unsigned request: %q", fake.auth[0])
	}
	obj, err := s.Open(ctx, "u/2026/05/f_a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(obj.Body)
	obj.Body.Close()
	if string(b) != "data" || obj.ContentType != "text/plain" || obj.ModTime.Year() != 2006 {
		t.Fatalf("open: %q %+v", b, obj)
	}
	if _, err := s.Open(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	if err := s.Delete(ctx, "nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.Delete(ctx, "  "); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	if err := s.Delete(ctx, "u/2026/05/f_a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, "u/2026/05/f_a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestS3ErrorDoesNotLeakBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>internal host leak</Message></Error>`))
	}))
	defer srv.Close()
	s := newS3(t, srv, "")
	_, err := s.Open(context.Background(), "k")
	var se *S3Error
	if !errors.As(err, &se) || se.Code != "AccessDenied" || strings.Contains(err.Error(), "leak") {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("403 must not look like not-found")
	}
}

func TestS3MaterializeCleansUp(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s := newS3(t, srv, "")
	ctx := context.Background()
	if _, err := s.Write(ctx, "doc.txt", bytes.NewReader([]byte("extract me")), -1, ""); err != nil {
		t.Fatal(err)
	}
	p, cleanup, err := s.Materialize(ctx, "doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "extract me" {
		t.Fatalf("temp copy: %q", b)
	}
	cleanup()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("temporary copy must be removed")
	}
	if _, _, err := s.Materialize(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("materialize missing: %v", err)
	}
}

func TestS3AmbientCredentials(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, err := NewS3(S3Options{Endpoint: srv.URL, Bucket: "b", PathStyle: true, Getenv: func(k string) string {
		return map[string]string{"AWS_ACCESS_KEY_ID": "ENVID", "AWS_SECRET_ACCESS_KEY": "envsecret", "AWS_SESSION_TOKEN": "tok"}[k]
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(context.Background(), "k", strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.auth[0], "Credential=ENVID/") || !strings.Contains(fake.auth[0], "x-amz-security-token") {
		t.Fatalf("ambient credentials not used: %q", fake.auth[0])
	}
	if !strings.Contains(fake.auth[0], "/auto/s3/") {
		t.Fatalf("blank region must default to auto: %q", fake.auth[0])
	}
}

func TestS3RequiresBucket(t *testing.T) {
	if _, err := NewS3(S3Options{Region: "r"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v", err)
	}
}

func TestSigningKeyVector(t *testing.T) {
	// Published example from the Signature V4 documentation.
	k := signingKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20150830", "us-east-1", "iam")
	if got := hex.EncodeToString(k); got != "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9" {
		t.Fatalf("signing key %s", got)
	}
}

// Scenario 21: startup validation of the storage section.
func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name string
		c    config.StorageConfig
		ok   bool
	}{
		{"blank", config.StorageConfig{}, true},
		{"local", config.StorageConfig{Backend: "local"}, true},
		{"s3 ok", config.StorageConfig{Backend: "s3", S3: config.S3Config{Bucket: "b", Region: "r"}}, true},
		{"s3 no bucket", config.StorageConfig{Backend: "s3", S3: config.S3Config{Region: "r"}}, false},
		{"s3 no region", config.StorageConfig{Backend: "s3", S3: config.S3Config{Bucket: "b"}}, false},
		{"gcs", config.StorageConfig{Backend: "gcs"}, false},
	}
	for _, tc := range cases {
		err := ValidateConfig(tc.c)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalidConfig)) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// Scenario 26: the next operation follows a configuration change.
func TestManagerFollowsConfig(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	cur := config.StorageConfig{Backend: "local", Local: config.LocalStorageConfig{RootDir: dirA}}
	var mu sync.Mutex
	m := NewManager(func() config.StorageConfig { mu.Lock(); defer mu.Unlock(); return cur }, nil, nil)
	ctx := context.Background()

	if _, err := m.Write(ctx, "k.txt", strings.NewReader("one"), 3, ""); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Current()
	again, _ := m.Current()
	if first != again {
		t.Fatal("backend instance must be cached while the config is unchanged")
	}
	if _, err := os.Stat(filepath.Join(dirA, "k.txt")); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	cur.Local.RootDir = dirB
	mu.Unlock()
	if _, err := m.Open(ctx, "k.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new root must be used, got %v", err)
	}
	if _, err := m.Write(ctx, "k.txt", strings.NewReader("two"), 3, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "k.txt")); err != nil {
		t.Fatal(err)
	}

	// Unknown backend values fall back to local at run time.
	mu.Lock()
	cur.Backend = "gcs"
	mu.Unlock()
	if st, err := m.Current(); err != nil {
		t.Fatal(err)
	} else if _, ok := st.(*Local); !ok {
		t.Fatalf("unknown backend must fall back to local, got %T", st)
	}

	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	mu.Lock()
	cur = config.StorageConfig{Backend: "s3", S3: config.S3Config{Endpoint: srv.URL, Region: "r", Bucket: "b", Prefix: "p", ForcePathStyle: true, AccessKeyID: "a", SecretAccessKey: "s"}}
	mu.Unlock()
	if _, err := m.Write(ctx, "k.txt", strings.NewReader("three"), 5, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.objects["/b/p/k.txt"]; !ok {
		t.Fatalf("s3 backend not selected: %v", fake.paths)
	}
}

func TestReadAllLimit(t *testing.T) {
	l := NewLocal(t.TempDir())
	ctx := context.Background()
	_, _ = l.Write(ctx, "a", strings.NewReader("0123456789"), 10, "")
	b, _, err := ReadAll(ctx, l, "a", 4)
	if err != nil || string(b) != "0123" {
		t.Fatalf("%q %v", b, err)
	}
}
