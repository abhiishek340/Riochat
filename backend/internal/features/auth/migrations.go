package auth

import (
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent schema steps. Seeds are not needed:
// the bootstrap administrator is created by a start hook because it logs a
// one-time password.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables",
			&User{}, &Credential{}, &Session{}, &AuthEvent{}, &ContactVerification{},
			&Provider{}, &UserLink{}, &MFASettings{}, &TrustedDevice{},
		),
		migrate.Exec("users_avatar_file_index", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_identity_users_avatar_file ON identity_users(avatar_url) WHERE avatar_url LIKE 'file:%'",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_identity_users_avatar_file ON identity_users(avatar_url) WHERE avatar_url LIKE 'file:%'",
		}),
		migrate.Exec("verifications_lookup_index", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_identity_contact_verifications_lookup ON identity_contact_verifications(user_id, purpose, target)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_identity_contact_verifications_lookup ON identity_contact_verifications(user_id, purpose, target)",
		}),
	}
}
