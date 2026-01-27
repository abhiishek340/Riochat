package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// settingsCacheTTL is the lifetime of version and value entries.
const settingsCacheTTL = 10 * time.Minute

// SettingsCache serves single user settings to hot paths (the chat runtime).
//
// Every (user, key) pair has a version entry holding a random token, and one
// value entry per token. A write replaces the token and stores the committed
// value under the new one, so a slow reader that still holds the old token can
// only ever fill an entry nobody reads any more.
//
// A read of a key that was never stored yields "" (not the catalog default);
// callers treat "" as unset.
type SettingsCache struct {
	db  *gorm.DB
	kv  cache.KV // nil means no cache: every read goes to the database
	log *logx.Logger
}

// NewSettingsCache builds the cache. kv may be nil.
func NewSettingsCache(db *gorm.DB, kv cache.KV, log *logx.Logger) *SettingsCache {
	return &SettingsCache{db: db, kv: kv, log: log}
}

// The braces are a cluster hash tag: all keys of one user share a slot.
func versionKey(userID uint, key string) string {
	return fmt.Sprintf("user:setting:ver:{%d}:%s", userID, key)
}

func valueKey(userID uint, key, token string) string {
	return fmt.Sprintf("user:setting:val:{%d}:%s:%s", userID, key, token)
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (c *SettingsCache) warn(event string, userID uint, key string, err error) {
	if c.log != nil {
		c.log.Warn(event, logx.F("user_id", userID), logx.F("key", key), logx.F("error", err))
	}
}

// version returns the current token of (user, key), creating one when absent.
// Creation is atomic (SetNX), so concurrent first readers agree on one token.
func (c *SettingsCache) version(ctx context.Context, userID uint, key string) (string, error) {
	vk := versionKey(userID, key)
	raw, found, err := c.kv.Get(ctx, vk)
	if err != nil {
		return "", err
	}
	if found {
		return string(raw), nil
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	created, err := c.kv.SetNX(ctx, vk, []byte(token), settingsCacheTTL)
	if err != nil {
		return "", err
	}
	if created {
		return token, nil
	}
	raw, found, err = c.kv.Get(ctx, vk)
	if err != nil {
		return "", err
	}
	if !found {
		return token, nil
	}
	return string(raw), nil
}

// Get returns the stored value of one setting, "" when no row exists. Cache
// problems fall back to the database and never surface as errors; only a
// database failure does.
func (c *SettingsCache) Get(ctx context.Context, userID uint, key string) (string, error) {
	if c == nil || c.kv == nil {
		return c.fromDB(ctx, userID, key)
	}
	token, err := c.version(ctx, userID, key)
	if err != nil {
		c.warn("user_setting_cache_version_failed", userID, key, err)
		return c.fromDB(ctx, userID, key)
	}
	vk := valueKey(userID, key, token)
	raw, found, err := c.kv.Get(ctx, vk)
	if err != nil {
		c.warn("user_setting_cache_read_failed", userID, key, err)
	} else if found {
		return string(raw), nil
	}
	val, err := c.fromDB(ctx, userID, key)
	if err != nil {
		return "", err
	}
	if err := c.kv.Set(ctx, vk, []byte(val), settingsCacheTTL); err != nil {
		c.warn("user_setting_cache_fill_failed", userID, key, err)
	}
	return val, nil
}

// UserSetting adapts Get to the reader shape the conversation area expects: ok
// is false while the setting is unset.
func (c *SettingsCache) UserSetting(ctx context.Context, userID uint, key string) (string, bool, error) {
	v, err := c.Get(ctx, userID, key)
	if err != nil {
		return "", false, err
	}
	return v, v != "", nil
}

func (c *SettingsCache) fromDB(ctx context.Context, userID uint, key string) (string, error) {
	var row Setting
	err := c.db.WithContext(ctx).Where(map[string]any{"user_id": userID, "key": key}).Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return row.Value, nil
}

// Refresh makes the committed database values of keys the ones the cache
// serves. Call it after the write commits. Failures are logged only.
func (c *SettingsCache) Refresh(ctx context.Context, userID uint, keys []string) {
	if c == nil || c.kv == nil || len(keys) == 0 {
		return
	}
	tokens := make(map[string]string, len(keys))
	advanced := make([]string, 0, len(keys))
	for _, key := range keys {
		token, err := newToken()
		if err == nil {
			err = c.kv.Set(ctx, versionKey(userID, key), []byte(token), settingsCacheTTL)
		}
		if err != nil {
			c.warn("user_setting_cache_advance_failed", userID, key, err)
			continue
		}
		tokens[key] = token
		advanced = append(advanced, key)
	}
	if len(advanced) == 0 {
		return
	}
	var rows []Setting
	if err := c.db.WithContext(ctx).Where(map[string]any{"user_id": userID, "key": advanced}).Find(&rows).Error; err != nil {
		c.warn("user_setting_cache_load_failed", userID, "", err)
		return
	}
	committed := make(map[string]string, len(rows))
	for _, r := range rows {
		committed[r.Key] = r.Value
	}
	for _, key := range advanced {
		if err := c.kv.Set(ctx, valueKey(userID, key, tokens[key]), []byte(committed[key]), settingsCacheTTL); err != nil {
			c.warn("user_setting_cache_store_failed", userID, key, err)
		}
	}
}
