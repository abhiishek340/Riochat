package usersettings

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations creates the user_settings table with its unique (user_id, key)
// index and the plain user_id index. AutoMigrate is idempotent.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &UserSetting{}),
	}
}
