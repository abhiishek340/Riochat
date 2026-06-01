// Package ragindex turns stored files into searchable vector indexes and uses
// them to ground chat answers: chunking, embedding through an external
// provider, the per-file index state machine with its durable queue and
// recovery, the administrator index operations, query-time retrieval and the
// evidence and prompt blocks built from the retrieved passages.
package ragindex

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Operating limits.
const (
	maxSlots        = 4                // concurrent indexing runs per process
	defaultWorkers  = 4                // queue workers
	runLimit        = 5 * time.Minute  // one indexing run
	stallSweepAge   = 6 * time.Minute  // run limit plus one minute
	stalledAfter    = 30 * time.Minute // queued/processing without progress
	maxAttempts     = 3                // deliveries before a queued job fails
	retryBackoff    = 5 * time.Second  // per failed attempt
	maxVectorDims   = 4096
	maxOriginalSize = 32 << 20
)

// Service is the indexing and retrieval service. It is safe for concurrent
// use.
type Service struct {
	db    *gorm.DB
	clock clock.Clock
	log   *logx.Logger
	kv    cache.KV
	life  *lifecycle.Manager

	cfg     ConfigSource
	emb     Embedder
	content Content
	shared  SharedFiles
	users   UserLabels

	slots chan struct{}
	wake  chan struct{}

	storeMu    sync.Mutex
	storeKnown bool
	storeOK    bool

	rebuilding atomic.Bool
	rebuildWG  sync.WaitGroup

	// enqueueFn is replaceable in tests to simulate a failing queue.
	enqueueFn func(ctx context.Context, j *Job) error
	// keywords is the keyword search; replaceable in tests.
	keywords func(ctx context.Context, files []uint, sig, query string, limit int) ([]scored, error)
}

// New builds the service from the platform dependencies and the optional
// collaborators.
func New(d deps.Deps, o Options) *Service {
	s := &Service{
		db: d.DB, clock: d.Clock, log: d.Logger, kv: d.Cache, life: d.Lifecycle,
		cfg: o.Config, emb: o.Embedder, content: o.Content, shared: o.Shared, users: o.Users,
		slots: make(chan struct{}, maxSlots), wake: make(chan struct{}, 8),
	}
	if s.cfg == nil {
		s.cfg = ConfigFunc(func(context.Context) Config { return DefaultConfig() })
	}
	s.enqueueFn = s.insertJob
	s.keywords = s.keywordSearch
	return s
}

func (s *Service) config(ctx context.Context) Config { return s.cfg.RAGConfig(ctx) }

func (s *Service) now() time.Time { return s.clock.Now() }

// files is the file_objects table (soft-deleted rows excluded).
func (s *Service) files(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&File{})
}

// Availability describes whether indexing can run now.
type Availability struct {
	OK bool
	// Reason is empty when OK; otherwise one of the Reason* constants.
	Reason string
	// NotConfigured is true when the cause is the administrator configuration
	// (disabled, model or host missing) rather than a missing component.
	NotConfigured bool
}

// Availability checks configuration, provider client and vector store. A store
// check that errors is reported with ReasonStoreError.
func (s *Service) Availability(ctx context.Context) Availability {
	return s.availabilityFor(ctx, s.config(ctx))
}

func (s *Service) availabilityFor(ctx context.Context, cfg Config) Availability {
	if r := cfg.configReason(); r != "" {
		return Availability{Reason: r, NotConfigured: true}
	}
	if s.emb == nil {
		return Availability{Reason: ReasonClientMissing}
	}
	ok, err := s.VectorStoreAvailable(ctx)
	switch {
	case err != nil:
		return Availability{Reason: ReasonStoreError}
	case !ok:
		return Availability{Reason: ReasonStoreUnavailable}
	}
	return Availability{OK: true}
}

// VectorStoreAvailable reports whether the chunk and vector tables exist. A
// definite answer (true or false) is remembered for the life of the process;
// an error is not.
func (s *Service) VectorStoreAvailable(ctx context.Context) (bool, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.storeKnown {
		return s.storeOK, nil
	}
	m := s.db.WithContext(ctx).Migrator()
	ok := m.HasTable(&Chunk{}) && m.HasTable(&ChunkVector{}) && m.HasTable(&File{})
	s.storeKnown, s.storeOK = true, ok
	return ok, nil
}

// notify wakes an idle queue worker.
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) acquire(ctx context.Context) bool {
	select {
	case s.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Service) release() { <-s.slots }
