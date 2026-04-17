package usersettings

import (
	"context"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Domain errors. Clients see only these stable codes and messages; the reason
// for a rejection is attached as a diagnostic for the server logs.
var (
	ErrUnknownKey   = apperr.Define("user_settings.unknown_key", "unknown setting key")
	ErrInvalidValue = apperr.Define("user_settings.invalid_value", "invalid user setting value")
)

// Notifier is told, after a successful commit, which keys a user changed so the
// chat side can make the committed values the ones it serves. Implementations
// must be safe for concurrent use.
type Notifier interface {
	SettingsChanged(ctx context.Context, userID uint, keys []string) error
}

// Service implements the read and update operations.
type Service struct {
	db       *gorm.DB
	clk      clock.Clock
	log      *logx.Logger
	notifier Notifier
}

// NewService builds a service. log and notifier may be nil.
func NewService(db *gorm.DB, clk clock.Clock, log *logx.Logger, notifier Notifier) *Service {
	if log == nil {
		log = logx.Nop()
	}
	return &Service{db: db, clk: clk, log: log, notifier: notifier}
}

// Get returns every supported key for the user: the stored value when a row
// exists, otherwise the default. Rows with unsupported keys are ignored.
func (s *Service) Get(ctx context.Context, userID uint) (map[string]string, error) {
	return loadAll(ctx, s.db, userID)
}

func loadAll(ctx context.Context, db *gorm.DB, userID uint) (map[string]string, error) {
	var rows []UserSetting
	keyValues := make([]any, len(orderedKeys))
	for i, k := range orderedKeys {
		keyValues[i] = k
	}
	err := db.WithContext(ctx).Where("user_id = ?", userID).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: keyValues}).Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	out := defaults()
	for _, r := range rows {
		if _, ok := catalog[r.Key]; ok {
			out[r.Key] = r.Value
		}
	}
	return out, nil
}

// normalize validates the whole request and returns the trimmed key -> value
// entries to write. Raw keys that differ only in surrounding whitespace are
// collapsed deterministically: the raw spelling that sorts last wins.
func normalize(in map[string]string) (map[string]string, error) {
	raws := make([]string, 0, len(in))
	for k := range in {
		raws = append(raws, k)
	}
	sort.Strings(raws)
	out := make(map[string]string, len(in))
	for _, raw := range raws {
		key := strings.TrimSpace(raw)
		def, ok := catalog[key]
		if !ok {
			return nil, ErrUnknownKey.WithDiagnostic("unsupported key")
		}
		if strings.ContainsRune(in[raw], 0) || !def.valid(in[raw]) {
			return nil, ErrInvalidValue.WithDiagnostic("rejected value for " + key)
		}
		out[key] = in[raw]
	}
	return out, nil
}

// Update validates every entry, writes them together and returns the full
// settings afterwards. Nothing is written when any entry is invalid, and the
// notifier runs only after the write committed.
func (s *Service) Update(ctx context.Context, userID uint, in map[string]string) (map[string]string, error) {
	entries, err := normalize(in)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		now := s.clk.Now()
		keys := make([]string, 0, len(entries))
		rows := make([]UserSetting, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rows = append(rows, UserSetting{UserID: userID, Key: k, Value: entries[k], UpdatedAt: now})
		}
		werr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(&rows).Error
		})
		if werr != nil {
			return nil, database.Translate(werr)
		}
		s.notify(ctx, userID, keys)
	}
	return loadAll(ctx, s.db, userID)
}

func (s *Service) notify(ctx context.Context, userID uint, keys []string) {
	if s.notifier == nil {
		return
	}
	// The write is already committed, so the refresh must not be abandoned
	// just because the caller's request context ended in the meantime.
	if err := s.notifier.SettingsChanged(context.WithoutCancel(ctx), userID, keys); err != nil {
		s.log.Warn("user_settings_cache_refresh_failed", logx.F("user_id", userID), logx.Err(err))
	}
}

// PurgeUser permanently removes every setting row of a user. It is meant to be
// called by the account purge inside its transaction.
func PurgeUser(ctx context.Context, tx *gorm.DB, userID uint) error {
	if err := tx.WithContext(ctx).Where("user_id = ?", userID).Delete(&UserSetting{}).Error; err != nil {
		return database.Translate(err)
	}
	return nil
}

func isClientError(err error) bool {
	return errors.Is(err, ErrUnknownKey) || errors.Is(err, ErrInvalidValue)
}
