package objectstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRoot is used when the configured root is blank.
const DefaultRoot = "./storage"

// Local keeps objects as files under a root folder.
type Local struct{ root string }

// NewLocal returns a local store rooted at root (DefaultRoot when blank).
func NewLocal(root string) *Local {
	if strings.TrimSpace(root) == "" {
		root = DefaultRoot
	}
	return &Local{root: root}
}

// Root returns the folder objects live under.
func (l *Local) Root() string { return l.root }

// resolve maps a key to a path that is guaranteed to stay under the root.
func (l *Local) resolve(key string) (string, string, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return "", "", err
	}
	rel := filepath.FromSlash(k)
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", "", ErrInvalidKey
	}
	abs := filepath.Join(l.root, rel)
	back, err := filepath.Rel(l.root, abs)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", "", ErrInvalidKey
	}
	return abs, k, nil
}

// Write stores the stream through a temporary file in the destination folder
// and renames it into place, so readers never observe partial content.
func (l *Local) Write(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error) {
	abs, k, err := l.resolve(key)
	if err != nil {
		return Info{}, err
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return Info{}, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return Info{}, err
	}
	if size >= 0 && n != size {
		return Info{}, io.ErrUnexpectedEOF
	}
	if err := tmp.Close(); err != nil {
		return Info{}, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return Info{}, err
	}
	committed = true
	return Info{Key: k, Size: n, ContentType: contentType, ModTime: time.Now()}, nil
}

// Open streams a file. The reported content type is always empty.
func (l *Local) Open(ctx context.Context, key string) (*Object, error) {
	abs, _, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.IsDir() {
		_ = f.Close()
		return nil, ErrNotFound
	}
	return &Object{Body: f, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete removes a file. A missing key and an invalid key both succeed.
func (l *Local) Delete(ctx context.Context, key string) error {
	abs, _, err := l.resolve(key)
	if err != nil {
		return nil
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Materialize returns the real path with a no-op cleanup.
func (l *Local) Materialize(ctx context.Context, key string) (string, func(), error) {
	abs, _, err := l.resolve(key)
	if err != nil {
		return "", nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, ErrNotFound
		}
		return "", nil, err
	}
	if st.IsDir() {
		return "", nil, ErrNotFound
	}
	return abs, func() {}, nil
}

// ctxReader stops a copy when the context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
