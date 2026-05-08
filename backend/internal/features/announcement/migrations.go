package announcement

import (
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent schema steps. AutoMigrate adds any
// column or index a database created by an older release lacks, without
// touching existing rows.
func Migrations() []migrate.Step {
	both := func(sql string) map[string]string {
		return map[string]string{"sqlite": sql, "postgres": sql}
	}
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Announcement{}, &UserState{}),
		migrate.Exec("listing_order_index", migrate.PhaseSchema, both(
			"CREATE INDEX IF NOT EXISTS idx_system_announcements_listing ON system_announcements (pinned, priority, updated_at, id)")),
		migrate.Exec("dismissed_lookup_index", migrate.PhaseSchema, both(
			"CREATE INDEX IF NOT EXISTS idx_announcement_user_states_dismissed ON announcement_user_states (user_id, dismissed_until) WHERE dismissed_until IS NOT NULL")),
		migrate.Exec("closed_lookup_index", migrate.PhaseSchema, both(
			"CREATE INDEX IF NOT EXISTS idx_announcement_user_states_closed ON announcement_user_states (user_id, closed_at) WHERE closed_at IS NOT NULL")),
	}
}
