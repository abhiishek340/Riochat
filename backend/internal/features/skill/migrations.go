package skill

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// legacyColumns were replaced by the markdown column and are dropped when an
// older database still has them.
var legacyColumns = []string{"content", "sections_json"}

// Migrations returns the area's idempotent steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Skill{}, &ProjectLink{}),
		{Name: "drop_legacy_columns", Phase: migrate.PhaseSchema, Up: dropLegacyColumns},
	}
}

func dropLegacyColumns(ctx context.Context, db *gorm.DB) error {
	m := db.WithContext(ctx).Migrator()
	for _, col := range legacyColumns {
		if m.HasColumn(&Skill{}, col) {
			// The column names are package constants, never user input.
			if err := db.WithContext(ctx).Exec("ALTER TABLE skills DROP COLUMN " + col).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
