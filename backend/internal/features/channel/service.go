package channel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Options carries the collaborators that live outside this area. Every field
// is optional; features that need a missing collaborator degrade as the spec
// describes (for example no ModelClient means remote discovery answers 502).
type Options struct {
	// Client is the protocol-adapter layer used for model discovery and probes.
	Client ModelClient
	// Pricing supplies public prices (billing area).
	Pricing PricingSource
	// Personal supplies a user's own-provider models (personal-provider area).
	Personal PersonalModelSource
	// Subscriptions resolves the access group of a user's active plan.
	Subscriptions SubscriptionGroupSource
	// IconUsage counts icon references held by other areas.
	IconUsage IconUsageCounter
	// Users counts all users (group listings).
	Users UserCounter
	// Plans counts the billing plans bound to a permission group (billing area).
	Plans PlanReferences
	// Verifier checks that user ids name existing accounts; the default reads
	// the identity users table.
	Verifier UserVerifier
	// IconStore overrides the default local object store for icons.
	IconStore IconStore
	// CatalogFetcher overrides the models.dev downloader.
	CatalogFetcher CatalogFetcher
	// BuiltinCatalog overrides the embedded catalog snapshot (gzip or JSON).
	BuiltinCatalog []byte
	// BuiltinNativeTools are the provider-native tool definitions shipped with
	// the application (owned by the native-tool area). NativeToolDefinitions
	// returns them first, followed by the ones models declare.
	BuiltinNativeTools []json.RawMessage
	// OnReady receives the service after it is mounted so the integrator can
	// hand it to the areas that depend on it.
	OnReady func(*Service)
}

// Service is the channel area's engine. Other areas use its exported methods.
type Service struct {
	db      *gorm.DB
	cfg     *config.Holder
	log     *logx.Logger
	cache   cache.Cache
	clock   clock.Clock
	secrets *secretbox.KeyRing
	life    *lifecycle.Manager
	opts    Options

	circuits *circuitStore
	catalog  *catalogManager
	public   *publicCache
	store    IconStore

	setMu          sync.Mutex
	breakerCache   breakerDefaults
	breakerLoaded  bool
	breakerExpires time.Time
}

// NewService builds the service from the platform dependencies.
func NewService(d deps.Deps, o Options) *Service {
	s := &Service{
		db: d.DB, cfg: d.Config, log: d.Logger, cache: d.Cache, clock: d.Clock,
		secrets: d.Secrets, life: d.Lifecycle, opts: o,
	}
	s.circuits = newCircuitStore(d.Cache, d.Clock)
	s.public = newPublicCache()
	s.store = o.IconStore
	if s.store == nil {
		if c := d.Config.Load(); c.Storage.Backend == config.StorageLocal && strings.TrimSpace(c.Storage.Local.RootDir) != "" {
			s.store = newDirIconStore(c.Storage.Local.RootDir)
		}
	}
	fetcher := o.CatalogFetcher
	if fetcher == nil {
		fetcher = newHTTPCatalogFetcher()
	}
	s.catalog = newCatalogManager(s, fetcher)
	return s
}

func (s *Service) now() time.Time { return s.clock.Now() }

func (s *Service) tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}

// ---- settings ---------------------------------------------------------------------

// loadSetting reads the raw value of an llm setting.
func (s *Service) loadSetting(ctx context.Context, key string) (string, bool, error) {
	db := s.db.WithContext(ctx)
	if !db.Migrator().HasTable(&settingRow{}) {
		return "", false, nil
	}
	var row settingRow
	err := db.Where("namespace = ? AND \"key\" = ?", settingsNamespace, key).First(&row).Error
	if err != nil {
		if database.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Value, true, nil
}

// breakerSettings returns the cached global breaker parameters. The cache lives
// for 5 seconds (1 second after a read failure, keeping the last good value or
// "disabled" when none was ever loaded).
func (s *Service) breakerSettings(ctx context.Context) breakerDefaults {
	s.setMu.Lock()
	defer s.setMu.Unlock()
	now := s.now()
	if s.breakerLoaded && now.Before(s.breakerExpires) {
		return s.breakerCache
	}
	raw, found, err := s.loadSetting(ctx, "circuit_breaker.defaults")
	if err == nil {
		def := builtinBreakerDefaults
		if found {
			def, err = parseBreakerDefaults(raw)
		}
		if err == nil {
			s.breakerCache, s.breakerLoaded, s.breakerExpires = def, true, now.Add(5*time.Second)
			return def
		}
	}
	s.log.Warn("llm_breaker_settings_unreadable", logx.Err(err))
	s.breakerExpires = now.Add(time.Second)
	if !s.breakerLoaded {
		s.breakerCache = builtinBreakerDefaults
		s.breakerCache.Enabled = false
		s.breakerLoaded = true
	}
	return s.breakerCache
}

// setBreakerCache refreshes the local cache after a successful settings write.
func (s *Service) setBreakerCache(def breakerDefaults) {
	s.setMu.Lock()
	s.breakerCache, s.breakerLoaded, s.breakerExpires = def, true, s.now().Add(5*time.Second)
	s.setMu.Unlock()
}

// BreakerEnabled reports whether the circuit breaker is globally enabled.
func (s *Service) BreakerEnabled(ctx context.Context) bool { return s.breakerSettings(ctx).Enabled }

func (s *Service) classification(ctx context.Context) errorClassification {
	raw, found, err := s.loadSetting(ctx, "circuit_breaker.error_classification")
	if err != nil || !found {
		return defaultClassification
	}
	return parseClassification(raw)
}

func (s *Service) backoffSettings(ctx context.Context) backoffConfig {
	raw, found, err := s.loadSetting(ctx, "rate_limit.defaults")
	if err != nil || !found {
		return defaultBackoff
	}
	return parseBackoff(raw)
}

// contextFallbackTokens reads chat.context_window_fallback_tokens.
func (s *Service) contextFallbackTokens(ctx context.Context) int {
	raw, found, err := s.loadSetting2(ctx, "chat", "context_window_fallback_tokens")
	if err != nil || !found {
		return DefaultContextFallback
	}
	n := 0
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			return DefaultContextFallback
		}
		n = n*10 + int(r-'0')
		if n > maxContextFallback*10 {
			break
		}
	}
	return normalizeFallbackWindow(n)
}

func (s *Service) loadSetting2(ctx context.Context, ns, key string) (string, bool, error) {
	db := s.db.WithContext(ctx)
	if !db.Migrator().HasTable(&settingRow{}) {
		return "", false, nil
	}
	var row settingRow
	err := db.Where("namespace = ? AND \"key\" = ?", ns, key).First(&row).Error
	if err != nil {
		if database.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Value, true, nil
}

// appAttribution is sent to upstreams during model listing and probes.
func (s *Service) appAttribution() (name, webBase string) {
	c := s.cfg.Load()
	return c.App.Name, c.Server.PublicWebBaseURL
}

func isNotFound(err error) bool {
	return database.IsNotFound(err) || errors.Is(err, gorm.ErrRecordNotFound)
}
