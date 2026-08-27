package contentmoderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Settings storage: namespace and keys inside system_settings.
const (
	settingsNamespace = "content_moderation"

	keyEnabled        = "enabled"
	keyBaseURL        = "base_url"
	keyAPIKey         = "api_key"
	keyModel          = "model"
	keyTimeoutSeconds = "timeout_seconds"
	keyMaxConcurrency = "max_concurrency"
	keyQueueCapacity  = "queue_capacity"
	keyPolicyJSON     = "policy_json"
	keyPolicyVersion  = "policy_version"
)

// Defaults and bounds of the stored configuration.
const (
	DefaultModel          = defaultModel
	DefaultTimeoutSeconds = 10
	DefaultMaxConcurrency = 4
	DefaultQueueCapacity  = 256

	maxTimeoutSeconds = 60
	maxMaxConcurrency = 64
	maxQueueCapacity  = 4096

	configCacheTTL = 2 * time.Second
)

// Config is the effective moderation configuration.
type Config struct {
	Enabled        bool
	BaseURL        string
	APIKey         string
	Model          string
	TimeoutSeconds int
	MaxConcurrency int
	QueueCapacity  int
	Policy         Policy
}

func defaultConfig() Config {
	return Config{
		BaseURL:        DefaultBaseURL,
		Model:          DefaultModel,
		TimeoutSeconds: DefaultTimeoutSeconds,
		MaxConcurrency: DefaultMaxConcurrency,
		QueueCapacity:  DefaultQueueCapacity,
		Policy:         Policy{}.nonNil(),
	}
}

// Active reports whether runs should be moderated at all.
func (c Config) Active() bool { return c.Enabled && c.Policy.AnySelected() }

// clientConfig derives the provider settings.
func (c Config) clientConfig() ClientConfig {
	t := time.Duration(c.TimeoutSeconds) * time.Second
	if t <= 0 {
		t = defaultTimeout
	}
	return ClientConfig{BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model, Timeout: t}
}

// maskKey renders an API key for display.
func maskKey(key string) string {
	key = strings.TrimSpace(key)
	n := utf8.RuneCountInString(key)
	switch {
	case n == 0:
		return ""
	case n <= 8:
		return "****"
	}
	r := []rune(key)
	return string(r[:4]) + "..." + string(r[n-4:])
}

// configStore persists the configuration in system_settings and caches reads
// briefly.
type configStore struct {
	db      *gorm.DB
	secrets *secretbox.KeyRing
	clock   clock.Clock

	mu       sync.Mutex
	cached   Config
	cachedAt time.Time
	hasCache bool
}

func (s *configStore) invalidate() {
	s.mu.Lock()
	s.hasCache = false
	s.mu.Unlock()
}

// get returns the configuration, serving from the cache for a short time.
func (s *configStore) get(ctx context.Context) (Config, error) {
	now := s.clock.Now()
	s.mu.Lock()
	if s.hasCache && now.Sub(s.cachedAt) < configCacheTTL {
		c := s.cached
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	c, err := s.read(ctx)
	if err != nil {
		return Config{}, err
	}
	s.mu.Lock()
	s.cached, s.cachedAt, s.hasCache = c, now, true
	s.mu.Unlock()
	return c, nil
}
