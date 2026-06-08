package promptreset

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// legacyColumns were dropped from the table in an earlier redesign; databases
// created by older releases may still carry them.
var legacyColumns = []string{"use_count", "last_used_at", "category", "tags_json"}

// Migrations returns the area's idempotent steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &PromptPreset{}),
		{Name: "drop_legacy_columns", Phase: migrate.PhaseSchema, Up: dropLegacyColumns},
	}
}

// dropLegacyColumns removes obsolete columns when present and does nothing when
// they are absent, so running it repeatedly is harmless. Indexes that mention a
// legacy column are dropped first because SQLite refuses to drop an indexed
// column.
func dropLegacyColumns(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	m := db.Migrator()
	table := PromptPreset{}.TableName()
	for _, col := range legacyColumns {
		if !m.HasColumn(table, col) {
			continue
		}
		indexes, err := m.GetIndexes(table)
		if err != nil {
			return err
		}
		for _, idx := range indexes {
			if primary, _ := idx.PrimaryKey(); primary {
				continue
			}
			for _, c := range idx.Columns() {
				if c == col {
					if err := m.DropIndex(table, idx.Name()); err != nil {
						return err
					}
					break
				}
			}
		}
		// col comes from the fixed list above, never from input.
		if err := db.Exec("ALTER TABLE " + table + " DROP COLUMN " + col).Error; err != nil {
			return err
		}
	}
	return nil
}
