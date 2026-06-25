package memory

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	embedTimeout = 20 * time.Second
	cacheTTL     = 3 * time.Minute
	sweepEvery   = time.Minute
)

// Embedder turns text into a vector. It is implemented by the model-provider
// layer and injected by the integrator; memory works without one.
type Embedder interface {
	// Enabled reports whether embeddings are switched on in configuration.
	Enabled() bool
	// Embed returns the vector for text and a signature naming the model and
	// dimension that produced it.
	Embed(ctx context.Context, text string) (vector []float32, signature string, err error)
}

// Detacher runs follow-up work that outlives the request. deps.Deps.Lifecycle
// satisfies it through DetachFunc.
type Detacher func(ctx context.Context, name string, timeout time.Duration, fn func(ctx context.Context))

// Options configure a Service. Every field is optional.
type Options struct {
	// Embedder enables vector generation and semantic search.
	Embedder Embedder
	// Detach runs background embedding. When nil the work runs inline.
	Detach Detacher
	// OnChange is called with the user id after a write or delete, once the
	// built-in chat cache has been invalidated.
	OnChange func(userID uint)
	// Logger receives operational events; nil discards them.
	Logger *logx.Logger
}

// Service is the entry point other areas use.
type Service struct {
	store  *store
	clk    clock.Clock
	opts   Options
	log    *logx.Logger
	cache  *chatCache
	embed  Embedder
	detach Detacher
}

// NewService builds a service over an already migrated database.
func NewService(db *gorm.DB, clk clock.Clock, opts Options) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	log := opts.Logger
	if log == nil {
		log = logx.Nop()
	}
	s := &Service{store: &store{db: db}, clk: clk, opts: opts, log: log, embed: opts.Embedder, detach: opts.Detach}
	s.cache = newChatCache(clk, cacheTTL)
	return s
}

// List returns every live entry of the user, most recently updated first. The
// result is never nil.
func (s *Service) List(ctx context.Context, userID uint) ([]Entry, error) {
	return s.store.list(ctx, userID)
}

// ListCached is List through the per-user in-process cache the chat pipeline
// uses. Writes made through this service invalidate it immediately.
func (s *Service) ListCached(ctx context.Context, userID uint) ([]Entry, error) {
	return s.cache.get(userID, func() ([]Entry, error) { return s.store.list(ctx, userID) })
}

// SweepCache drops expired cache entries; the mounted worker calls it every
// minute.
func (s *Service) SweepCache() int { return s.cache.sweep() }

// InvalidateCache forgets the cached list of the user.
func (s *Service) InvalidateCache(userID uint) { s.cache.invalidate(userID) }

// Upsert stores one entry for the user under updatedBy and returns it. Inputs
// are trimmed. A new key beyond the cap fails with ErrLimitExceeded; updating an
// existing key never does. After success the cache is invalidated and, when an
// embedder is enabled, a vector is generated in the background.
func (s *Service) Upsert(ctx context.Context, userID uint, key, value, scope, updatedBy string) (Entry, error) {
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	scope, updatedBy = clip(strings.TrimSpace(scope), maxScopeChars), clip(strings.TrimSpace(updatedBy), maxUpdatedByChars)
	if key == "" || utf8.RuneCountInString(key) > MaxKeyChars {
		return Entry{}, ErrInvalidInput
	}
	e, err := s.store.upsert(ctx, s.clk.Now(), userID, key, value, scope, updatedBy)
	if err != nil {
		return Entry{}, err
	}
	s.changed(userID)
	s.indexAsync(ctx, userID, key, value)
	return e, nil
}

// Delete removes the user's entry with that key (after trimming). Deleting a
// missing entry succeeds.
func (s *Service) Delete(ctx context.Context, userID uint, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrKeyRequired
	}
	if err := s.store.remove(ctx, userID, key); err != nil {
		return err
	}
	s.changed(userID)
	return nil
}

// StoreVector attaches a vector to the entry (userID, key). It is a successful
// no-op for an empty vector or blank signature, and when the entry is gone or,
// with a non-empty expectedValue, no longer holds exactly that value.
func (s *Service) StoreVector(ctx context.Context, userID uint, key, expectedValue string, vector []float32, signature string) error {
	signature = strings.TrimSpace(signature)
	if len(vector) == 0 || signature == "" || !finiteVector(vector) {
		return nil
	}
	return s.store.setVector(ctx, userID, strings.TrimSpace(key), expectedValue, vector, clip(signature, maxSignatureChars))
}
