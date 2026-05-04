package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/riochat/riochat/backend/internal/platform/config"
)

// ConfigSource yields the current storage settings. *config.Holder satisfies
// it through StorageOf.
type ConfigSource func() config.StorageConfig

// StorageOf adapts a configuration holder.
func StorageOf(h *config.Holder) ConfigSource {
	return func() config.StorageConfig { return h.Load().Storage }
}

// Manager hands out the Store for the current configuration. The backend
// instance is cached and rebuilt whenever any storage setting changes, so a
// configuration change applies without a restart. Manager is itself a Store.
type Manager struct {
	src  ConfigSource
	http *http.Client
	env  func(string) string

	mu    sync.Mutex
	sig   string
	store Store
}

var _ Store = (*Manager)(nil)

// NewManager builds a manager over src. httpClient and getenv may be nil.
func NewManager(src ConfigSource, httpClient *http.Client, getenv func(string) string) *Manager {
	return &Manager{src: src, http: httpClient, env: getenv}
}

// ValidateConfig applies the startup rules: backend local (or blank) or s3,
// and for s3 a bucket and a region.
func ValidateConfig(c config.StorageConfig) error {
	switch strings.ToLower(strings.TrimSpace(c.Backend)) {
	case "", config.StorageLocal:
		return nil
	case config.StorageS3:
		if strings.TrimSpace(c.S3.Bucket) == "" || strings.TrimSpace(c.S3.Region) == "" {
			return fmt.Errorf("%w: s3 needs a bucket and a region", ErrInvalidConfig)
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported backend %q", ErrInvalidConfig, c.Backend)
}

// Current returns the store for the present configuration. An unknown backend
// value falls back to local at run time (it fails ValidateConfig at startup).
func (m *Manager) Current() (Store, error) {
	c := m.src()
	sig := fmt.Sprintf("%q|%q|%+v", strings.ToLower(strings.TrimSpace(c.Backend)), c.Local.RootDir, c.S3)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil && m.sig == sig {
		return m.store, nil
	}
	var st Store
	if strings.ToLower(strings.TrimSpace(c.Backend)) == config.StorageS3 {
		s3, err := NewS3(S3Options{
			Endpoint: c.S3.Endpoint, Region: c.S3.Region, Bucket: c.S3.Bucket, Prefix: c.S3.Prefix,
			AccessKeyID: c.S3.AccessKeyID, SecretAccessKey: c.S3.SecretAccessKey, PathStyle: c.S3.ForcePathStyle,
			HTTP: m.http, Getenv: m.env,
		})
		if err != nil {
			return nil, err
		}
		st = s3
	} else {
		st = NewLocal(c.Local.RootDir)
	}
	m.sig, m.store = sig, st
	return st, nil
}

// Write implements Store.
func (m *Manager) Write(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error) {
	st, err := m.Current()
	if err != nil {
		return Info{}, err
	}
	return st.Write(ctx, key, r, size, contentType)
}

// Open implements Store.
func (m *Manager) Open(ctx context.Context, key string) (*Object, error) {
	st, err := m.Current()
	if err != nil {
		return nil, err
	}
	return st.Open(ctx, key)
}

// Delete implements Store.
func (m *Manager) Delete(ctx context.Context, key string) error {
	st, err := m.Current()
	if err != nil {
		return err
	}
	return st.Delete(ctx, key)
}

// Materialize implements Store.
func (m *Manager) Materialize(ctx context.Context, key string) (string, func(), error) {
	st, err := m.Current()
	if err != nil {
		return "", nil, err
	}
	return st.Materialize(ctx, key)
}
