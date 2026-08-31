package contentmoderation

import (
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent steps.
//
// The columns this feature adds to the conversation run and message tables
// (moderation_state, moderation_event_id, moderation_categories_json) belong to
// the models of the area that owns those tables; see GAPS.md.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Event{}, &DailyStat{}, &settingRow{}),
	}
}
