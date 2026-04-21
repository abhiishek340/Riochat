// Package objectstore stores binary content behind one small interface with a
// local-directory backend and an S3-compatible backend.
package objectstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// Errors returned by every backend.
var (
	ErrInvalidKey = errors.New("invalid object key")
	ErrNotFound   = errors.New("object not found")
)

// Info describes a stored object.
type Info struct {
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
}

// Store is the object-store port.
type Store interface {
	// Put stores r under key. size is the declared length, or negative when
	// unknown; the returned Info carries the number of bytes written.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error)
	// Open returns a stream for key; the caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, Info, error)
	// Delete removes key. A missing object, or an invalid key, is not an error.
	Delete(ctx context.Context, key string) error
	// Materialize returns a local file path holding the object and a function
	// that releases it, for tools that need a real file.
	Materialize(ctx context.Context, key string) (path string, cleanup func(), err error)
}

// NormalizeKey trims whitespace, converts backslashes to slashes and trims
// leading and trailing slashes. An empty result is ErrInvalidKey.
func NormalizeKey(key string) (string, error) {
	k := strings.TrimSpace(key)
	k = strings.ReplaceAll(k, "\\", "/")
	k = strings.Trim(k, "/")
	if k == "" {
		return "", ErrInvalidKey
	}
	return k, nil
}
