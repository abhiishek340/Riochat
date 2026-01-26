package user

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations returns the area's idempotent steps. Only user_settings belongs to
// this area; the identity and billing tables it administers are migrated by
// their owners.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Setting{}),
	}
}
