package user

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// WriteHook is told which keys of which user were written, after the commit and
// after the cache refresh. It must not fail the request.
type WriteHook func(ctx context.Context, userID uint, keys []string)

// SettingsStore reads and writes per-user preferences.
type SettingsStore struct {
	db    *gorm.DB
	clock clock.Clock
	cache *SettingsCache
	hook  WriteHook
	log   *logx.Logger
}

// NewSettingsStore builds the store. cache and hook may be nil.
func NewSettingsStore(db *gorm.DB, clk clock.Clock, cache *SettingsCache, hook WriteHook, log *logx.Logger) *SettingsStore {
	return &SettingsStore{db: db, clock: clk, cache: cache, hook: hook, log: log}
}

// All returns every supported key with the stored value or the default. Stored
// rows for keys outside the catalog are ignored.
func (s *SettingsStore) All(ctx context.Context, userID uint) (map[string]string, error) {
	var rows []Setting
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := SettingDefaults()
	for _, r := range rows {
		if _, known := settingIndex[r.Key]; known {
			out[r.Key] = r.Value
		}
	}
	return out, nil
}

// Patch validates the whole payload, then writes all entries in one atomic
// upsert, refreshes the cache and returns the resulting full map.
func (s *SettingsStore) Patch(ctx context.Context, userID uint, raw map[string]string) (map[string]string, error) {
	entries, err := validatePatch(raw)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		now := s.clock.Now()
		rows := make([]Setting, 0, len(entries))
		keys := make([]string, 0, len(entries))
		for _, key := range SettingKeys() {
			if val, ok := entries[key]; ok {
				rows = append(rows, Setting{UserID: userID, Key: key, Value: val, UpdatedAt: now})
				keys = append(keys, key)
			}
		}
		err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&rows).Error
		if err != nil {
			return nil, database.Translate(err)
		}
		// The write is committed; a dropped request must not leave the cache stale.
		post := context.WithoutCancel(ctx)
		s.cache.Refresh(post, userID, keys)
		if s.hook != nil {
			s.hook(post, userID, keys)
		}
	}
	return s.All(ctx, userID)
}

// DeleteAll permanently removes every setting row of a user.
func (s *SettingsStore) DeleteAll(ctx context.Context, userID uint) error {
	return s.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&Setting{}).Error
}
