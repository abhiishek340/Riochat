package example

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent steps. Schema steps of every area run
// before any seed step.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Example{}),
		migrate.Exec("owner_title_index", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_user_examples_owner_title ON user_examples(owner_id, title)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_user_examples_owner_title ON user_examples(owner_id, title)",
		}),
		migrate.Seed("welcome_row", func(ctx context.Context, db *gorm.DB) error {
			// Idempotent: look up by a natural key, create only when absent.
			row := Example{OwnerID: 0, Title: "Welcome"}
			return db.WithContext(ctx).Where("owner_id = ? AND title = ?", row.OwnerID, row.Title).FirstOrCreate(&row).Error
		}),
	}
}
