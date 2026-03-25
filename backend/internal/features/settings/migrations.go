package settings

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations returns the area's schema steps. Built-in rows are seeded when the
// area is mounted, because writing them needs the data-encryption key ring.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &SystemSetting{}, &UserSetting{}),
	}
}
