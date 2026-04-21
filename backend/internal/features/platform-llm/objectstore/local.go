package objectstore

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Local keeps objects as files under a root directory.
type Local struct {
	root string
}

// NewLocal returns a store rooted at dir (made absolute).
func NewLocal(dir string) (*Local, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "./storage"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

// Root returns the absolute root directory.
func (l *Local) Root() string { return l.root }

// resolve maps a key to a path strictly inside the root.
func (l *Local) resolve(key string) (string, string, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return "", "", err
	}
	clean := path.Clean(k)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || filepath.IsAbs(filepath.FromSlash(clean)) {
		return "", "", ErrInvalidKey
	}
	full := filepath.Join(l.root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", ErrInvalidKey
	}
	return full, clean, nil
}

// Put writes atomically: content goes to a temporary file next to the target
// and is renamed into place, so readers never observe a partial object.
func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error) {
	full, clean, err := l.resolve(key)
	if err != nil {
		return Info{}, err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return Info{}, err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return Info{}, err
	}
	if err := tmp.Close(); err != nil {
		return Info{}, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return Info{}, err
	}
	ok = true
	st, err := os.Stat(full)
	mod := st
	info := Info{Key: clean, Size: n, ContentType: contentType}
	if err == nil && mod != nil {
		info.ModTime = mod.ModTime()
	}
	return info, nil
}

// Open returns the file and its size and modification time (no content type).
func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	full, clean, err := l.resolve(key)
	if err != nil {
		return nil, Info{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		_ = f.Close()
		if err == nil {
			err = ErrNotFound
		}
		return nil, Info{}, err
	}
	return f, Info{Key: clean, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete ignores missing files and invalid keys.
func (l *Local) Delete(ctx context.Context, key string) error {
	full, _, err := l.resolve(key)
	if err != nil {
		return nil
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Materialize returns the real path with a no-op cleanup.
func (l *Local) Materialize(ctx context.Context, key string) (string, func(), error) {
	full, _, err := l.resolve(key)
	if err != nil {
		return "", nil, err
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		return "", nil, ErrNotFound
	}
	return full, func() {}, nil
}

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
