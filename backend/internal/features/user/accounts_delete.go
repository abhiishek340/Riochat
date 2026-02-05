package user

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// purgeStep deletes one family of a user's rows. The statement receives the
// user id for every placeholder. It is skipped when any listed table does not
// exist yet (the owning area is not part of this deployment).
type purgeStep struct {
	name   string
	tables []string
	sql    string
	// args is how many times the user id is bound.
	args int
}

const (
	userConversations = `SELECT id FROM chat_conversations WHERE user_id = ?`
	userProjects      = `SELECT id FROM chat_conversation_projects WHERE user_id = ?`
	userRuns          = `SELECT run_id FROM chat_runs WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`
	userPrivateKBs    = `SELECT id FROM knowledge_bases WHERE scope = 'user' AND owner_user_id = ?`
	userFiles         = `SELECT id FROM file_objects WHERE user_id = ?`
)

// purgeSteps lists the deletions in dependency order: children before parents,
// the account row last. Usage ledger, balance transactions and payment orders
// are deliberately absent: they are kept for financial audit.
var purgeSteps = []purgeStep{
	{"credentials", []string{"identity_credentials"}, `DELETE FROM identity_credentials WHERE user_id = ?`, 1},
	{"sessions", []string{"identity_sessions"}, `DELETE FROM identity_sessions WHERE user_id = ?`, 1},
	{"auth_events", []string{"identity_auth_events"}, `DELETE FROM identity_auth_events WHERE user_id = ?`, 1},
	{"contact_verifications", []string{"identity_contact_verifications"}, `DELETE FROM identity_contact_verifications WHERE user_id = ?`, 1},
	{"identity_links", []string{"identity_user_links"}, `DELETE FROM identity_user_links WHERE user_id = ?`, 1},
	{"two_factor_settings", []string{"identity_mfa_settings"}, `DELETE FROM identity_mfa_settings WHERE user_id = ?`, 1},
	{"trusted_devices", []string{"identity_trusted_devices"}, `DELETE FROM identity_trusted_devices WHERE user_id = ?`, 1},

	{"context_records", []string{"chat_context_records", "chat_conversations"},
		`DELETE FROM chat_context_records WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"run_events", []string{"chat_run_events", "chat_runs", "chat_conversations"},
		`DELETE FROM chat_run_events WHERE run_id IN (` + userRuns + `)`, 2},
	{"runs", []string{"chat_runs", "chat_conversations"},
		`DELETE FROM chat_runs WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"attachments", []string{"chat_attachments", "chat_conversations"},
		`DELETE FROM chat_attachments WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"message_chunks", []string{"chat_message_chunks", "chat_conversations"},
		`DELETE FROM chat_message_chunks WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"message_ui_states", []string{"chat_message_ui_states", "chat_conversations"},
		`DELETE FROM chat_message_ui_states WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"message_feedback", []string{"chat_feedback", "chat_conversations"},
		`DELETE FROM chat_feedback WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"conversation_shares", []string{"chat_conversation_shares", "chat_conversations"},
		`DELETE FROM chat_conversation_shares WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"messages", []string{"chat_messages", "chat_conversations"},
		`DELETE FROM chat_messages WHERE user_id = ? OR conversation_id IN (` + userConversations + `)`, 2},
	{"conversations", []string{"chat_conversations"}, `DELETE FROM chat_conversations WHERE user_id = ?`, 1},

	{"project_mcp_tools", []string{"chat_conversation_project_mcp_tools", "chat_conversation_projects"},
		`DELETE FROM chat_conversation_project_mcp_tools WHERE project_id IN (` + userProjects + `)`, 1},
	{"project_skills", []string{"chat_conversation_project_skills", "chat_conversation_projects", "skills"},
		`DELETE FROM chat_conversation_project_skills WHERE project_id IN (` + userProjects + `)
		 OR skill_id IN (SELECT id FROM skills WHERE scope = 'user' AND owner_user_id = ?)`, 2},
	{"project_skills_no_skill_table", []string{"chat_conversation_project_skills", "chat_conversation_projects"},
		`DELETE FROM chat_conversation_project_skills WHERE project_id IN (` + userProjects + `)`, 1},
	{"project_knowledge_bases", []string{"chat_conversation_project_knowledge_bases", "chat_conversation_projects", "knowledge_bases"},
		`DELETE FROM chat_conversation_project_knowledge_bases WHERE project_id IN (` + userProjects + `)
		 OR knowledge_base_id IN (SELECT public_id FROM knowledge_bases WHERE scope = 'user' AND owner_user_id = ?)
		 OR knowledge_base_id IN (SELECT CAST(id AS TEXT) FROM knowledge_bases WHERE scope = 'user' AND owner_user_id = ?)`, 3},
	{"project_knowledge_bases_no_kb_table", []string{"chat_conversation_project_knowledge_bases", "chat_conversation_projects"},
		`DELETE FROM chat_conversation_project_knowledge_bases WHERE project_id IN (` + userProjects + `)`, 1},
	{"projects", []string{"chat_conversation_projects"}, `DELETE FROM chat_conversation_projects WHERE user_id = ?`, 1},

	{"skills", []string{"skills"}, `DELETE FROM skills WHERE scope = 'user' AND owner_user_id = ?`, 1},
	{"personal_providers", []string{"llm_user_providers"}, `DELETE FROM llm_user_providers WHERE user_id = ?`, 1},

	{"knowledge_base_files", []string{"knowledge_base_files", "knowledge_bases", "file_objects"},
		`DELETE FROM knowledge_base_files WHERE knowledge_base_id IN (` + userPrivateKBs + `)
		 OR file_object_id IN (` + userFiles + `)`, 2},
	{"knowledge_base_files_no_file_table", []string{"knowledge_base_files", "knowledge_bases"},
		`DELETE FROM knowledge_base_files WHERE knowledge_base_id IN (` + userPrivateKBs + `)`, 1},
	{"knowledge_bases", []string{"knowledge_bases"}, `DELETE FROM knowledge_bases WHERE scope = 'user' AND owner_user_id = ?`, 1},

	{"file_chunks", []string{"file_chunks"}, `DELETE FROM file_chunks WHERE user_id = ?`, 1},
	{"file_objects", []string{"file_objects"}, `DELETE FROM file_objects WHERE user_id = ?`, 1},
	{"storage_quotas", []string{"file_storage_quotas"}, `DELETE FROM file_storage_quotas WHERE user_id = ?`, 1},
	{"memory_embeddings", []string{"user_memory_embeddings"}, `DELETE FROM user_memory_embeddings WHERE user_id = ?`, 1},
	{"memories", []string{"user_memories"}, `DELETE FROM user_memories WHERE user_id = ?`, 1},
	{"user_settings", []string{"user_settings"}, `DELETE FROM user_settings WHERE user_id = ?`, 1},
	{"permission_group_access", []string{"permission_group_user_access"}, `DELETE FROM permission_group_user_access WHERE user_id = ?`, 1},
	{"subscriptions", []string{"billing_subscriptions"}, `DELETE FROM billing_subscriptions WHERE user_id = ?`, 1},
	{"billing_account", []string{"billing_accounts"}, `DELETE FROM billing_accounts WHERE user_id = ?`, 1},
}

// variantOf returns the name of the richer step a fallback step stands in for,
// or "" for an ordinary step. A fallback runs only when its richer sibling was
// skipped because a table it joins against does not exist.
func (s purgeStep) variantOf() string {
	for _, marker := range []string{"_no_skill_table", "_no_kb_table", "_no_file_table"} {
		if len(s.name) > len(marker) && s.name[len(s.name)-len(marker):] == marker {
			return s.name[:len(s.name)-len(marker)]
		}
	}
	return ""
}

func tableExists(tx *gorm.DB, known map[string]bool, name string) (bool, error) {
	if v, ok := known[name]; ok {
		return v, nil
	}
	v := tx.Migrator().HasTable(name)
	known[name] = v
	return v, nil
}

func missingTable(tx *gorm.DB, known map[string]bool, tables []string) (bool, error) {
	for _, t := range tables {
		ok, err := tableExists(tx, known, t)
		if err != nil {
			return false, err
		}
		if !ok {
			return true, nil
		}
	}
	return false, nil
}

// HardDelete permanently removes an account and everything that hangs off it,
// soft-deleted rows included, in one transaction. A failing step rolls
// everything back and the error names the step.
//
// It refuses (ErrOwnerFileReference) when the user owns active files that a
// built-in knowledge base references. Usage ledger, balance transactions and
// payment orders are kept. Physical file bytes are the caller's concern: see
// FileStoragePaths, which must be read before the deletion.
func (a *Accounts) HardDelete(ctx context.Context, userID uint) error {
	return a.conn(ctx).Transaction(func(tx *gorm.DB) error {
		known := map[string]bool{}
		if skip, err := missingTable(tx, known, []string{"knowledge_base_files", "knowledge_bases", "file_objects"}); err != nil {
			return err
		} else if !skip {
			var n int64
			err := tx.Raw(`SELECT COUNT(*) FROM knowledge_base_files kbf
				JOIN knowledge_bases kb ON kb.id = kbf.knowledge_base_id
				JOIN file_objects f ON f.id = kbf.file_object_id
				WHERE kb.scope = 'builtin' AND f.user_id = ? AND f.status = 'active' AND f.deleted_at IS NULL`, userID).Scan(&n).Error
			if err != nil {
				return fmt.Errorf("delete user: check builtin knowledge base references: %w", err)
			}
			if n > 0 {
				return ErrOwnerFileReference
			}
		}

		if err := purgeRelated(tx, known, userID); err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id = ?", userID).Delete(&Account{}).Error; err != nil {
			return fmt.Errorf("delete user: account: %w", err)
		}
		return nil
	})
}

// FileStoragePaths lists the distinct object-storage paths of all files a user
// owns, soft-deleted ones included, so the caller can remove the bytes.
func (a *Accounts) FileStoragePaths(ctx context.Context, userID uint) ([]string, error) {
	db := a.conn(ctx)
	if !db.Migrator().HasTable("file_objects") {
		return []string{}, nil
	}
	var paths []string
	err := db.Raw(`SELECT DISTINCT storage_path FROM file_objects WHERE user_id = ? AND storage_path <> '' ORDER BY storage_path`, userID).
		Scan(&paths).Error
	if err != nil {
		return nil, err
	}
	if paths == nil {
		paths = []string{}
	}
	return paths, nil
}

// purgeRelated runs every purge step for the user inside tx.
func purgeRelated(tx *gorm.DB, known map[string]bool, userID uint) error {
	ran := map[string]bool{}
	for _, step := range purgeSteps {
		if base := step.variantOf(); base != "" && ran[base] {
			continue
		}
		skip, err := missingTable(tx, known, step.tables)
		if err != nil {
			return fmt.Errorf("delete user: %s: %w", step.name, err)
		}
		if skip {
			continue
		}
		args := make([]any, step.args)
		for i := range args {
			args[i] = userID
		}
		if err := tx.Exec(step.sql, args...).Error; err != nil {
			return fmt.Errorf("delete user: %s: %w", step.name, err)
		}
		ran[step.name] = true
		if base := step.variantOf(); base != "" {
			ran[base] = true
		}
	}
	return nil
}

// PurgeRelated removes the data every area keeps for the user (conversations,
// files, knowledge bases, memories, settings, subscriptions and so on) inside
// the caller's transaction. It does not delete the account row, the usage
// ledger or payment records. The caller is responsible for the files' bytes
// (see FileStoragePaths).
func PurgeRelated(tx *gorm.DB, userID uint) error {
	return purgeRelated(tx, map[string]bool{}, userID)
}
