// Package objectstore is the one place binary and text artifacts are kept:
// chat files, isolated moderation evidence and derived artifacts. It offers a
// four-operation contract over string keys with two interchangeable backends,
// the local disk and any S3-compatible service.
//
// The package is a library, not a routed area: it declares no routes and no
// tables. The integrator builds a Manager from the application configuration
// and hands it to the areas that need to store bytes.
package objectstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// Errors reported by every backend.
var (
	// ErrNotFound means the key holds no object. It is distinct from every
	// other failure so callers can map it to their own "file not found".
	ErrNotFound = errors.New("objectstore: object not found")
	// ErrInvalidKey means the key is empty or tries to leave the store.
	ErrInvalidKey = errors.New("objectstore: invalid key")
	// ErrInvalidConfig means the storage configuration cannot be used.
	ErrInvalidConfig = errors.New("objectstore: invalid storage configuration")
)

// Info describes a stored object.
type Info struct {
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
}

// Object is an opened object. The caller closes Body.
type Object struct {
	Body io.ReadCloser
	// Size is the content length, or -1 when the backend does not know it.
	Size int64
	// ContentType is empty when the backend does not know it (the local
	// backend never does; callers derive the type from their own records).
	ContentType string
	ModTime     time.Time
}

// Store is the backend-independent contract.
type Store interface {
	// Write stores r under key. size is the exact length when known (negative
	// when unknown) and contentType may be empty. A failed write leaves nothing
	// visible under the key.
	Write(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error)
	// Open streams the object. A missing key yields ErrNotFound.
	Open(ctx context.Context, key string) (*Object, error)
	// Delete removes the object. Removing a missing key succeeds.
	Delete(ctx context.Context, key string) error
	// Materialize returns a local filesystem path holding the content, for tools
	// that need a path, and a cleanup function the caller must run.
	Materialize(ctx context.Context, key string) (path string, cleanup func(), err error)
}

// NormalizeKey applies the key rules shared by every backend: surrounding
// whitespace is removed, backslashes become forward slashes and leading and
// trailing slashes are dropped. Empty keys, parent-directory segments and
// drive-letter prefixes are invalid.
func NormalizeKey(key string) (string, error) {
	k := strings.TrimSpace(key)
	k = strings.ReplaceAll(k, "\\", "/")
	k = strings.Trim(k, "/")
	if k == "" {
		return "", ErrInvalidKey
	}
	if strings.ContainsRune(k, 0) {
		return "", ErrInvalidKey
	}
	for _, seg := range strings.Split(k, "/") {
		if seg == ".." {
			return "", ErrInvalidKey
		}
	}
	if len(k) >= 2 && k[1] == ':' {
		c := k[0] | 0x20
		if c >= 'a' && c <= 'z' {
			return "", ErrInvalidKey
		}
	}
	return k, nil
}

// ReadAll opens key and reads at most limit bytes (limit <= 0 means no limit).
// Reading beyond the limit is not an error: the result is truncated to limit.
func ReadAll(ctx context.Context, s Store, key string, limit int64) ([]byte, string, error) {
	obj, err := s.Open(ctx, key)
	if err != nil {
		return nil, "", err
	}
	defer obj.Body.Close()
	var r io.Reader = obj.Body
	if limit > 0 {
		r = io.LimitReader(obj.Body, limit)
	}
	b, err := io.ReadAll(r)
	return b, obj.ContentType, err
}
