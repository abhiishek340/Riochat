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

// Search returns the user's entries closest to the query vector, best first, at
// most topK, each with cosine similarity at least minSimilarity. Only vectors
// stored under the same signature are considered. Timestamps are not filled.
func (s *Service) Search(ctx context.Context, userID uint, query []float32, signature string, topK int, minSimilarity float64) ([]Entry, error) {
	signature = strings.TrimSpace(signature)
	if len(query) == 0 || signature == "" || topK <= 0 || !finiteVector(query) {
		return []Entry{}, nil
	}
	out, err := s.store.search(ctx, userID, query, clip(signature, maxSignatureChars), topK, minSimilarity)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Entry{}
	}
	return out, nil
}

// PurgeUser hard-deletes all memory rows and vectors of a user, including
// soft-deleted rows. Use it when the account is permanently removed.
func (s *Service) PurgeUser(ctx context.Context, userID uint) error {
	return s.PurgeUserTx(s.store.db.WithContext(ctx), userID)
}

// PurgeUserTx is PurgeUser inside a caller-owned transaction.
func (s *Service) PurgeUserTx(tx *gorm.DB, userID uint) error {
	if err := s.store.purgeUser(tx, userID); err != nil {
		return err
	}
	s.cache.invalidate(userID)
	return nil
}

func (s *Service) changed(userID uint) {
	s.cache.invalidate(userID)
	if s.opts.OnChange != nil {
		s.opts.OnChange(userID)
	}
}

// indexAsync generates the vector for a freshly written entry. Failures are
// swallowed: the entry stays valid as text and recall falls back to keywords.
func (s *Service) indexAsync(ctx context.Context, userID uint, key, value string) {
	if s.embed == nil || !s.embed.Enabled() || key == "" || value == "" {
		return
	}
	work := func(bg context.Context) {
		bg, cancel := context.WithTimeout(context.WithoutCancel(bg), embedTimeout)
		defer cancel()
		vec, sig, err := s.embed.Embed(bg, value)
		if err != nil || len(vec) == 0 {
			s.log.Debug("memory_embed_skipped", logx.F("user_id", userID))
			return
		}
		if err := s.StoreVector(bg, userID, key, value, vec, sig); err != nil {
			s.log.Debug("memory_vector_store_failed", logx.F("user_id", userID))
		}
	}
	if s.detach != nil {
		s.detach(ctx, "memory-embed", embedTimeout, work)
		return
	}
	// Without a lifecycle-managed detacher the work still must not delay the
	// caller or die with the request.
	go work(context.WithoutCancel(ctx))
}

// clip cuts s to at most n characters.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

type cacheItem struct {
	entries []Entry
	expires time.Time
}

// chatCache keeps one list per user for a few minutes. A generation counter,
// bumped by every invalidation, stops a load that raced with a write from
// storing stale data.
type chatCache struct {
	mu    sync.Mutex
	clk   clock.Clock
	ttl   time.Duration
	items map[uint]cacheItem
	gen   uint64
}

func newChatCache(clk clock.Clock, ttl time.Duration) *chatCache {
	return &chatCache{clk: clk, ttl: ttl, items: map[uint]cacheItem{}}
}

func (c *chatCache) get(userID uint, load func() ([]Entry, error)) ([]Entry, error) {
	now := c.clk.Now()
	c.mu.Lock()
	if it, ok := c.items[userID]; ok && now.Before(it.expires) {
		out := append([]Entry(nil), it.entries...)
		c.mu.Unlock()
		return out, nil
	}
	gen := c.gen
	c.mu.Unlock()

	entries, err := load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.gen == gen {
		c.items[userID] = cacheItem{entries: append([]Entry(nil), entries...), expires: now.Add(c.ttl)}
	}
	c.mu.Unlock()
	return entries, nil
}

func (c *chatCache) invalidate(userID uint) {
	c.mu.Lock()
	delete(c.items, userID)
	c.gen++
	c.mu.Unlock()
}

func (c *chatCache) sweep() int {
	now := c.clk.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for id, it := range c.items {
		if !now.Before(it.expires) {
			delete(c.items, id)
			n++
		}
	}
	return n
}
