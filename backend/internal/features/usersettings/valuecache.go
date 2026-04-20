package usersettings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ValueTTL is how long a cached setting value lives.
const ValueTTL = 10 * time.Minute

// versionTTL outlives the values stored under a token, so a token never
// disappears while values written under it can still be served.
const versionTTL = 2 * ValueTTL

// ValueCache is the shared read-through cache used by chat processing to read
// single settings (for example chat.file_mode). Each (user, key) has a version
// token held in the shared cache; values live under a key that includes the
// token. A write replaces the token, so entries stored under an older token
// become unreachable at once on every instance, and a slow reader that fetched
// the previous database value can only store it under the token it started
// with, never over a newer one.
//
// ValueCache also implements Notifier.
type ValueCache struct {
	db *gorm.DB
	kv cache.KV
}

// NewValueCache builds the cache over the shared KV store.
func NewValueCache(db *gorm.DB, kv cache.KV) *ValueCache {
	return &ValueCache{db: db, kv: kv}
}

func versionKey(userID uint, key string) string {
	return "usersettings:ver:" + strconv.FormatUint(uint64(userID), 10) + ":" + key
}

func valueKey(userID uint, key, token string) string {
	return "usersettings:val:" + strconv.FormatUint(uint64(userID), 10) + ":" + key + ":" + token
}

func newToken() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// SettingsChanged replaces the version token of every written key. It tries
// every key even when one fails and reports the combined failure.
func (c *ValueCache) SettingsChanged(ctx context.Context, userID uint, keys []string) error {
	var errs []error
	for _, k := range keys {
		tok, err := newToken()
		if err == nil {
			err = c.kv.Set(ctx, versionKey(userID, k), []byte(tok), versionTTL)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// token returns the current version token, creating one when absent.
func (c *ValueCache) token(ctx context.Context, userID uint, key string) (string, error) {
	vk := versionKey(userID, key)
	raw, found, err := c.kv.Get(ctx, vk)
	if err != nil {
		return "", err
	}
	if found {
		return string(raw), nil
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if ok, err := c.kv.SetNX(ctx, vk, []byte(tok), versionTTL); err != nil {
		return "", err
	} else if ok {
		return tok, nil
	}
	raw, found, err = c.kv.Get(ctx, vk)
	if err != nil || !found {
		return tok, err
	}
	return string(raw), nil
}

// Value returns the stored value of a setting, empty when the user has no row
// for it. It serves from the cache when possible and falls back to the
// database on a miss or when the cache is unavailable. Unsupported keys are an
// error.
func (c *ValueCache) Value(ctx context.Context, userID uint, key string) (string, error) {
	if _, ok := catalog[key]; !ok {
		return "", ErrUnknownKey
	}
	tok, terr := c.token(ctx, userID, key)
	if terr == nil {
		if raw, found, err := c.kv.Get(ctx, valueKey(userID, key, tok)); err == nil && found {
			return string(raw), nil
		}
	}
	var row UserSetting
	err := c.db.WithContext(ctx).Where("user_id = ?", userID).Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).Take(&row).Error
	val := ""
	switch {
	case err == nil:
		val = row.Value
	case database.IsNotFound(err):
	default:
		return "", database.Translate(err)
	}
	if terr == nil {
		_ = c.kv.Set(ctx, valueKey(userID, key, tok), []byte(val), ValueTTL)
	}
	return val, nil
}
