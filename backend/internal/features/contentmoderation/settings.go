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

// read loads the configuration from the database, applying defaults for absent
// or unusable values. An undecryptable key or an invalid stored policy is an
// error.
func (s *configStore) read(ctx context.Context) (Config, error) {
	var rows []settingRow
	if err := s.db.WithContext(ctx).Where("namespace = ?", settingsNamespace).Find(&rows).Error; err != nil {
		return Config{}, database.Translate(err)
	}
	kv := make(map[string]string, len(rows))
	for _, r := range rows {
		kv[r.Key] = r.Value
	}
	cfg := defaultConfig()
	if v, ok := kv[keyEnabled]; ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		cfg.Enabled = err == nil && b
	}
	if v := strings.TrimSpace(kv[keyBaseURL]); v != "" {
		cfg.BaseURL = v
	}
	if v := strings.TrimSpace(kv[keyModel]); v != "" {
		cfg.Model = v
	}
	cfg.TimeoutSeconds = positiveInt(kv[keyTimeoutSeconds], cfg.TimeoutSeconds)
	cfg.MaxConcurrency = positiveInt(kv[keyMaxConcurrency], cfg.MaxConcurrency)
	cfg.QueueCapacity = positiveInt(kv[keyQueueCapacity], cfg.QueueCapacity)

	if enc := strings.TrimSpace(kv[keyAPIKey]); enc != "" {
		plain, _, err := s.secrets.Decrypt(enc)
		if err != nil {
			return Config{}, fmt.Errorf("content moderation: decrypt api key: %w", err)
		}
		cfg.APIKey = strings.TrimSpace(plain)
	}

	policy, err := parseStoredPolicy(kv[keyPolicyJSON], kv[keyPolicyVersion])
	if err != nil {
		return Config{}, err
	}
	cfg.Policy = policy
	return cfg, nil
}

func positiveInt(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return def
	}
	return n
}

// parseStoredPolicy reads policy_json (which carries the version) and falls
// back to the separate policy_version row when the JSON has none.
func parseStoredPolicy(policyJSON, versionRaw string) (Policy, error) {
	var p Policy
	if strings.TrimSpace(policyJSON) != "" {
		if err := json.Unmarshal([]byte(policyJSON), &p); err != nil {
			return Policy{}, fmt.Errorf("content moderation: stored policy is unreadable: %w", err)
		}
		norm, err := p.normalize()
		if err != nil {
			return Policy{}, fmt.Errorf("content moderation: stored policy is invalid: %w", err)
		}
		norm.Version = p.Version
		p = norm
	}
	if p.Version == 0 {
		if v, err := strconv.ParseInt(strings.TrimSpace(versionRaw), 10, 64); err == nil && v > 0 {
			p.Version = v
		}
	}
	return p.nonNil(), nil
}

type settingValue struct {
	key, value, valueType, description string
}

// write stores all nine keys in one transaction. Existing rows keep their type
// and description; only value and updated_at change.
func (s *configStore) write(ctx context.Context, cfg Config) error {
	encKey := ""
	if cfg.APIKey != "" {
		enc, err := s.secrets.Encrypt(cfg.APIKey)
		if err != nil {
			return fmt.Errorf("content moderation: encrypt api key: %w", err)
		}
		encKey = enc
	}
	policy, err := json.Marshal(cfg.Policy.nonNil())
	if err != nil {
		return err
	}
	values := []settingValue{
		{keyEnabled, strconv.FormatBool(cfg.Enabled), "bool", "Whether content moderation is enabled"},
		{keyBaseURL, cfg.BaseURL, "string", "Moderation service base URL"},
		{keyAPIKey, encKey, "string", "Moderation service API key (encrypted)"},
		{keyModel, cfg.Model, "string", "Moderation model"},
		{keyTimeoutSeconds, strconv.Itoa(cfg.TimeoutSeconds), "int", "Per-check timeout in seconds"},
		{keyMaxConcurrency, strconv.Itoa(cfg.MaxConcurrency), "int", "Maximum concurrent checks"},
		{keyQueueCapacity, strconv.Itoa(cfg.QueueCapacity), "int", "Maximum waiting checks"},
		{keyPolicyJSON, string(policy), "json", "Moderation category policy"},
		{keyPolicyVersion, strconv.FormatInt(cfg.Policy.Version, 10), "int", "Moderation policy version"},
	}
	now := s.clock.Now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, v := range values {
			row := settingRow{
				Namespace: settingsNamespace, Key: v.key, Value: v.value,
				ValueType: v.valueType, Description: v.description,
				CreatedAt: now, UpdatedAt: now,
			}
			res := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "namespace"}, {Name: "key"}},
				DoUpdates: clause.Assignments(map[string]any{"value": v.value, "updated_at": now}),
			}).Create(&row)
			if res.Error != nil {
				return database.Translate(res.Error)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// reencrypt re-encrypts the stored API key with the active key. It reports
// whether a row changed.
func (s *configStore) reencrypt(ctx context.Context) (bool, error) {
	var row settingRow
	err := s.db.WithContext(ctx).Where(&settingRow{Namespace: settingsNamespace, Key: keyAPIKey}).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(row.Value) == "" {
		return false, nil
	}
	next, changed, err := s.secrets.Reencrypt(row.Value)
	if err != nil || !changed {
		return false, err
	}
	err = s.db.WithContext(ctx).Model(&settingRow{}).Where("id = ?", row.ID).
		Updates(map[string]any{"value": next, "updated_at": s.clock.Now()}).Error
	if err != nil {
		return false, err
	}
	s.invalidate()
	return true, nil
}
