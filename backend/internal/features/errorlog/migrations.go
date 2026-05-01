package errorlog

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations returns the area's idempotent schema steps. The table has no seed
// data.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Entry{}),
	}
}
