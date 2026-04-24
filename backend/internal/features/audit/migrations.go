package audit

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations creates the audit table and an index that serves the default
// time-range and time-ordered listings.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Log{}),
		migrate.Exec("created_at_index", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)",
		}),
	}
}
