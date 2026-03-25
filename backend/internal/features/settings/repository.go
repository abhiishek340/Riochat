package settings

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// repo is the storage layer of system_settings. Reads never fail on duplicate
// rows: callers collapse them with collapseRows.
type repo struct{ db *gorm.DB }

// all lists every row in insertion order.
func (r repo) all(ctx context.Context) ([]SystemSetting, error) {
	var rows []SystemSetting
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, database.Translate(err)
	}
	return rows, nil
}

// byNamespace lists the rows of one namespace ordered by key, then id.
func (r repo) byNamespace(ctx context.Context, ns string) ([]SystemSetting, error) {
	var rows []SystemSetting
	err := r.db.WithContext(ctx).Where("namespace = ?", ns).Order(quotedKey + " ASC").Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	return rows, nil
}

// find returns every row stored for (ns, key), lowest id first.
func (r repo) find(ctx context.Context, ns, key string) ([]SystemSetting, error) {
	var rows []SystemSetting
	err := r.db.WithContext(ctx).Where("namespace = ? AND "+quotedKey+" = ?", ns, key).Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	return rows, nil
}

// upsertValue sets the value of (ns, key). An existing row only has its value
// and updated_at changed; a missing row is created with the given type and
// description.
func (r repo) upsertValue(ctx context.Context, ns, key, value, valueType, desc string, now time.Time) error {
	return upsertRow(r.db.WithContext(ctx), ns, key, value, valueType, desc, now, false)
}

// upsertWithDescription is upsertValue that also refreshes the description.
func (r repo) upsertWithDescription(ctx context.Context, ns, key, value, valueType, desc string, now time.Time) error {
	return upsertRow(r.db.WithContext(ctx), ns, key, value, valueType, desc, now, true)
}

// upsertRow updates in place and falls back to an insert. It does not rely on
// ON CONFLICT so it also works on databases whose unique index is missing.
func upsertRow(tx *gorm.DB, ns, key, value, valueType, desc string, now time.Time, withDesc bool) error {
	changes := map[string]any{"value": value, "updated_at": now}
	if withDesc {
		changes["description"] = desc
	}
	res := tx.Model(&SystemSetting{}).Where("namespace = ? AND "+quotedKey+" = ?", ns, key).Updates(changes)
	if res.Error != nil {
		return database.Translate(res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}
	row := SystemSetting{Namespace: ns, Key: key, Value: value, ValueType: valueType, Description: desc, CreatedAt: now, UpdatedAt: now}
	err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	return database.Translate(err)
}

// insertMissing inserts rows, skipping any that already exist.
func (r repo) insertMissing(ctx context.Context, rows []SystemSetting) error {
	if len(rows) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	return database.Translate(err)
}

// deleteKey removes every row of (ns, key).
func (r repo) deleteKey(ctx context.Context, ns, key string) error {
	err := r.db.WithContext(ctx).Where("namespace = ? AND "+quotedKey+" = ?", ns, key).Delete(&SystemSetting{}).Error
	return database.Translate(err)
}

// removeRows deletes the given rows by id and rebuilds the unique index in the
// same transaction, so a database whose index had silently stopped enforcing
// uniqueness is repaired.
func (r repo) removeRows(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id IN ?", ids).Delete(&SystemSetting{}).Error; err != nil {
			return err
		}
		if err := tx.Exec("DROP INDEX IF EXISTS " + systemSettingsUniqueIndex).Error; err != nil {
			return err
		}
		return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS " + systemSettingsUniqueIndex +
			" ON system_settings (namespace, " + quotedKey + ")").Error
	})
}

// collapseRows reduces duplicate rows of the same (namespace, key) to one
// effective row, keeping the order in which keys first appear. losers lists the
// ids of the rows that were set aside.
//
// Among duplicates the winner is chosen by: a row an administrator evidently
// modified (updated_at differs from created_at, or its value differs from the
// registry default) over an untouched one; then the more recently updated row;
// then the lowest id.
func collapseRows(rows []SystemSetting) (effective []SystemSetting, losers []uint) {
	index := map[string]int{}
	for _, row := range rows {
		k := row.Namespace + "\x00" + row.Key
		i, seen := index[k]
		if !seen {
			index[k] = len(effective)
			effective = append(effective, row)
			continue
		}
		if betterRow(row, effective[i]) {
			losers = append(losers, effective[i].ID)
			effective[i] = row
		} else {
			losers = append(losers, row.ID)
		}
	}
	return effective, losers
}

func rowModified(row SystemSetting) bool {
	if !row.UpdatedAt.Equal(row.CreatedAt) {
		return true
	}
	if d := reg.lookup(row.Namespace, row.Key); d != nil {
		return row.Value != d.def
	}
	return false
}

// betterRow reports whether a should be preferred over b.
func betterRow(a, b SystemSetting) bool {
	am, bm := rowModified(a), rowModified(b)
	if am != bm {
		return am
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID < b.ID
}

// ---- user settings -----------------------------------------------------------

type userRepo struct{ db *gorm.DB }

// load returns the stored values of one user.
func (r userRepo) load(ctx context.Context, userID uint) (map[string]string, error) {
	var rows []UserSetting
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, database.Translate(err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// save upserts all values in one transaction.
func (r userRepo) save(ctx context.Context, userID uint, values map[string]string, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range values {
			row := UserSetting{UserID: userID, Key: k, Value: v, UpdatedAt: now}
			err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
				DoUpdates: clause.Assignments(map[string]any{"value": v, "updated_at": now}),
			}).Create(&row).Error
			if err != nil {
				return database.Translate(err)
			}
		}
		return nil
	})
}
