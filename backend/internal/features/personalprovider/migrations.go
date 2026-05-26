package personalprovider

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations returns the area's idempotent schema steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Provider{}, &settingRow{}),
	}
}
