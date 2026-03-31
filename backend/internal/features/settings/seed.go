package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// seed brings the table to the state a start requires: duplicate rows are
// collapsed, every registry default that is missing is inserted, obsolete keys
// are removed and untouched historical defaults are moved to the current one.
// Existing administrator values are never overwritten.
func (s *Service) seed(ctx context.Context) error {
	rows, err := s.repo.all(ctx)
	if err != nil {
		return fmt.Errorf("settings: read rows: %w", err)
	}

	// Which keys exist is decided on the whole table, so a second row is never
	// inserted for a key that already has a duplicate.
	present := make(map[string]bool, len(rows))
	for _, row := range rows {
		present[nk(row.Namespace, row.Key)] = true
	}

	if _, losers := collapseRows(rows); len(losers) > 0 {
		s.log.Warn("settings_duplicate_rows",
			logx.F("removed", len(losers)),
			logx.F("hint", "system_settings contained duplicate rows; reindex the whole database"))
		if err := s.repo.removeRows(ctx, losers); err != nil {
			// A failed cleanup must not stop the server from starting.
			s.log.Error("settings_duplicate_cleanup_failed", logx.Err(err))
		}
	}

	now := s.clock.Now()
	var missing []SystemSetting
	for _, d := range reg.list {
		if present[nk(d.ns, d.key)] {
			continue
		}
		value := d.def
		if d.sensitive && strings.TrimSpace(value) != "" {
			enc, err := s.encrypt(value)
			if err != nil {
				return fmt.Errorf("settings: encrypt default %s/%s: %w", d.ns, d.key, err)
			}
			value = enc
		}
		missing = append(missing, SystemSetting{
			Namespace: d.ns, Key: d.key, Value: value, ValueType: string(d.typ),
			Description: d.desc, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := s.repo.insertMissing(ctx, missing); err != nil {
		return fmt.Errorf("settings: insert defaults: %w", err)
	}

	for _, k := range obsoleteKeys {
		if err := s.repo.deleteKey(ctx, k[0], k[1]); err != nil {
			return fmt.Errorf("settings: remove obsolete %s/%s: %w", k[0], k[1], err)
		}
	}
	return s.migrateDefaults(ctx)
}

// historicalMimeSets are earlier default upload type lists: images, text and
// documents only, and the same plus mp4 and webm video.
func historicalMimeSets() []map[string]bool {
	current := splitMime(defaultMimeTypes)
	first := map[string]bool{}
	for t := range current {
		if !strings.HasPrefix(t, "video/") && !strings.HasPrefix(t, "audio/") {
			first[t] = true
		}
	}
	second := map[string]bool{"video/mp4": true, "video/webm": true}
	for t := range first {
		second[t] = true
	}
	return []map[string]bool{first, second}
}

func splitMime(v string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out[p] = true
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// migrateDefaults resets a setting to the current default only when its stored
// value still equals an earlier default. Customised values are kept.
func (s *Service) migrateDefaults(ctx context.Context) error {
	now := s.clock.Now()
	reset := func(ns, key string) error {
		d := reg.lookup(ns, key)
		if err := s.repo.upsertValue(ctx, ns, key, d.def, string(d.typ), d.desc, now); err != nil {
			return fmt.Errorf("settings: reset %s/%s: %w", ns, key, err)
		}
		return nil
	}

	stored, err := s.rawStored(ctx, "file", "allowed_mime_types")
	if err != nil {
		return err
	}
	if strings.TrimSpace(stored) != "" {
		set := splitMime(stored)
		for _, old := range historicalMimeSets() {
			if sameSet(set, old) {
				if err := reset("file", "allowed_mime_types"); err != nil {
					return err
				}
				break
			}
		}
	}

	stored, err = s.rawStored(ctx, "chat", "model_option_allowed_paths")
	if err != nil {
		return err
	}
	if isHistoricalAllowedDoc(stored) {
		return reset("chat", "model_option_allowed_paths")
	}
	return nil
}

// rawStored returns the effective stored value without trimming; "" when the
// row is absent.
func (s *Service) rawStored(ctx context.Context, ns, key string) (string, error) {
	rows, err := s.repo.find(ctx, ns, key)
	if err != nil {
		return "", err
	}
	if eff, _ := collapseRows(rows); len(eff) > 0 {
		return eff[0].Value, nil
	}
	return "", nil
}
