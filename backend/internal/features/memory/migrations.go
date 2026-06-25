package memory

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations returns the area's idempotent steps.
func Migrations() []migrate.Step {
	// The live-row uniqueness rule is a partial index: a soft-deleted entry
	// must never block re-creating the same key, and two users may use the
	// same key. Both supported databases accept the same statement.
	const liveUnique = "CREATE UNIQUE INDEX IF NOT EXISTS uidx_user_memories_user_key_live " +
		"ON user_memories (user_id, memory_key) WHERE deleted_at IS NULL"
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Memory{}, &vectorRow{}),
		migrate.Exec("live_user_key_unique", migrate.PhaseSchema, map[string]string{
			"sqlite":   liveUnique,
			"postgres": liveUnique,
		}),
	}
}
