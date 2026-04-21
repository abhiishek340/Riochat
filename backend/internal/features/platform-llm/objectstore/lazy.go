package objectstore

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

// Settings is the comparable subset of storage configuration that selects and
// parameterises a backend.
type Settings struct {
	Backend         string
	LocalRoot       string
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	PathStyle       bool
}

// SettingsFrom converts the platform storage configuration.
func SettingsFrom(c config.StorageConfig) Settings {
	return Settings{
		Backend: c.Backend, LocalRoot: c.Local.RootDir,
		Endpoint: c.S3.Endpoint, Region: c.S3.Region, Bucket: c.S3.Bucket, Prefix: c.S3.Prefix,
		AccessKeyID: c.S3.AccessKeyID, SecretAccessKey: c.S3.SecretAccessKey, PathStyle: c.S3.ForcePathStyle,
	}
}

// New builds the backend the settings select: "s3" selects S3, anything else
// (including unknown values) selects the local directory.
func New(s Settings, tracer *trace.Tracer) (Store, error) {
	if strings.EqualFold(strings.TrimSpace(s.Backend), config.StorageS3) {
		return NewS3(S3Options{
			Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket, Prefix: s.Prefix,
			AccessKeyID: s.AccessKeyID, SecretAccessKey: s.SecretAccessKey, PathStyle: s.PathStyle,
			Tracer: tracer,
		})
	}
	return NewLocal(s.LocalRoot)
}

// Lazy resolves the backend from live configuration on every use, so a
// configuration change applies to subsequent operations.
type Lazy struct {
	load   func() Settings
	tracer *trace.Tracer

	mu    sync.Mutex
	key   Settings
	store Store
	have  bool
}

// NewLazy returns a Store that follows load().
func NewLazy(load func() Settings, tracer *trace.Tracer) *Lazy {
	return &Lazy{load: load, tracer: tracer}
}

// Current returns the backend for the present configuration.
func (l *Lazy) Current() (Store, error) {
	s := l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.have && l.key == s {
		return l.store, nil
	}
	st, err := New(s, l.tracer)
	if err != nil {
		return nil, err
	}
	l.key, l.store, l.have = s, st, true
	return st, nil
}

// Put implements Store.
func (l *Lazy) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) (Info, error) {
	s, err := l.Current()
	if err != nil {
		return Info{}, err
	}
	return s.Put(ctx, key, r, size, ct)
}

// Open implements Store.
func (l *Lazy) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	s, err := l.Current()
	if err != nil {
		return nil, Info{}, err
	}
	return s.Open(ctx, key)
}

// Delete implements Store.
func (l *Lazy) Delete(ctx context.Context, key string) error {
	s, err := l.Current()
	if err != nil {
		return err
	}
	return s.Delete(ctx, key)
}

// Materialize implements Store.
func (l *Lazy) Materialize(ctx context.Context, key string) (string, func(), error) {
	s, err := l.Current()
	if err != nil {
		return "", nil, err
	}
	return s.Materialize(ctx, key)
}
